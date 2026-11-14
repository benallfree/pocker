package pocker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/pockethost/pocketone/ext/host"
	"github.com/pockethost/pocketone/ext/sandbox"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/ceplugin"
)

// Server wires a [Config], an [InstanceSource] and a [Manager] into one HTTP server.
type Server struct {
	Config  Config
	Source  InstanceSource // nil = YAMLSource{Path: Config.InstancesFile}
	Logger  *slog.Logger
	Manager *Manager // set by Run

	// OnEvent, if set, receives instance events (started, stopped, restarts). An instance source that
	// implements [EventSink] receives them too.
	OnEvent func(Event)

	// SetupApp, if set, runs for each app after its migrations and before it serves: the place for a host
	// application's Go code (routes, settings) that uses Instance.Private instead of env.
	SetupApp func(inst Instance, app core.App) error

	// Wrap, if set, wraps the instance handler (e.g. firewall or rate-limit middleware from a host application).
	Wrap func(http.Handler) http.Handler

	// Forward, if set, serves requests for instances another server runs (see [ManagerOptions.Forward]).
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)

	// Gates every request passes first, in order (e.g. ext/firewall: allowed peers, client IPs, rate limits).
	Gates []host.Gate

	// Background work run until shutdown (e.g. an edge provider's refresh).
	Background []func(ctx context.Context)

	// Plugins extend the server (see [Plugin]): their own settings, a request gate, TLS (e.g. the autotls plugin), handler wrappers and background work. Order matters for wrappers: the
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
	h := &host.Host{
		Source: source, First: first, Forward: s.Forward, Dev: s.Config.Dev, Logger: log,
		Listeners: host.Listeners{Listen: s.Config.Listen, PlainListen: s.Config.PlainListen, TLS: s.Config.TLS, ShutdownTimeout: s.Config.ShutdownTimeout},
	}
	h.Gates = append(h.Gates, s.Gates...)
	if err := s.prepare(h); err != nil {
		return err
	}
	return h.Run(ctx)
}

// prepare makes h run its instances in-process: plugins, the egress policy, the Manager as h's runtime, wrappers,
// background work (plugin starters). h's Source, Gates, Forward, Dev, Listeners and Logger are
// already set; plugin gates are added after h's.
func (s *Server) prepare(h *host.Host) error {
	log := h.Logger
	if log == nil {
		log = slog.Default()
		h.Logger = log
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
	sandbox.InstallEgressPolicy(sandbox.EgressPolicy{Allow: allow})
	pool := 2
	if s.Config.HooksPoolSize != nil {
		pool = *s.Config.HooksPoolSize
	}
	var (
		tlsProv   TLSProvider
		wrappers  []Wrapper
		starters  []Starter
		initers   []AppIniter
		jsIniters []JSIniter
	)
	for _, p := range s.Plugins {
		if c, ok := p.(Configurer); ok {
			if err := c.Configure(s.Config); err != nil {
				return fmt.Errorf("plugin %s: %w", p.Name(), err)
			}
		}
		if cp, ok := p.(ceplugin.Plugin); ok {
			// a PocketOne plugin: register it on every instance, with that instance's settings
			if _, own := p.(AppIniter); !own {
				hp := hostCEPlugin(cp)
				initers = append(initers, hp)
				jsIniters = append(jsIniters, hp)
			}
		}
		if g, ok := p.(Gate); ok {
			h.Gates = append(h.Gates, g)
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
	if h.Directory == nil {
		h.Directory = host.NewDirectory(log)
	}
	s.Manager = NewManager(h.Source, ManagerOptions{
		PBRoot:        s.Config.PBRoot,
		Dev:           s.Config.Dev,
		HooksPoolSize: pool,
		Logger:        log,
		Defaults:      s.Config.Limits,
		Gate:          host.ChainGates(h.Gates...),
		OnEvent:       s.onEvent(h.Source, log),
		Init:          chainIniters(initers),
		InitJS:        chainJSIniters(jsIniters),
		Setup:         s.SetupApp,
		Forward:       h.Forward,
		Directory:     h.Directory,
	})
	if s.OnStart != nil {
		s.OnStart(s.Manager)
	}
	h.Runtime = s.Manager
	if tlsProv != nil {
		h.TLS = tlsProv
	}

	var wrap []func(http.Handler) http.Handler // outermost first: plugin wrappers, then the host application's
	for _, wr := range wrappers {
		wrap = append(wrap, wr.Wrap)
	}
	if s.Wrap != nil {
		wrap = append(wrap, s.Wrap)
	}
	h.Wrappers = append(wrap, h.Wrappers...)
	h.Background = append(h.Background, s.Background...)
	for _, st := range starters {
		h.Background = append(h.Background, func(ctx context.Context) { st.Start(ctx, s.Manager) })
	}
	h.OnListen = func(addr string) {
		log.Info("pocker: listening", "addr", addr, "pb_root", s.Config.PBRoot, "dev", s.Config.Dev)
	}
	return nil
}

// EventSink is implemented by instance sources that want instance events (e.g. to notify the owner).
type EventSink interface {
	InstanceEvent(Event)
}

func (s *Server) onEvent(source InstanceSource, log *slog.Logger) func(Event) {
	sink, _ := source.(EventSink)
	return func(e Event) {
		log.Info("pocker: event", "kind", e.Kind, "id", e.InstanceID, "reason", e.Reason)
		if s.OnEvent != nil {
			s.OnEvent(e)
		}
		if sink != nil {
			sink.InstanceEvent(e)
		}
	}
}
