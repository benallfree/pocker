package pocker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Server wires a [Config], an [InstanceSource] and a [Manager] into one HTTP server.
type Server struct {
	Config  Config
	Source  InstanceSource // nil = YAMLSource{Path: Config.InstancesFile}
	Logger  *slog.Logger
	Manager *Manager // set by Run

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
	if s.Config.MaxQueryDuration > 0 {
		maxQueryDuration = s.Config.MaxQueryDuration
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
	InstallEgressPolicy(EgressPolicy{Allow: allow}, int(s.Config.HTTPSendMaxTimeout/time.Second), s.Config.HTTPSendMaxBody)
	pool := 2
	if s.Config.HooksPoolSize != nil {
		pool = *s.Config.HooksPoolSize
	}
	s.Manager = NewManager(source, ManagerOptions{
		PBRoot:        s.Config.PBRoot,
		Dev:           s.Config.Dev,
		HooksPoolSize: pool,
		Logger:        log,
	})

	var handler http.Handler = s.Manager
	if s.Wrap != nil {
		handler = s.Wrap(handler)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	managerErr := make(chan error, 1)
	go func() { managerErr <- s.Manager.Run(runCtx) }()

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
	go func() { serveErr <- srv.Serve(ln) }()
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
	cancel() // ends long-lived requests such as realtime SSE
	s.Manager.Shutdown()
	return nil
}
