// Package docker is a runtime provider for ext/host: it runs each instance in its own container with an official
// PocketBase (or PocketOne) binary of the version the instance asks for, so one host can serve every version.
//
// Each instance folder (<DataRoot>/<id>) is mounted at /home/pockethost, the binary read-only at
// /home/pockethost/pocketbase, and the container runs `./pocketbase serve --http 0.0.0.0:8090` (plus --dev and
// --encryptionEnv when set), published on a loopback port the host proxies to. That is the layout PocketHost's Node
// edge uses, so its instance folders and binary cache move as they are. A container starts on the first request,
// stops after IdleTTL without requests, and keeps running across a host restart (the next process adopts it).
package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

// Labels on every container this runtime creates.
const (
	LabelRuntime  = "p1.runtime"  // "docker"
	LabelInstance = "p1.instance" // instance id
	LabelVersion  = "p1.version"  // resolved release: "pb:0.36.9" or "p1:0.0.1"
	LabelSpec     = "p1.spec"     // hash of the container's configuration
)

// Spec is what a host adds to an instance's container (see [Config.Prepare]).
type Spec struct {
	Binds  []string          // extra host:container[:ro] mounts, e.g. platform hooks
	Env    map[string]string // extra environment, applied over the instance's secrets
	Labels map[string]string
}

// Config configures a [Runtime].
type Config struct {
	Socket   string    // Docker socket (default [DefaultSocket])
	Image    string    // base image the binary runs in (required); pulled when missing
	DataRoot string    // host folder with one folder per instance (<DataRoot>/<id>; Instance.Dir overrides)
	Home     string    // the instance folder inside the container (default /home/pockethost)
	Binaries *Binaries // PocketBase binaries by version (required)

	DefaultVersion string // for instances without a Version (e.g. "pb:0.36.*" or "p1:0.0.*"); empty = such instances fail

	IdleTTL             time.Duration // stop after this long without requests (default 10m)
	MaxConcurrentStarts int           // containers starting at once (default 4)
	StartTimeout        time.Duration // until /api/health answers (default 60s)
	StopGrace           time.Duration // SIGINT, then kill after this (default 10s)

	// Per-container limits (0 = unlimited / Docker's default).
	Memory     int64 // bytes
	NanoCPUs   int64
	PidsLimit  int64
	NofileSoft int64
	NofileHard int64
	// Hardening: drop these capabilities ("ALL") and set no-new-privileges.
	CapDrop         []string
	NoNewPrivileges bool
	Network         string // Docker network (default: the bridge)
	HostIP          string // where container ports are published (default 127.0.0.1)

	// Serves picks the instances this runtime runs (default: those asking for "docker", or any version when nothing
	// else claims them; see [host.Runtime.Serves]).
	Serves func(inst host.Instance) bool
	// Prepare adds host-specific mounts, environment and labels (e.g. platform hooks, an app URL). version is the
	// resolved release with its line, e.g. "pb:0.36.9" or "p1:0.0.1".
	Prepare func(ctx context.Context, inst host.Instance, version string, spec *Spec) error
	// Version, if set, rewrites an instance's version before the binary is picked (e.g. "every instance on this host
	// runs PocketOne unless it says pb:").
	Version func(inst host.Instance) string

	// AdoptImage: at start, also adopt running containers of this image that carry no labels and are named after an
	// instance id (containers started by another manager, e.g. PocketHost's Node edge).
	AdoptImage string

	OnLog  func(instanceID string, stderr bool, line string) // container output (default: debug log)
	Logger *slog.Logger
}

// Runtime runs instances in containers; it implements [host.Runtime] (+ Syncer, Statser, Shutdowner, Starter).
type Runtime struct {
	cfg  Config
	e    *engine
	log  *slog.Logger
	sem  chan struct{}
	http *http.Client
	tr   *http.Transport

	mu    sync.Mutex
	ctrs  map[string]*ctr
	held  map[string]chan struct{} // instances under maintenance (WithInstanceStopped)
	ready atomic.Bool
	watch *host.ScriptWatcher // restarts a container when its pb_hooks change

	bg     context.Context
	cancel context.CancelFunc
}

type ctr struct {
	inst string

	mu        sync.Mutex     // serializes start/stop
	container string         // container id ("" = not running)
	last      *host.Instance // the instance as last started (nil = adopted, not started by this process)
	port      int
	hash      string // its LabelSpec ("" = adopted from another manager: kept until the instance changes)
	dirty     bool   // the instance changed since the container started

	running  atomic.Bool // container != "" (readable without mu, which a start holds for seconds)
	inflight atomic.Int64
	lastUsed atomic.Int64 // unix nanos
}

