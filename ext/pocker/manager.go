package pocker

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/pockethost/pocketone/ext/host"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"github.com/dop251/goja"
)

// HealthPath answers on any host with the engine's status (see [host.DefaultHealthPath]).
const HealthPath = host.DefaultHealthPath

// ValidID reports whether id is a safe instance id (see [host.ValidID]).
func ValidID(id string) bool { return host.ValidID(id) }

// DevID is the id dev mode gives an unmapped hostname (see [host.DevID]).
func DevID(h string) string { return host.DevID(h) }

// Manager runs instances in-process: an [host.Runtime] that starts each instance's app on its first request and
// keeps it running. It reads instances from an [host.Directory] (its own, fed by the source, unless one is shared in
// [ManagerOptions.Directory]); as an http.Handler it serves the whole edge pipeline with itself as the only runtime.
type Manager struct {
	source InstanceSource
	opts   appOptions
	log    *slog.Logger

	dir     *host.Directory
	ownsDir bool
	handler *host.Handler

	defaults Limits
	onEvent  func(Event)
	hooks    *host.ScriptWatcher // restarts an app when its pb_hooks change
	gate     Gate

	held map[string]chan struct{} // by ID: instances under maintenance; requests wait for the channel to close

	mu   sync.RWMutex
	apps map[string]*appSlot // by ID
}

var (
	_ host.Runtime    = (*Manager)(nil)
	_ host.Prechecker = (*Manager)(nil)
	_ host.Syncer     = (*Manager)(nil)
	_ host.Shutdowner = (*Manager)(nil)
	_ host.Statser    = (*Manager)(nil)
)

// appSlot holds one app: a start in progress (done not yet closed) or a finished start (app or err set).
type appSlot struct {
	done chan struct{}
	app  *runningApp
	err  error
}

// ManagerOptions configures a [Manager].
type ManagerOptions struct {
	// PBRoot is the folder that holds one PocketBase folder per instance (<PBRoot>/<instance id>).
	PBRoot string
	// Dev serves any hostname: a host the source doesn't map gets [DevID] as its id and its folder is created on
	// first use. Apps run in PocketBase dev mode.
	Dev bool
	// HooksPoolSize is the number of prewarmed JS runtimes per app (0 = create one per hook run).
	HooksPoolSize int
	// Defaults are the per-instance limits used where an instance doesn't set its own (zero = DefaultLimits).
	Defaults Limits
	// OnEvent receives instance events (started, stopped, restarts), e.g. to log them or notify the owner.
	OnEvent func(Event)
	// Gate, if set, admits requests: a peer check before routing, and per-instance admission (e.g. rate limits)
	// before an instance is served here or forwarded (see [Gate]).
	Gate Gate
	// Init runs host Go code for each app before it bootstraps (plugins implementing [AppIniter]).
	Init func(inst Instance, app core.App) error
	// InitJS adds globals to each JS runtime after the sandbox set it up (plugins implementing [JSIniter]).
	InitJS func(inst Instance, app core.App, vm *goja.Runtime)
	// Setup runs host Go code for each app after migrations and before it serves (see [Server.SetupApp]).
	Setup func(inst Instance, app core.App) error
	// Forward serves requests for instances with [Instance.Remote] set, after the firewall checks. Nil = a plain
	// reverse proxy to Remote.
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)
	// Directory, when set, is shared with the rest of the edge (the host feeds it); the Manager then doesn't load
	// the source itself and [Manager.Run] only waits.
	Directory *host.Directory
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// NewManager creates a Manager. Call [Manager.Run] to load instances and follow changes.
func NewManager(source InstanceSource, opts ManagerOptions) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	defaults := opts.Defaults.Over(DefaultLimits())
	m := &Manager{
		source:   source,
		opts:     appOptions{PBRoot: opts.PBRoot, HooksPoolSize: opts.HooksPoolSize, ForceDev: opts.Dev, Defaults: defaults, Init: opts.Init, InitJS: opts.InitJS, Setup: opts.Setup},
		defaults: defaults,
		onEvent:  opts.OnEvent,
		gate:     opts.Gate,
		held:     map[string]chan struct{}{},
		log:      opts.Logger,
		apps:     map[string]*appSlot{},
		dir:      opts.Directory,
	}
	if m.dir == nil {
		m.dir = host.NewDirectory(opts.Logger)
		m.ownsDir = true
		m.dir.Subscribe(m.Sync) // with a shared directory, the host subscribes its runtime
	}
	m.hooks = host.NewScriptWatcher(opts.Logger, m.hooksChanged)
	m.handler = &host.Handler{Dir: m.dir, Gate: opts.Gate, Runtimes: []host.Runtime{m}, Forward: opts.Forward, Dev: opts.Dev, Logger: opts.Logger}
	return m
}

