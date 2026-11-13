package host

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// Host serves many instances with one runtime: it follows the instance source into a directory, runs the request
// front (health, peer check, resolve, blocked, then the forward hook or the runtime, with the gates) behind the
// wrappers, and listens. Fill it in and call Run.
type Host struct {
	Source  InstanceSource // required
	Runtime Runtime        // the provider's runtime (required)

	// Directory is shared with the runtime when the runtime needs it at construction (e.g. pocker's Manager); nil =
	// a new one.
	Directory *Directory
	// First, if set, is a snapshot already taken from Source (so it isn't loaded twice).
	First []Instance

	Gates    []Gate                                                      // in order; a ConfigurableGate is configured by its owner
	Wrappers []func(http.Handler) http.Handler                           // the first is the outermost
	TLS      TLSProvider                                                 // nil = Listeners.TLS cert files, or plain HTTP
	Forward  func(w http.ResponseWriter, r *http.Request, inst Instance) // instances another server runs (ext/edge); nil = plain proxy to Remote
	Dev      bool                                                        // also serve unknown hostnames (dev ids)

	Listeners  Listeners
	HealthPath string // default [DefaultHealthPath]

	// Background work run until shutdown (e.g. the edge provider's refresh, plugin jobs).
	Background []func(ctx context.Context)
	// OnListen is called for each address once it accepts connections.
	OnListen func(addr string)
	// OnShutdown runs after the listeners closed (in-flight requests done or cut), e.g. to stop running apps.
	OnShutdown func()

	Logger *slog.Logger

	handler *Handler
}

// Handler returns the request front (before the wrappers), built on first use.
func (h *Host) Handler() *Handler {
	if h.handler == nil {
		h.handler = &Handler{Dir: h.dir(), Gate: ChainGates(h.Gates...), Runtimes: []Runtime{h.Runtime}, Forward: h.Forward,
			Dev: h.Dev, HealthPath: h.HealthPath, Logger: h.log()}
	}
	return h.handler
}

func (h *Host) dir() *Directory {
	if h.Directory == nil {
		h.Directory = NewDirectory(h.log())
	}
	return h.Directory
}

func (h *Host) log() *slog.Logger {
	if h.Logger == nil {
		h.Logger = slog.Default()
	}
	return h.Logger
}

// Run serves until ctx is done or the source fails, then shuts down: the listeners (in-flight requests get
// ShutdownTimeout), background work, the runtime.
func (h *Host) Run(ctx context.Context) error {
	if h.Source == nil || h.Runtime == nil {
		return errors.New("host: Source and Runtime are required")
	}
	log := h.log()
	dir := h.dir()
	if s, ok := h.Runtime.(Syncer); ok {
		dir.Subscribe(s.Sync)
	}
	var handler http.Handler = h.Handler()
	for i := len(h.Wrappers) - 1; i >= 0; i-- {
		handler = h.Wrappers[i](handler)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sourceErr := make(chan error, 1)
	go func() { sourceErr <- dir.Run(runCtx, h.Source, h.First) }()
	for _, bg := range h.Background {
		go bg(runCtx)
	}

	serveCtx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	go func() {
		select {
		case <-serveCtx.Done():
		case err := <-sourceErr:
			if err != nil {
				log.Error("host: instance source stopped", "error", err)
			}
			stopServe()
		}
	}()
	served := func(host string) bool {
		_, ok := dir.Lookup(host)
		return ok
	}
	onListen := h.OnListen
	if onListen == nil {
		onListen = func(addr string) { log.Info("host: listening", "addr", addr) }
	}
	err := Serve(serveCtx, runCtx, h.Listeners, handler, h.TLS, served, log, onListen)
	cancel() // ends long-lived requests such as realtime SSE, and background work
	if h.OnShutdown != nil {
		h.OnShutdown()
	}
	if s, ok := h.Runtime.(Shutdowner); ok {
		s.Shutdown()
	}
	return err
}
