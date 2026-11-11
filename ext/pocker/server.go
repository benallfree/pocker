package pocker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

// Server wires a [Config], an [InstanceSource] and a [Manager] into one HTTP server.
type Server struct {
	Config  Config
	Source  InstanceSource // nil = YAMLSource{Path: Config.InstancesFile}
	Logger  *slog.Logger
	Manager *Manager // set by Run

	// OnEvent, if set, receives instance events (memory strikes, pauses, restarts). An instance source that
	// implements [EventSink] receives them too.
	OnEvent func(Event)

	// SetupApp, if set, runs for each app after its migrations and before it serves: the place for a host
	// application's Go code (routes, settings) that uses Instance.Private instead of env.
	SetupApp func(inst Instance, app core.App) error

	// Wrap, if set, wraps the instance handler (e.g. firewall or rate-limit middleware from a host application).
	Wrap func(http.Handler) http.Handler

	// Forward, if set, serves requests for instances another server runs (see [ManagerOptions.Forward]).
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)

	// Plugins extend the server (see [Plugin]): settings from the config file, a request gate (e.g. the firewall
	// plugin), TLS (e.g. the autotls plugin), handler wrappers and background work. Order matters for wrappers: the
	// first listed is the outermost.
	Plugins []Plugin

	// OnStart, if set, receives the Manager once Run has created it, before any request is served: the safe way for
	// host code running in other goroutines (scheduled jobs, maintenance) to get it.
	OnStart func(m *Manager)
}

// Run serves until ctx is done, then shuts down in-flight requests and running apps.
func (s *Server) Run(ctx context.Context) error {
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	source := s.Source
	if source == nil {
		// production needs the host -> id map; dev mode works with or without it
		source = &YAMLSource{Path: s.Config.InstancesFile, Required: !s.Config.Dev}
	}
	first, err := source.Snapshot(ctx)
	if err != nil {
		return err // fail fast, e.g. missing instances file in production
	}
	var allow []netip.Prefix
	for _, c := range s.Config.EgressAllow {
		pfx, err := netip.ParsePrefix(c)
		if err != nil {
			a, aerr := netip.ParseAddr(c)
			if aerr != nil {
				return fmt.Errorf("egress_allow: %q is not an IP or CIDR", c)
			}
			pfx = netip.PrefixFrom(a, a.BitLen())
		}
		allow = append(allow, pfx)
	}
	InstallEgressPolicy(EgressPolicy{Allow: allow}, int(s.Config.HTTPSendMaxTimeout/time.Second), int64(s.Config.HTTPSendMaxBody))
	pool := 2
	if s.Config.HooksPoolSize != nil {
		pool = *s.Config.HooksPoolSize
	}
	var (
		gates     []Gate
		tlsProv   TLSProvider
		wrappers  []Wrapper
		starters  []Starter
		initers   []AppIniter
		jsIniters []JSIniter
	)
	for _, p := range s.Plugins {
		if c, ok := p.(Configurer); ok {
			if err := c.Configure(s.Config.Doc(), s.Config); err != nil {
				return fmt.Errorf("plugin %s: %w", p.Name(), err)
			}
		}
		if cp, ok := p.(ceplugin.Plugin); ok {
			// a PocketBase CE plugin: register it on every instance, with that instance's settings
			if _, own := p.(AppIniter); !own {
				h, err := hostCEPlugin(cp, s.Config.Doc())
				if err != nil {
					return fmt.Errorf("plugin %s: %w", p.Name(), err)
				}
				initers = append(initers, h)
				jsIniters = append(jsIniters, h)
			}
		}
		if g, ok := p.(Gate); ok {
			gates = append(gates, g)
		}
		if t, ok := p.(TLSProvider); ok {
			if tlsProv != nil {
				return fmt.Errorf("plugin %s: another plugin already provides TLS", p.Name())
			}
			tlsProv = t
		}
		if wr, ok := p.(Wrapper); ok {
			wrappers = append(wrappers, wr)
		}
		if st, ok := p.(Starter); ok {
			starters = append(starters, st)
		}
		if in, ok := p.(AppIniter); ok {
			initers = append(initers, in)
		}
		if js, ok := p.(JSIniter); ok {
			jsIniters = append(jsIniters, js)
		}
	}
	s.Manager = NewManager(&primedSource{InstanceSource: source, first: first}, ManagerOptions{
		PBRoot:        s.Config.PBRoot,
		Dev:           s.Config.Dev,
		HooksPoolSize: pool,
		Logger:        log,
		Defaults:      s.Config.Limits,
		Gate:          chainGates(gates),
		OnEvent:       s.onEvent(source, log),
		Init:          chainIniters(initers),
		InitJS:        chainJSIniters(jsIniters),
		Setup:         s.SetupApp,
		Forward:       s.Forward,
	})

	if s.OnStart != nil {
		s.OnStart(s.Manager)
	}

	var handler http.Handler = s.Manager
	if s.Wrap != nil {
		handler = s.Wrap(handler)
	}
	for i := len(wrappers) - 1; i >= 0; i-- {
		handler = wrappers[i].Wrap(handler)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	managerErr := make(chan error, 1)
	go func() { managerErr <- s.Manager.Run(runCtx) }()
	for _, st := range starters {
		go st.Start(runCtx, s.Manager)
	}

	if s.Config.MemoryLimit > 0 {
		debug.SetMemoryLimit(s.Config.MemoryLimit)
	}
	soft := s.Config.MemorySoftLimit
	if soft == 0 {
		soft = 1 << 30
		if s.Config.MemoryLimit > 0 {
			soft = s.Config.MemoryLimit * 7 / 10
		}
	}
	if soft > 0 {
		watch := &MemoryWatch{SoftLimit: uint64(soft), OnStrike: s.Manager.Strike, Logger: log}
		go watch.Run(runCtx)
	}

	ln, err := net.Listen("tcp", s.Config.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Minute,
		// large uploads/downloads, like `pocketbase serve`
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		BaseContext:  func(net.Listener) context.Context { return runCtx },
	}
	serveErr := make(chan error, 1+len(s.Config.PlainListen))
	var redirect *http.Server
	startRedirect := func(h http.Handler) {
		if s.Config.TLS.HTTPListen == "" {
			return
		}
		redirect = &http.Server{Addr: s.Config.TLS.HTTPListen, Handler: h, ReadHeaderTimeout: time.Minute}
		go func() {
			if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("pocker: http listener", "error", err)
			}
		}()
	}
	var (
		tlsCfg   *tls.Config
		wrapHTTP func(http.Handler) http.Handler
	)
	if tlsProv != nil {
		tlsCfg, wrapHTTP, err = tlsProv.TLS(runCtx, func(host string) bool {
			_, ok := s.Manager.Lookup(host)
			return ok
		})
		if err != nil {
			_ = ln.Close()
			return err
		}
	}
	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg
		go func() { serveErr <- srv.ServeTLS(ln, "", "") }()
		h := httpsRedirect(handler)
		if wrapHTTP != nil {
			h = wrapHTTP(h)
		}
		startRedirect(h)
	} else if s.Config.TLS.Cert != "" || s.Config.TLS.Key != "" {
		certs, err := newCertReloader(s.Config.TLS.Cert, s.Config.TLS.Key)
		if err != nil {
			_ = ln.Close()
			return err
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.GetCertificate}
		go func() { serveErr <- srv.ServeTLS(ln, "", "") }()
		startRedirect(httpsRedirect(handler))
	} else {
		go func() { serveErr <- srv.Serve(ln) }()
	}
	log.Info("pocker: listening", "addr", ln.Addr().String(), "pb_root", s.Config.PBRoot, "dev", s.Config.Dev)
	if s.Config.Dev {
		// instances start on their first request; start the dev mail trap now so its notice shows at startup
		_, _ = mailer.DevTrap("Dev mode: instances without SMTP")
	}
	var plain []*http.Server
	for _, addr := range s.Config.PlainListen {
		pln, err := net.Listen("tcp", addr)
		if err != nil {
			_ = srv.Close()
			for _, p := range plain {
				_ = p.Close()
			}
			s.Manager.Shutdown()
			return fmt.Errorf("plain_listen %s: %w", addr, err)
		}
		p := &http.Server{
			Handler: handler, ReadHeaderTimeout: time.Minute, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute,
			BaseContext: func(net.Listener) context.Context { return runCtx },
		}
		plain = append(plain, p)
		go func() {
			if err := p.Serve(pln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
			}
		}()
		log.Info("pocker: listening (plain HTTP)", "addr", pln.Addr().String())
	}

	select {
	case <-ctx.Done():
	case err := <-managerErr:
		if err != nil {
			log.Error("pocker: instance source stopped", "error", err)
		}
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
			s.Manager.Shutdown()
			return err
		}
	}

	timeout := s.Config.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), timeout)
	defer done()
	_ = srv.Shutdown(shutdownCtx)
	if redirect != nil {
		_ = redirect.Shutdown(shutdownCtx)
	}
	for _, p := range plain {
		_ = p.Shutdown(shutdownCtx)
	}
	cancel() // ends long-lived requests such as realtime SSE
	s.Manager.Shutdown()
	return nil
}

