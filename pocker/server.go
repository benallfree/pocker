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
	"path/filepath"
	"runtime/debug"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
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
	if _, err := source.Snapshot(ctx); err != nil {
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
	defaults := s.Config.Limits
	if s.Config.Dev && defaults.RateLimits == nil {
		defaults.RateLimits = &RateLimits{Off: true} // local development: no request budgets unless configured
	}
	fw, err := NewFirewall(s.Config.Firewall)
	if err != nil {
		return err
	}
	s.Manager = NewManager(source, ManagerOptions{
		PBRoot:        s.Config.PBRoot,
		Dev:           s.Config.Dev,
		HooksPoolSize: pool,
		Logger:        log,
		Defaults:      defaults,
		Firewall:      fw,
		OnEvent:       s.onEvent(source, log),
		Setup:         s.SetupApp,
	})

	var handler http.Handler = s.Manager
	if s.Wrap != nil {
		handler = s.Wrap(handler)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	managerErr := make(chan error, 1)
	go func() { managerErr <- s.Manager.Run(runCtx) }()

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
	serveErr := make(chan error, 1)
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
	if s.Config.TLS.Auto {
		auto, err := newAutoTLS(runCtx, s.Config.TLS, filepath.Join(s.Config.PBRoot, ".pocker-certs"), func(host string) bool {
			_, ok := s.Manager.Lookup(host)
			return ok
		})
		if err != nil {
			_ = ln.Close()
			return err
		}
		srv.TLSConfig = auto.tlsConfig()
		go func() { serveErr <- srv.ServeTLS(ln, "", "") }()
		startRedirect(auto.httpHandler(httpsRedirect(handler)))
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