// Directory is the instance directory the Manager reads.
func (m *Manager) Directory() *host.Directory { return m.dir }

// Run loads the snapshot, marks the manager ready and applies changes until ctx is done. With a shared directory
// ([ManagerOptions.Directory]) the host feeds it and Run only waits for ctx.
func (m *Manager) Run(ctx context.Context) error {
	if !m.ownsDir {
		<-ctx.Done()
		return nil
	}
	return m.dir.Run(ctx, m.source, nil)
}

// Sync implements [host.Syncer]: a changed instance (env, limits, dev mode, folder) gets a fresh app on its next
// request; a removed one is stopped.
func (m *Manager) Sync(c Change, prev *Instance) {
	switch c.Kind {
	case Upsert:
		// A blocked instance (powered off, suspended, unpaid) must not keep running: its cron jobs and other
		// background work stop with the app, and unblocking starts a fresh app (re-reading pb_hooks) on the next
		// request or StartInstance.
		blocked := c.Instance.Blocked != nil
		var stale *appSlot
		if prev != nil && (blocked || !sameRuntime(*prev, c.Instance)) {
			m.mu.Lock()
			stale = m.apps[c.Instance.ID]
			delete(m.apps, c.Instance.ID)
			m.mu.Unlock()
		}
		m.log.Info("pocker: instance updated", "id", c.Instance.ID)
		if stale != nil {
			if blocked {
				m.emit(Event{Kind: EventStopped, InstanceID: c.Instance.ID, Reason: c.Instance.Blocked.Message})
			} else {
				m.emit(Event{Kind: EventRestarted, InstanceID: c.Instance.ID, Reason: "settings changed"})
			}
			go retire(stale)
		}
	case Delete:
		m.mu.Lock()
		slot := m.apps[c.Instance.ID]
		delete(m.apps, c.Instance.ID)
		m.mu.Unlock()
		m.log.Info("pocker: instance removed", "id", c.Instance.ID)
		if slot != nil {
			go func() {
				<-slot.done
				if slot.app != nil {
					stopApp(slot.app)
				}
			}()
		}
	}
}

// Lookup returns the instance that serves host.
func (m *Manager) Lookup(host string) (Instance, bool) { return m.dir.Lookup(host) }

// Name implements [host.Runtime].
func (m *Manager) Name() string { return "pocker" }

// Serves implements [host.Runtime]: every instance that doesn't ask for another runtime.
func (m *Manager) Serves(inst Instance) bool { return inst.Runtime == "" || inst.Runtime == "pocker" }

// app returns the instance's running app, starting it (once, shared by concurrent callers) if needed.
// A failed start is not cached: the next request tries again.
func (m *Manager) app(inst Instance) (*runningApp, error) {
	m.mu.Lock()
	slot, ok := m.apps[inst.ID]
	if !ok {
		slot = &appSlot{done: make(chan struct{})}
		m.apps[inst.ID] = slot
		m.mu.Unlock()

		t := time.Now()
		slot.app, slot.err = startApp(m.opts, inst)
		close(slot.done)
		if slot.err != nil {
			m.mu.Lock()
			if m.apps[inst.ID] == slot {
				delete(m.apps, inst.ID)
			}
			m.mu.Unlock()
			m.log.Error("pocker: instance failed to start", "id", inst.ID, "error", slot.err)
			openInstanceLog(instanceDir(m.opts, inst)).Write("stderr", "Instance failed to start: "+slot.err.Error())
			m.emit(Event{Kind: EventFailed, InstanceID: inst.ID, Reason: slot.err.Error()})
		} else {
			m.catchRestart(inst.ID, slot)
			m.watchHooks(inst, slot)
			m.log.Info("pocker: instance started", "id", inst.ID, "ms", time.Since(t).Milliseconds())
			m.emit(Event{Kind: EventStarted, InstanceID: inst.ID})
		}
		return slot.app, slot.err
	}
	m.mu.Unlock()
	<-slot.done
	return slot.app, slot.err
}

// ServeHTTP serves the edge pipeline (resolve by Host, gates, forwarding) with the Manager as the only runtime.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.handler.ServeHTTP(w, r) }

