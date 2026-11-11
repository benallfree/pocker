package pocker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
)

// HealthPath answers on any host with the engine's status.
const HealthPath = "/_pocker/health"

var validID = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// ValidID reports whether id is a safe instance id: lowercase letters, digits, dots and dashes, starting and ending
// with a letter or digit, no "..". Ids are folder names under the PocketBase root.
func ValidID(id string) bool {
	return validID.MatchString(id) && !strings.Contains(id, "..")
}

// Manager keeps the instance list from an [InstanceSource] and serves each instance's app, starting it on its first
// request and keeping it running.
type Manager struct {
	source InstanceSource
	opts   appOptions
	log    *slog.Logger

	dev bool

	defaults Limits
	onEvent  func(Event)
	gate     Gate
	forward  func(w http.ResponseWriter, r *http.Request, inst Instance)

	held map[string]chan struct{} // by ID: instances under maintenance; requests wait for the channel to close

	mu        sync.RWMutex
	qstate    map[string]*quarantineState // by ID
	paused    map[string]time.Time        // by ID: quarantined until
	instances map[string]Instance         // by ID
	hosts     map[string]string           // host -> ID
	apps      map[string]*appSlot         // by ID
	ready     bool
}

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
	// OnEvent receives instance events (memory strikes, pauses, restarts), e.g. to log them or notify the owner.
	OnEvent func(Event)
	// Gate, if set, admits requests: a peer check before routing, and per-instance admission (e.g. rate limits)
	// before an instance is served here or forwarded (see [Gate]).
	Gate Gate
	// Init runs host Go code for each app before it bootstraps (plugins implementing [AppIniter]).
	Init func(inst Instance, app core.App) error
	// Setup runs host Go code for each app after migrations and before it serves (see [Server.SetupApp]).
	Setup func(inst Instance, app core.App) error
	// Forward serves requests for instances with [Instance.Remote] set, after the firewall checks. Nil = a plain
	// reverse proxy to Remote.
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// NewManager creates a Manager. Call [Manager.Run] to load instances and follow changes.
func NewManager(source InstanceSource, opts ManagerOptions) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	defaults := opts.Defaults.over(DefaultLimits())
	return &Manager{
		source:    source,
		opts:      appOptions{PBRoot: opts.PBRoot, HooksPoolSize: opts.HooksPoolSize, ForceDev: opts.Dev, Defaults: defaults, Init: opts.Init, Setup: opts.Setup},
		defaults:  defaults,
		onEvent:   opts.OnEvent,
		gate:      opts.Gate,
		forward:   opts.Forward,
		qstate:    map[string]*quarantineState{},
		paused:    map[string]time.Time{},
		held:      map[string]chan struct{}{},
		log:       opts.Logger,
		dev:       opts.Dev,
		instances: map[string]Instance{},
		hosts:     map[string]string{},
		apps:      map[string]*appSlot{},
	}
}

// Run loads the snapshot, marks the manager ready and applies changes until ctx is done.
func (m *Manager) Run(ctx context.Context) error {
	list, err := m.source.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("instance snapshot: %w", err)
	}
	m.mu.Lock()
	for _, inst := range list {
		m.upsertLocked(inst)
	}
	m.ready = true
	m.mu.Unlock()
	m.log.Info("pocker: instances loaded", "count", len(list))

	changes, err := m.source.Watch(ctx)
	if err != nil {
		return fmt.Errorf("instance watch: %w", err)
	}
	if changes == nil {
		<-ctx.Done()
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-changes:
			if !ok {
				return nil
			}
			m.apply(c)
		}
	}
}