var (
	_ host.Runtime        = (*Runtime)(nil)
	_ host.Syncer         = (*Runtime)(nil)
	_ host.Statser        = (*Runtime)(nil)
	_ host.Shutdowner     = (*Runtime)(nil)
	_ host.RuntimeStarter = (*Runtime)(nil)
)

// New returns a runtime for cfg. Call Start before serving.
func New(cfg Config) (*Runtime, error) {
	if cfg.Image == "" || cfg.Binaries == nil || cfg.DataRoot == "" {
		return nil, errors.New("docker: Image, Binaries and DataRoot are required")
	}
	if cfg.Socket == "" {
		cfg.Socket = DefaultSocket()
	}
	if cfg.Home == "" {
		cfg.Home = "/home/pockethost"
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}
	if cfg.MaxConcurrentStarts <= 0 {
		cfg.MaxConcurrentStarts = 4
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 60 * time.Second
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 10 * time.Second
	}
	if cfg.HostIP == "" {
		cfg.HostIP = "127.0.0.1"
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	// its own transport: an edge's egress policy guards http.DefaultTransport and refuses loopback
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
	bg, cancel := context.WithCancel(context.Background())
	rt := &Runtime{cfg: cfg, e: newEngine(cfg.Socket), log: log, sem: make(chan struct{}, cfg.MaxConcurrentStarts),
		http: &http.Client{Transport: tr, Timeout: 3 * time.Second}, tr: tr, ctrs: map[string]*ctr{}, bg: bg, cancel: cancel}
	rt.watch = host.NewScriptWatcher(log, rt.scriptsChanged)
	return rt, nil
}

// Name implements [host.Runtime].
func (rt *Runtime) Name() string { return "docker" }

// Serves implements [host.Runtime].
func (rt *Runtime) Serves(inst host.Instance) bool {
	if rt.cfg.Serves != nil {
		return rt.cfg.Serves(inst)
	}
	return inst.Runtime == "" || inst.Runtime == "docker"
}

// Ready implements [host.Runtime]: true once Start has adopted the running containers.
func (rt *Runtime) Ready() bool { return rt.ready.Load() }

// Start implements [host.RuntimeStarter]: it makes sure the image is there, adopts running containers and starts the
// idle reaper.
func (rt *Runtime) Start(ctx context.Context) error {
	ok, err := rt.e.imageExists(ctx, rt.cfg.Image)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	if !ok {
		rt.log.Info("docker: pulling image", "image", rt.cfg.Image)
		if err := rt.e.pull(ctx, rt.cfg.Image); err != nil {
			return fmt.Errorf("docker: pull %s: %w", rt.cfg.Image, err)
		}
	}
	if err := rt.adopt(ctx); err != nil {
		return err
	}
	go rt.reap()
	rt.ready.Store(true)
	return nil
}

func (rt *Runtime) adopt(ctx context.Context) error {
	list, err := rt.e.list(ctx, map[string][]string{"status": {"running"}})
	if err != nil {
		return fmt.Errorf("docker: list containers: %w", err)
	}
	n := 0
	for _, s := range list {
		id, hash := "", ""
		switch {
		case s.Labels[LabelRuntime] == "docker" && s.Labels[LabelInstance] != "":
			id, hash = s.Labels[LabelInstance], s.Labels[LabelSpec]
		case rt.cfg.AdoptImage != "" && strings.HasPrefix(s.Image, rt.cfg.AdoptImage) && len(s.Names) > 0 && s.Labels[LabelRuntime] == "":
			id = strings.TrimPrefix(s.Names[0], "/")
		default:
			continue
		}
		if !host.ValidID(id) {
			continue
		}
		info, err := rt.e.inspect(ctx, s.ID)
		if err != nil || !info.State.Running || info.hostPort() == 0 {
			continue
		}
		c := rt.get(id)
		c.container, c.port, c.hash = s.ID, info.hostPort(), hash
		c.running.Store(true)
		c.lastUsed.Store(time.Now().UnixNano())
		go rt.follow(c.inst, s.ID, time.Now())
		rt.watchScripts(id, filepath.Join(rt.cfg.DataRoot, id))
		n++
	}
	if n > 0 {
		rt.log.Info("docker: adopted running containers", "count", n)
	}
	return nil
}

func (rt *Runtime) get(id string) *ctr {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	c, ok := rt.ctrs[id]
	if !ok {
		c = &ctr{inst: id}
		rt.ctrs[id] = c
	}
	return c
}

// ServeInstance implements [host.Runtime]: it starts the instance's container if needed and proxies to it.
func (rt *Runtime) ServeInstance(w http.ResponseWriter, r *http.Request, inst host.Instance) {
	c := rt.get(inst.ID)
	c.inflight.Add(1)
	c.lastUsed.Store(time.Now().UnixNano())
	defer func() {
		c.lastUsed.Store(time.Now().UnixNano())
		c.inflight.Add(-1)
	}()
	port, err := rt.ensure(r.Context(), c, inst)
	if err != nil {
		rt.log.Warn("docker: instance failed to start", "instance", inst.ID, "error", err)
		http.Error(w, "instance failed to start: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	target := "127.0.0.1:" + strconv.Itoa(port)
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", target
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		Transport:     rt.tr,
		FlushInterval: -1, // realtime (SSE) streams flush at once
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			var op *net.OpError
			if errors.As(err, &op) && op.Op == "dial" {
				rt.forget(c, port) // the container went away; the next request starts it again
			}
			rt.log.Debug("docker: proxy error", "instance", inst.ID, "error", err)
			http.Error(w, "instance unavailable", http.StatusBadGateway)
		},
	}
	p.ServeHTTP(w, r)
}

// forget drops a container that no longer answers on port.
func (rt *Runtime) forget(c *ctr, port int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port == port {
		c.container, c.port, c.hash = "", 0, ""
		c.running.Store(false)
	}
}

// ensure returns the port of the instance's running container, starting (or restarting a changed) one.
func (rt *Runtime) ensure(ctx context.Context, c *ctr, inst host.Instance) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.container != "" && !c.dirty {
		return c.port, nil
	}
	cfg, version, err := rt.containerConfig(ctx, inst)
	if err != nil {
		return 0, err
	}
	if c.container != "" { // dirty: restart only if what it would run changed
		if cfg.Labels[LabelSpec] == c.hash {
			c.dirty = false
			return c.port, nil
		}
		rt.log.Info("docker: instance changed, restarting", "instance", inst.ID)
		rt.stopLocked(ctx, c)
	}
	return rt.startLocked(ctx, c, inst, cfg, version)
}

