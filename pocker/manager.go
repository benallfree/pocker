package pocker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
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

	mu        sync.RWMutex
	instances map[string]Instance // by ID
	hosts     map[string]string   // host -> ID
	apps      map[string]*appSlot // by ID
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
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// NewManager creates a Manager. Call [Manager.Run] to load instances and follow changes.
func NewManager(source InstanceSource, opts ManagerOptions) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		source:    source,
		opts:      appOptions{PBRoot: opts.PBRoot, HooksPoolSize: opts.HooksPoolSize, ForceDev: opts.Dev},
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
		m.upsertLocked(c.Instance)
		m.mu.Unlock()
		m.log.Info("pocker: instance updated", "id", c.Instance.ID)
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
		} else {
			m.log.Info("pocker: instance started", "id", inst.ID, "ms", time.Since(t).Milliseconds())
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

	app, err := m.app(inst)
	if err != nil {
		http.Error(w, "instance failed to start: "+err.Error(), http.StatusBadGateway)
		return
	}
	app.handler.ServeHTTP(w, r)
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
