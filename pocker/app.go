package pocker

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations" // registers PocketBase's system migrations, as pocketbase.go does
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/ui"
)

// runningApp is a bootstrapped PocketBase app and the HTTP handler that serves it.
type runningApp struct {
	app     *core.BaseApp
	handler http.Handler
	started time.Time
}

// appOptions are the process-wide settings applied to every app.
type appOptions struct {
	PBRoot        string
	HooksPoolSize int
	ForceDev      bool // Pocker dev mode: every app runs in PocketBase dev mode
	// MaxJSExecution interrupts a JS call that runs longer (0 = jsvm default, 30s).
	MaxJSExecution time.Duration
}

func instanceDir(opts appOptions, inst Instance) string {
	if inst.Dir != "" {
		return inst.Dir
	}
	return filepath.Join(opts.PBRoot, inst.ID)
}

// startApp creates, bootstraps and migrates the instance's PocketBase app and builds its router, the same way
// `pocketbase serve` does, but without opening a listener: Pocker's own server dispatches to the returned handler.
func startApp(opts appOptions, inst Instance) (*runningApp, error) {
	dir, err := filepath.Abs(instanceDir(opts, inst))
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"pb_data", "pb_hooks", "pb_migrations", "pb_public"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	app := core.NewBaseApp(core.BaseAppConfig{
		DBConnect:     guardedDBConnect(), // SQL stays inside this app's files
		DataDir:       filepath.Join(dir, "pb_data"),
		IsDev:         inst.Dev || opts.ForceDev,
		EncryptionKey: inst.Env["PB_ENCRYPTION_KEY"],
		AppMigrations: &core.MigrationsList{}, // this app's JS migrations only
	})

	env := make(map[string]string, len(inst.Env))
	for k, v := range inst.Env {
		env[k] = v
	}

	err = jsvm.Register(app, jsvm.Config{
		HooksDir:      filepath.Join(dir, "pb_hooks"),
		MigrationsDir: filepath.Join(dir, "pb_migrations"),
		HooksWatch:    false, // one process serves every app: a hooks change must not restart it
		HooksPoolSize: opts.HooksPoolSize,
		Sandbox: &jsvm.Sandbox{
			Dir:          dir,
			Env:          env,
			MaxExecution: opts.MaxJSExecution,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("load hooks: %w", err)
	}

	if err := app.Bootstrap(); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}

	handler, err := buildHandler(app, filepath.Join(dir, "pb_public"))
	if err != nil {
		return nil, err
	}
	return &runningApp{app: app, handler: handler, started: time.Now()}, nil
}

// buildHandler mirrors apis.Serve: router, CORS, dashboard, OnServe hooks (customer routes, pb_public) and the
// final mux. The OnServe event gets a listener that is never served, so no port is opened per app.
func buildHandler(app *core.BaseApp, publicDir string) (http.Handler, error) {
	router, err := apis.NewRouter(app)
	if err != nil {
		return nil, err
	}

	router.Bind(apis.CORS(apis.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete},
	}))

	if ui.DistDirFS != nil {
		router.GET("/_/{path...}", apis.Static(ui.DistDirFS, false)).Bind(apis.Gzip())
	}

	server := &http.Server{}
	event := &core.ServeEvent{
		App:      app,
		Router:   router,
		Server:   server,
		Listener: idleListener{},
	}

	var handler http.Handler
	err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error {
		// like `pocketbase serve`: pb_public is served unless a hook already took the catch-all route
		if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
			e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), true))
		}
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		handler = mux
		return nil
	})
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("OnServe did not complete; a hook may not have called e.next()")
	}
	return handler, nil
}

// stopApp runs the app's terminate hooks and closes its databases.
func stopApp(r *runningApp) {
	_ = r.app.OnTerminate().Trigger(&core.TerminateEvent{App: r.app}, func(e *core.TerminateEvent) error {
		return e.App.ResetBootstrapState()
	})
}

// idleListener satisfies core.ServeEvent.Listener; Pocker never accepts on it.
type idleListener struct{}

func (idleListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (idleListener) Close() error              { return nil }
func (idleListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