// folder is an instance's folder on the host: Instance.Dir, or <DataRoot>/<id>.
func (rt *Runtime) folder(inst host.Instance) string {
	if inst.Dir != "" {
		return inst.Dir
	}
	return filepath.Join(rt.cfg.DataRoot, inst.ID)
}

// watchScripts makes sure the instance's pb_hooks exists and watches it; a change restarts the container.
// (pb_migrations isn't watched: stock binaries write a migration there on every dashboard schema change.)
func (rt *Runtime) watchScripts(id, dir string) {
	hooks := filepath.Join(dir, "pb_hooks")
	_ = os.MkdirAll(hooks, 0o755)
	rt.watch.Add(id, hooks)
}

// scriptsChanged restarts an instance's container after its pb_hooks changed. Requests for it wait for the new
// container (they queue on the instance's lock). A container adopted from an earlier process is stopped instead;
// its next request starts it with the current configuration.
func (rt *Runtime) scriptsChanged(id string) {
	rt.mu.Lock()
	c, ok := rt.ctrs[id]
	rt.mu.Unlock()
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.container == "" {
		return
	}
	rt.log.Info("docker: pb_hooks changed, restarting", "instance", id)
	ctx := rt.bg
	if c.last == nil {
		rt.stopLocked(ctx, c)
		return
	}
	inst := *c.last
	cfg, version, err := rt.containerConfig(ctx, inst)
	if err != nil {
		rt.log.Warn("docker: restart after pb_hooks change", "instance", id, "error", err)
		rt.stopLocked(ctx, c)
		return
	}
	rt.stopLocked(ctx, c)
	if _, err := rt.startLocked(ctx, c, inst, cfg, version); err != nil {
		rt.log.Warn("docker: restart after pb_hooks change", "instance", id, "error", err)
	}
}