// Precheck implements [host.Prechecker]: instances under maintenance answer 503 before the gates.
func (m *Manager) Precheck(w http.ResponseWriter, r *http.Request, inst Instance) bool {
	if err := m.waitHeld(r.Context(), inst.ID); err != nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "this instance is being updated; retry shortly", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// ServeInstance implements [host.Runtime]: start the app on demand and serve the request.
func (m *Manager) ServeInstance(w http.ResponseWriter, r *http.Request, inst Instance) {
	wakeStart := time.Now()
	app, err := m.app(inst)
	if err != nil {
		http.Error(w, "instance failed to start: "+err.Error(), http.StatusBadGateway)
		return
	}
	readyAt := time.Now()
	Timing(r.Context(), func() []TimingEntry {
		return []TimingEntry{{Name: "origin", Dur: time.Since(readyAt)}, {Name: "wake", Dur: readyAt.Sub(wakeStart)}}
	})
	app.handler.ServeHTTP(w, r)
}

// Gate returns the manager's gate (nil if none), so host code in front of the manager (e.g. its own routes) can
// apply the same peer check.
func (m *Manager) Gate() Gate { return m.gate }

// LogInstance appends a line to the instance's log (<instance folder>/logs/exec.log, the same file its app writes),
// e.g. for a scheduled job's result. Unknown instances are ignored.
func (m *Manager) LogInstance(id, stream, message string) {
	dir, ok := m.InstanceDir(id)
	if !ok {
		return
	}
	openInstanceLog(dir).Write(stream, message)
}

// RunningCount implements [host.Statser]: apps currently running.
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	running := 0
	for _, s := range m.apps {
		select {
		case <-s.done:
			if s.app != nil {
				running++
			}
		default:
		}
	}
	return running
}

// watchHooks restarts the app when its pb_hooks change (PocketBase's HooksWatch, but only this app and with one
// shared watcher). Migrations are not watched: like PocketBase, they run when the app boots. The watch ends with this
// app, unless a newer app for the instance already took over.
func (m *Manager) watchHooks(inst Instance, slot *appSlot) {
	if m.hooks == nil {
		return
	}
	m.hooks.Add(inst.ID, filepath.Join(instanceDir(m.opts, inst), "pb_hooks"))
	slot.app.app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		m.mu.RLock()
		cur := m.apps[inst.ID]
		m.mu.RUnlock()
		if cur == nil || cur == slot {
			m.hooks.Remove(inst.ID)
		}
		return e.Next()
	})
}

// hooksChanged restarts the instance's running app (app.Restart, caught by catchRestart: only this app restarts).
func (m *Manager) hooksChanged(id string) {
	m.mu.RLock()
	slot := m.apps[id]
	m.mu.RUnlock()
	if slot == nil {
		return
	}
	select {
	case <-slot.done:
	default:
		return // still starting: it reads the current files
	}
	if slot.app == nil {
		return
	}
	m.log.Info("pocker: pb_hooks changed, restarting", "id", id)
	if inst, ok := m.dir.Get(id); ok {
		openInstanceLog(instanceDir(m.opts, inst)).Write("stdout", "pb_hooks changed, restarting")
	}
	go func() {
		if err := slot.app.app.Restart(); err != nil {
			m.log.Error("pocker: restart after pb_hooks change failed", "id", id, "error", err)
		}
	}()
}

// Shutdown stops every running app.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	slots := m.apps
	m.apps = map[string]*appSlot{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-s.done
			if s.app != nil {
				stopApp(s.app)
			}
		}()
	}
	wg.Wait()
	m.hooks.Close()
}

// Event is something that happened to an instance, for logs or owner notifications.
type Event struct {
	Kind       EventKind
	InstanceID string
	Reason     string
}

// EventKind names an [Event].
type EventKind string

const (
	EventRestarted EventKind = "restarted" // settings changed: the app restarts on its next request
	EventStarted   EventKind = "started"   // the app started and serves requests
	EventFailed    EventKind = "failed"    // the app failed to start (Reason has the error)
	EventStopped   EventKind = "stopped"   // the instance became blocked (e.g. powered off): its app was stopped
)

func (m *Manager) emit(e Event) {
	if m.onEvent != nil {
		m.onEvent(e)
	}
}

// sameRuntime reports whether two versions of an instance can share a running app. Plugin settings count: plugins
// read them once, when they're registered on the app.
func sameRuntime(a, b Instance) bool {
	return a.Remote == b.Remote && a.Dir == b.Dir && a.Dev == b.Dev && reflect.DeepEqual(a.Secrets, b.Secrets) && a.EncryptionKey == b.EncryptionKey &&
		reflect.DeepEqual(a.Access, b.Access) && reflect.DeepEqual(a.Limits, b.Limits) &&
		reflect.DeepEqual(a.Superusers, b.Superusers) && reflect.DeepEqual(a.Private, b.Private) &&
		reflect.DeepEqual(a.Plugins, b.Plugins)
}