// EventSink is implemented by instance sources that want instance events (e.g. to notify the owner).
type EventSink interface {
	InstanceEvent(Event)
}

func (s *Server) onEvent(source InstanceSource, log *slog.Logger) func(Event) {
	sink, _ := source.(EventSink)
	return func(e Event) {
		log.Info("pocker: event", "kind", e.Kind, "id", e.InstanceID, "reason", e.Reason, "until", e.Until, "strikes", e.Strikes)
		if s.OnEvent != nil {
			s.OnEvent(e)
		}
		if sink != nil {
			sink.InstanceEvent(e)
		}
	}
}

// chainGates combines gates: a peer must pass every gate, and a request is admitted only when all admit it.
func chainGates(gates []Gate) Gate {
	switch len(gates) {
	case 0:
		return nil
	case 1:
		return gates[0]
	}
	return gateChain(gates)
}

type gateChain []Gate

func (c gateChain) AllowPeer(r *http.Request) bool {
	for _, g := range c {
		if !g.AllowPeer(r) {
			return false
		}
	}
	return true
}

func (c gateChain) Admit(w http.ResponseWriter, r *http.Request, inst Instance) (func(), bool) {
	var releases []func()
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, g := range c {
		rel, ok := g.Admit(w, r, inst)
		if !ok {
			releaseAll()
			return nil, false
		}
		releases = append(releases, rel)
	}
	return releaseAll, true
}

// primedSource hands the Manager the snapshot Run already loaded (to fail fast) instead of loading it a second time,
// which for a remote source like a control-plane mirror is a second full download before the edge is ready. Later
// Snapshot calls, if any, go to the source.
type primedSource struct {
	InstanceSource
	mu    sync.Mutex
	first []Instance
	used  bool
}

func (p *primedSource) Snapshot(ctx context.Context) ([]Instance, error) {
	p.mu.Lock()
	if !p.used {
		p.used = true
		list := p.first
		p.first = nil
		p.mu.Unlock()
		return list, nil
	}
	p.mu.Unlock()
	return p.InstanceSource.Snapshot(ctx)
}