// containerConfig is the container an instance should run, labelled with a hash of itself.
func (rt *Runtime) containerConfig(ctx context.Context, inst host.Instance) (containerConfig, string, error) {
	want := inst.Version
	if rt.cfg.Version != nil {
		want = rt.cfg.Version(inst)
	}
	if want == "" {
		want = rt.cfg.DefaultVersion
	}
	if want == "" {
		return containerConfig{}, "", errors.New("no PocketBase version")
	}
	line, ver, bin, err := rt.cfg.Binaries.Resolve(ctx, want)
	if err != nil {
		return containerConfig{}, "", err
	}
	version := line + ":" + ver // e.g. "pb:0.36.9", "p1:0.0.1"
	dir := rt.folder(inst)
	// create the instance folder here: otherwise the daemon creates a missing bind source itself (root-owned on
	// Linux; inside the VM with Docker Desktop, where the host never sees it)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return containerConfig{}, "", fmt.Errorf("instance folder: %w", err)
	}
	spec := Spec{Env: map[string]string{}, Labels: map[string]string{}}
	for k, v := range inst.Secrets {
		spec.Env[k] = v
	}
	if inst.EncryptionKey != "" {
		spec.Env["PB_ENCRYPTION_KEY"] = inst.EncryptionKey
	}
	if rt.cfg.Prepare != nil {
		if err := rt.cfg.Prepare(ctx, inst, version, &spec); err != nil {
			return containerConfig{}, "", fmt.Errorf("prepare: %w", err)
		}
	}
	// --hooksWatch=false: the runtime watches pb_hooks from the host (one watcher for all containers) and restarts the
	// container, so PocketBase's own watcher would only restart it a second time
	cmd := []string{"./pocketbase", "serve", "--http", "0.0.0.0:8090", "--hooksWatch=false"}
	if inst.Dev {
		cmd = append(cmd, "--dev")
	}
	if spec.Env["PB_ENCRYPTION_KEY"] != "" {
		cmd = append(cmd, "--encryptionEnv=PB_ENCRYPTION_KEY")
	}
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	binds := append([]string{dir + ":" + rt.cfg.Home, bin + ":" + rt.cfg.Home + "/pocketbase:ro"}, spec.Binds...)
	hc := hostConfig{
		Binds:        binds,
		PortBindings: map[string][]portBinding{"8090/tcp": {{HostIP: rt.cfg.HostIP, HostPort: ""}}},
		Init:         true,
		AutoRemove:   true,
		Memory:       rt.cfg.Memory,
		NanoCPUs:     rt.cfg.NanoCPUs,
		PidsLimit:    rt.cfg.PidsLimit,
		CapDrop:      rt.cfg.CapDrop,
		NetworkMode:  rt.cfg.Network,
	}
	if rt.cfg.NofileSoft > 0 || rt.cfg.NofileHard > 0 {
		hc.Ulimits = []ulimit{{Name: "nofile", Soft: rt.cfg.NofileSoft, Hard: max(rt.cfg.NofileHard, rt.cfg.NofileSoft)}}
	}
	if rt.cfg.NoNewPrivileges {
		hc.SecurityOpt = []string{"no-new-privileges"}
	}
	cfg := containerConfig{
		Image:        rt.cfg.Image,
		Cmd:          cmd,
		Env:          env,
		WorkingDir:   rt.cfg.Home,
		ExposedPorts: map[string]struct{}{"8090/tcp": {}},
		HostConfig:   hc,
	}
	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[LabelRuntime], labels[LabelInstance], labels[LabelVersion] = "docker", inst.ID, version
	cfg.Labels = labels
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	cfg.Labels[LabelSpec] = hex.EncodeToString(sum[:8])
	return cfg, version, nil
}

func (rt *Runtime) startLocked(ctx context.Context, c *ctr, inst host.Instance, cfg containerConfig, version string) (int, error) {
	select {
	case rt.sem <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-rt.sem }()

	t0 := time.Now()
	name := inst.ID
	_ = rt.e.remove(ctx, name) // a stale container of the same name
	id, err := rt.e.create(ctx, name, cfg)
	if isConflict(err) {
		time.Sleep(500 * time.Millisecond) // its removal is still in progress
		id, err = rt.e.create(ctx, name, cfg)
	}
	if err != nil {
		return 0, fmt.Errorf("create: %w", err)
	}
	since := time.Now()
	if err := rt.e.start(ctx, id); err != nil {
		_ = rt.e.remove(context.Background(), id)
		return 0, fmt.Errorf("start: %w", err)
	}
	go rt.follow(inst.ID, id, since.Add(-time.Second))
	info, err := rt.e.inspect(ctx, id)
	if err != nil || info.hostPort() == 0 {
		_ = rt.e.remove(context.Background(), id)
		return 0, fmt.Errorf("no published port (running %v, ports %v, err %v)", info.State.Running, info.NetworkSettings.Ports, err)
	}
	port := info.hostPort()
	if err := rt.waitHealthy(ctx, id, port); err != nil {
		_ = rt.e.remove(context.Background(), id)
		return 0, err
	}
	c.container, c.port, c.hash, c.dirty = id, port, cfg.Labels[LabelSpec], false
	c.running.Store(true)
	started := inst
	c.last = &started
	rt.watchScripts(inst.ID, rt.folder(inst))
	rt.log.Info("docker: instance started", "instance", inst.ID, "version", version, "ms", time.Since(t0).Milliseconds())
	return port, nil
}