// retire stops an app that new requests no longer reach, after giving in-flight requests time to finish.
func retire(slot *appSlot) {
	<-slot.done
	if slot.app == nil {
		return
	}
	time.Sleep(10 * time.Second)
	stopApp(slot.app)
}

// InstanceDir returns the folder of a known instance (<pbRoot>/<id> unless the instance sets Dir).
func (m *Manager) InstanceDir(id string) (string, bool) {
	m.mu.RLock()
	inst, ok := m.dir.Get(id)
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	return instanceDir(m.opts, inst), true
}

// WithInstanceStopped stops the instance's app (if running), holds its new requests, runs fn with the instance
// folder, then releases the held requests; the next request starts a fresh app. Use it to change an instance's
// files (deploys, restores) or to work on its databases while nothing has them open. Requests in flight when it
// starts are cut off, like stopping a process.
func (m *Manager) WithInstanceStopped(ctx context.Context, id string, fn func(dir string) error) error {
	m.mu.Lock()
	inst, ok := m.dir.Get(id)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance %q not found", id)
	}
	if _, busy := m.held[id]; busy {
		m.mu.Unlock()
		return fmt.Errorf("instance %q is already being updated", id)
	}
	release := make(chan struct{})
	m.held[id] = release
	slot := m.apps[id]
	delete(m.apps, id)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.held, id)
		m.mu.Unlock()
		close(release)
	}()
	if slot != nil {
		select {
		case <-slot.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if slot.app != nil {
			stopApp(slot.app)
		}
	}
	return fn(instanceDir(m.opts, inst))
}

// Ready reports whether the instance list has loaded (before that, requests get 503 "starting").
func (m *Manager) Ready() bool { return m.dir.Ready() }

// Running reports whether the instance's app is started (or starting).
func (m *Manager) Running(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.apps[id]
	return ok
}

// StartInstance starts the instance's app now, if it isn't running, and returns the start error (e.g. a broken
// hook or migration). Host code uses it after changing an instance's files to report the result right away instead
// of on the next request. A blocked instance isn't started.
func (m *Manager) StartInstance(ctx context.Context, id string) error {
	if err := m.waitHeld(ctx, id); err != nil {
		return err
	}
	m.mu.RLock()
	inst, ok := m.dir.Get(id)
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %q not found", id)
	}
	if inst.Blocked != nil {
		return fmt.Errorf("instance %q is blocked: %s", id, inst.Blocked.Message)
	}
	_, err := m.app(inst)
	return err
}

// waitHeld blocks while the instance is under maintenance (WithInstanceStopped), up to a minute.
func (m *Manager) waitHeld(ctx context.Context, id string) error {
	m.mu.RLock()
	ch := m.held[id]
	m.mu.RUnlock()
	if ch == nil {
		return nil
	}
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	select {
	case <-ch:
		return nil
	case <-t.C:
		return fmt.Errorf("instance %q still being updated", id)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// catchRestart makes app.Restart() (PocketBase's backup restore, plugins) restart this one app instead of
// re-executing the whole process, which would restart every instance. Two handlers: the innermost one replaces the
// re-exec (close the databases, drop the slot); the outermost one starts a fresh app once every other OnTerminate
// handler has finished (e.g. a plugin that detaches from the database after PocketBase closes it), so a start error
// is reported right away and the restored data is served next.
func (m *Manager) catchRestart(id string, slot *appSlot) {
	var caught bool
	slot.app.app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Priority: math.MinInt,
		Func: func(e *core.TerminateEvent) error {
			err := e.Next()
			if caught {
				caught = false
				go func() {
					if err := m.StartInstance(context.Background(), id); err != nil {
						m.log.Error("pocker: instance failed to restart", "id", id, "error", err)
					}
				}()
			}
			return err
		},
	})
	slot.app.app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Priority: math.MaxInt,
		Func: func(e *core.TerminateEvent) error {
			if !e.IsRestart {
				return e.Next()
			}
			caught = true
			m.mu.Lock()
			if m.apps[id] == slot {
				delete(m.apps, id)
			}
			m.mu.Unlock()
			m.log.Info("pocker: instance restarting (app.Restart)", "id", id)
			m.emit(Event{Kind: EventRestarted, InstanceID: id, Reason: "app restart"})
			return e.App.ResetBootstrapState()
		},
	})
}