func (m *Manager) apply(c Change) {
	switch c.Kind {
	case Upsert:
		m.mu.Lock()
		old, existed := m.instances[c.Instance.ID]
		m.upsertLocked(c.Instance)
		var stale *appSlot
		if existed && !sameRuntime(old, c.Instance) {
			// env (secrets), limits, dev mode or folder changed: the next request starts a fresh app
			stale = m.apps[c.Instance.ID]
			delete(m.apps, c.Instance.ID)
		}
		m.mu.Unlock()
		m.log.Info("pocker: instance updated", "id", c.Instance.ID)
		if stale != nil {
			m.emit(Event{Kind: EventRestarted, InstanceID: c.Instance.ID, Reason: "settings changed"})
			go retire(stale)
		}
	case Delete:
		m.mu.Lock()
		slot := m.apps[c.Instance.ID]
		m.removeLocked(c.Instance.ID)
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

// upsertLocked replaces the instance record and its host routes. A running app keeps running with the settings
// it started with (v1: no in-place reload).
func (m *Manager) upsertLocked(inst Instance) {
	if !ValidID(inst.ID) {
		m.log.Warn("pocker: ignoring instance with an unsafe id", "id", inst.ID)
		return
	}
	m.removeLocked(inst.ID)
	m.instances[inst.ID] = inst
	for _, h := range inst.Hosts {
		h = normalizeHost(h)
		if other, taken := m.hosts[h]; taken && other != inst.ID {
			m.log.Warn("pocker: host claimed by two instances; keeping the first", "host", h, "kept", other, "ignored", inst.ID)
			continue
		}
		m.hosts[h] = inst.ID
	}
}

func (m *Manager) removeLocked(id string) {
	old, ok := m.instances[id]
	if !ok {
		return
	}
	for _, h := range old.Hosts {
		h = normalizeHost(h)
		if m.hosts[h] == id {
			delete(m.hosts, h)
		}
	}
	delete(m.instances, id)
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// Lookup returns the instance that serves host.
func (m *Manager) Lookup(host string) (Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.hosts[normalizeHost(host)]
	if !ok {
		return Instance{}, false
	}
	inst, ok := m.instances[id]
	return inst, ok
}

// DevID is the canonical id dev mode gives a hostname the instance source doesn't map: the hostname in normal form
// (lowercase, no port, no trailing dot). It returns "" when that isn't a safe id (see [ValidID]).
func DevID(host string) string {
	id := normalizeHost(host)
	if !ValidID(id) {
		return ""
	}
	return id
}

// resolve maps the request's Host to an instance through the source's hostname -> id map. In dev mode an unmapped
// host becomes a new instance whose id is the hostname ([DevID]). A non-zero status means the request can't be routed.
func (m *Manager) resolve(r *http.Request) (Instance, int, string) {
	host := normalizeHost(r.Host)
	if inst, ok := m.Lookup(host); ok {
		return inst, 0, ""
	}
	if m.dev {
		id := DevID(host)
		if id == "" {
			return Instance{}, http.StatusBadRequest, "hostname can't be used as an instance id: " + host
		}
		return m.devInstance(id, host), 0, ""
	}
	return Instance{}, http.StatusNotFound, host + " not found"
}

// devInstance registers (or returns) the dev-mode instance for host.
func (m *Manager) devInstance(id, host string) Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		return inst
	}
	inst := Instance{ID: id, Hosts: []string{host}, Dev: true}
	m.upsertLocked(inst)
	m.log.Info("pocker: dev instance", "host", host, "id", id)
	return inst
}

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
			m.log.Info("pocker: instance started", "id", inst.ID, "ms", time.Since(t).Milliseconds())
			m.emit(Event{Kind: EventStarted, InstanceID: inst.ID})
		}
		return slot.app, slot.err
	}
	m.mu.Unlock()
	<-slot.done
	return slot.app, slot.err
}

// ServeHTTP routes a request to its instance by Host.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == HealthPath {
		m.serveHealth(w)
		return
	}
	if m.gate != nil && !m.gate.AllowPeer(r) {
		http.Error(w, "forbidden: "+peerAddr(r).String(), http.StatusForbidden)
		return
	}

	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()
	if !ready {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}

	inst, status, msg := m.resolve(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	if inst.Blocked != nil {
		status := inst.Blocked.Status
		if status == 0 {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(inst.Blocked.Message))
		return
	}

	if inst.Remote != "" {
		m.serveRemote(w, r, inst)
		return
	}

	if err := m.waitHeld(r.Context(), inst.ID); err != nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "this instance is being updated; retry shortly", http.StatusServiceUnavailable)
		return
	}

	if until, paused := m.pausedUntil(inst.ID); paused {
		w.Header().Set("Retry-After", fmt.Sprint(int(time.Until(until).Seconds())+1))
		http.Error(w, "this instance is paused for using too much memory; retry later", http.StatusServiceUnavailable)
		return
	}

	if m.gate != nil {
		release, ok := m.gate.Admit(w, r, inst)
		if !ok {
			return
		}
		defer release() // long-lived responses (realtime) hold their admission until they end
	}

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

