package pocker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/debug"
	"time"

	"github.com/pockethost/pocketbase/ext/host"
	"github.com/pockethost/pocketbase/ext/sandbox"

	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
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

	// Forward, if set, serves requests for instances another server runs (see [ManagerOptions.Forward]).
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)

	// Firewall is the first gate every request passes (e.g. ext/firewall: allowed peers, client IPs, rate limits). It is
	// configured from the config file. When nil, every gate registered with the host whose section the config file
	// has is turned on (linking ext/firewall registers it for `firewall:` / `rate_limits:`).
	Firewall host.ConfigurableGate

	// Background work run until shutdown (e.g. an edge provider's refresh).
	Background []func(ctx context.Context)

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
	sandbox.InstallEgressPolicy(sandbox.EgressPolicy{Allow: allow}, int(s.Config.HTTPSendMaxTimeout/time.Second), int64(s.Config.HTTPSendMaxBody))
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
	fws := host.ConfiguredGates(s.Config.Doc())
	if s.Firewall != nil {
		fws = []host.ConfigurableGate{s.Firewall}
	}
	for _, fw := range fws {
		if err := fw.Configure(s.Config.Doc(), s.Config.Dev); err != nil {
			return err
		}
		gates = append(gates, fw)
	}
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
	dir := host.NewDirectory(log)
	s.Manager = NewManager(source, ManagerOptions{
		PBRoot:        s.Config.PBRoot,
		Dev:           s.Config.Dev,
		HooksPoolSize: pool,
		Logger:        log,
		Defaults:      s.Config.Limits,
		Gate:          host.ChainGates(gates...),
		OnEvent:       s.onEvent(source, log),
		Init:          chainIniters(initers),
		InitJS:        chainJSIniters(jsIniters),
		Setup:         s.SetupApp,
		Forward:       s.Forward,
		Directory:     dir,
	})
	if s.OnStart != nil {
		s.OnStart(s.Manager)
	}

	var wrap []func(http.Handler) http.Handler // outermost first: plugin wrappers, then the host application's
	for _, wr := range wrappers {
		wrap = append(wrap, wr.Wrap)
	}
	if s.Wrap != nil {
		wrap = append(wrap, s.Wrap)
	}
	background := append([]func(context.Context){}, s.Background...)
	for _, st := range starters {
		background = append(background, func(ctx context.Context) { st.Start(ctx, s.Manager) })
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
		watch := &sandbox.MemoryWatch{SoftLimit: uint64(soft), OnStrike: s.Manager.Strike, Logger: log}
		background = append(background, func(ctx context.Context) { watch.Run(ctx) })
	}

	h := &host.Host{
		Source: source, Runtime: s.Manager, Directory: dir, First: first,
		Gates: gates, Wrappers: wrap, TLS: tlsProv, Forward: s.Forward, Dev: s.Config.Dev,
		Listeners:  host.Listeners{Listen: s.Config.Listen, PlainListen: s.Config.PlainListen, TLS: s.Config.TLS, ShutdownTimeout: s.Config.ShutdownTimeout},
		Background: background, Logger: log,
		OnListen: func(addr string) {
			log.Info("pocker: listening", "addr", addr, "pb_root", s.Config.PBRoot, "dev", s.Config.Dev)
		},
	}
	return h.Run(ctx)
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