// waitHealthy polls /api/health until it answers 200, the container exits or StartTimeout passes.
func (rt *Runtime) waitHealthy(ctx context.Context, id string, port int) error {
	deadline := time.Now().Add(rt.cfg.StartTimeout)
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/health"
	for i := 0; ; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if res, err := rt.http.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		if i%10 == 9 { // every second: still running?
			if info, err := rt.e.inspect(ctx, id); err != nil || !info.State.Running {
				return errors.New("container exited during startup (see the instance log)")
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy within %s", rt.cfg.StartTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// follow sends a container's output to OnLog until it stops.
func (rt *Runtime) follow(instID, containerID string, since time.Time) {
	_ = rt.e.logs(rt.bg, containerID, func(stream int, text string) {
		if rt.cfg.OnLog != nil {
			rt.cfg.OnLog(instID, stream == 2, text)
			return
		}
		rt.log.Debug("docker: instance output", "instance", instID, "stderr", stream == 2, "line", text)
	})
}

func (rt *Runtime) stopLocked(ctx context.Context, c *ctr) {
	if c.container == "" {
		return
	}
	if err := rt.e.stop(ctx, c.container, rt.cfg.StopGrace); err != nil {
		rt.log.Warn("docker: stop", "instance", c.inst, "error", err)
	}
	_ = rt.e.remove(ctx, c.container)
	c.container, c.port, c.hash, c.dirty = "", 0, "", false
	c.running.Store(false)
	rt.watch.Remove(c.inst)
}

// Stop stops an instance's container (its folder stays), e.g. before maintenance on its files.
func (rt *Runtime) Stop(ctx context.Context, id string) {
	c := rt.get(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	rt.stopLocked(ctx, c)
}

// Running reports whether an instance's container is running.
func (rt *Runtime) Running(id string) bool {
	rt.mu.Lock()
	c, ok := rt.ctrs[id]
	rt.mu.Unlock()
	return ok && c.running.Load()
}

// Sync implements [host.Syncer]: deleted instances (or ones moved to another edge) stop; changed ones restart on
// their next request if what they run changed.
func (rt *Runtime) Sync(ch host.Change, prev *host.Instance) {
	rt.mu.Lock()
	c, ok := rt.ctrs[ch.Instance.ID]
	rt.mu.Unlock()
	if !ok {
		return
	}
	if ch.Kind == host.Delete || ch.Instance.Remote != "" || (ch.Instance.Runtime != "" && ch.Instance.Runtime != "docker") {
		go rt.Stop(context.Background(), ch.Instance.ID)
		return
	}
	if prev == nil || !reflect.DeepEqual(*prev, ch.Instance) {
		c.mu.Lock()
		c.dirty = true
		if c.hash == "" { // adopted from another manager: we can't tell what changed, so restart it
			c.hash = "unknown"
		}
		c.mu.Unlock()
	}
}

// reap stops containers idle for longer than IdleTTL.
func (rt *Runtime) reap() {
	every := rt.cfg.IdleTTL / 4
	every = min(max(every, time.Second), time.Minute)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-rt.bg.Done():
			return
		case <-t.C:
		}
		rt.mu.Lock()
		list := make([]*ctr, 0, len(rt.ctrs))
		for _, c := range rt.ctrs {
			list = append(list, c)
		}
		rt.mu.Unlock()
		for _, c := range list {
			if !c.running.Load() || c.inflight.Load() > 0 || time.Since(time.Unix(0, c.lastUsed.Load())) < rt.cfg.IdleTTL {
				continue
			}
			c.mu.Lock()
			if c.running.Load() && c.inflight.Load() == 0 && time.Since(time.Unix(0, c.lastUsed.Load())) >= rt.cfg.IdleTTL {
				rt.log.Info("docker: instance idle, stopping", "instance", c.inst)
				rt.stopLocked(rt.bg, c)
			}
			c.mu.Unlock()
		}
	}
}

// RunningCount implements [host.Statser].
func (rt *Runtime) RunningCount() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, c := range rt.ctrs {
		if c.running.Load() {
			n++
		}
	}
	return n
}

// Shutdown implements [host.Shutdowner]: background work stops; containers keep running for the next edge process
// to adopt.
func (rt *Runtime) Shutdown() {
	rt.cancel()
	rt.watch.Close()
}