// serveRemote forwards a request for an instance another server runs, after admission.
func (m *Manager) serveRemote(w http.ResponseWriter, r *http.Request, inst Instance) {
	if m.gate != nil {
		release, ok := m.gate.Admit(w, r, inst)
		if !ok {
			return
		}
		defer release()
	}
	if m.forward != nil {
		m.forward(w, r, inst)
		return
	}
	target, err := url.Parse(inst.Remote)
	if err != nil || target.Host == "" {
		http.Error(w, "bad remote for "+inst.ID, http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = forwardTransport
	proxy.ErrorLog = slog.NewLogLogger(m.log.Handler(), slog.LevelWarn)
	proxy.ServeHTTP(w, r)
}

// forwardTransport is the default forwarder's own transport: http.DefaultTransport carries the JS egress policy,
// which blocks private addresses, where other servers usually are.
var forwardTransport = &http.Transport{
	DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConnsPerHost: 64,
	IdleConnTimeout:     90 * time.Second,
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

func (m *Manager) serveHealth(w http.ResponseWriter) {
	m.mu.RLock()
	ready := m.ready
	instances := len(m.instances)
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
	m.mu.RUnlock()

	status := http.StatusOK
	body := map[string]any{"status": "ok", "instances": instances, "running": running}
	if !ready {
		status = http.StatusServiceUnavailable
		body["status"] = "starting"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
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
}

// Event is something that happened to an instance, for logs or owner notifications.
type Event struct {
	Kind       EventKind
	InstanceID string
	Reason     string
	Until      time.Time // EventPaused: when requests are served again
	Strikes    int       // EventStrike: strikes within the window so far
}

// EventKind names an [Event].
type EventKind string

const (
	EventStrike    EventKind = "memory_strike" // a JS call was interrupted by the memory watch
	EventPaused    EventKind = "paused"        // too many strikes: app stopped, requests get 503 until Until
	EventRestarted EventKind = "restarted"     // settings changed: the app restarts on its next request
	EventStarted   EventKind = "started"       // the app started and serves requests
	EventFailed    EventKind = "failed"        // the app failed to start (Reason has the error)
)

func (m *Manager) emit(e Event) {
	if m.onEvent != nil {
		m.onEvent(e)
	}
}

type quarantineState struct {
	strikes    []time.Time
	level      int // quarantines so far (pause backoff)
	lastStrike time.Time
}

// sameRuntime reports whether two versions of an instance can share a running app.
func sameRuntime(a, b Instance) bool {
	return a.Remote == b.Remote && a.Dir == b.Dir && a.Dev == b.Dev && reflect.DeepEqual(a.Secrets, b.Secrets) && a.EncryptionKey == b.EncryptionKey &&
		reflect.DeepEqual(a.Access, b.Access) && reflect.DeepEqual(a.Limits, b.Limits) &&
		reflect.DeepEqual(a.Superusers, b.Superusers) && reflect.DeepEqual(a.Private, b.Private)
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

// Strike records a memory strike against an instance (from the [MemoryWatch]). With the instance's quarantine
// policy, enough strikes within the window pause it: its app is stopped to free memory and requests get 503 until
// the pause ends. Pauses back off on repeat quarantines and the backoff resets after a quiet period.
func (m *Manager) Strike(id string) {
	now := time.Now()
	m.mu.Lock()
	inst := m.instances[id]
	policy := *inst.Limits.over(m.defaults).Quarantine
	st := m.qstate[id]
	if st == nil {
		st = &quarantineState{}
		m.qstate[id] = st
	}
	if !st.lastStrike.IsZero() && now.Sub(st.lastStrike) > policy.ResetAfter {
		st.level = 0
	}
	st.lastStrike = now
	recent := st.strikes[:0]
	for _, t := range st.strikes {
		if now.Sub(t) < policy.Window {
			recent = append(recent, t)
		}
	}
	st.strikes = append(recent, now)
	count := len(st.strikes)

	var slot *appSlot
	var until time.Time
	if !policy.Off && count >= policy.Strikes {
		until = now.Add(policy.pauseFor(st.level))
		st.level++
		st.strikes = nil
		m.paused[id] = until
		slot = m.apps[id]
		delete(m.apps, id)
	}
	m.mu.Unlock()

	m.log.Warn("pocker: memory strike", "id", id, "strikes", count)
	m.emit(Event{Kind: EventStrike, InstanceID: id, Strikes: count, Reason: "memory"})
	if !until.IsZero() {
		m.log.Warn("pocker: instance paused for memory", "id", id, "until", until)
		m.emit(Event{Kind: EventPaused, InstanceID: id, Until: until, Reason: "memory"})
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

func (m *Manager) pausedUntil(id string) (time.Time, bool) {
	m.mu.RLock()
	until, ok := m.paused[id]
	m.mu.RUnlock()
	if !ok {
		return time.Time{}, false
	}
	if time.Now().After(until) {
		m.mu.Lock()
		delete(m.paused, id)
		m.mu.Unlock()
		return time.Time{}, false
	}
	return until, true
}

// InstanceDir returns the folder of a known instance (<pbRoot>/<id> unless the instance sets Dir).
func (m *Manager) InstanceDir(id string) (string, bool) {
	m.mu.RLock()
	inst, ok := m.instances[id]
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
	inst, ok := m.instances[id]
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
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

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
	inst, ok := m.instances[id]
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
