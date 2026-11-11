package pocker

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations" // registers PocketBase's system migrations, as pocketbase.go does
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/ui"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/sandbox"
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
	// Defaults are the process-wide limits; an instance's own Limits override them.
	Defaults Limits
	// Init, if set, runs for each app right after it is created, before its hooks load and before it bootstraps
	// (opens its databases): the place to bind OnBootstrap/OnTerminate handlers.
	Init func(inst Instance, app core.App) error
	// InitJS, if set, adds globals to each JS runtime after the sandbox set it up.
	InitJS func(inst Instance, app core.App, vm *goja.Runtime)
	// Setup, if set, runs for each app after migrations and before it serves (host application Go code).
	Setup func(inst Instance, app core.App) error
}

func instanceDir(opts appOptions, inst Instance) string {
	if inst.Dir != "" {
		return inst.Dir
	}
	return filepath.Join(opts.PBRoot, inst.ID)
}

// startApp creates, bootstraps and migrates the instance's PocketBase app and builds its router, the same way
// `pocketbase serve` does, but without opening a listener: Pocker's own server dispatches to the returned handler.
func startApp(opts appOptions, inst Instance) (r *runningApp, err error) {
	// PocketBase's JS plugin panics on a broken hooks file; one instance's bad code must be an error, not a crash
	defer func() {
		if p := recover(); p != nil {
			r, err = nil, fmt.Errorf("%v", p)
		}
	}()

	dir, err := filepath.Abs(instanceDir(opts, inst))
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"pb_data", "pb_hooks", "pb_migrations", "pb_public"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	lim := inst.Limits.over(opts.Defaults)

	app := core.NewBaseApp(core.BaseAppConfig{
		DBConnect:     guardedDBConnect(lim.MaxQueryDuration), // SQL stays inside this app's files
		DataDir:       filepath.Join(dir, "pb_data"),
		IsDev:         inst.Dev || opts.ForceDev,
		EncryptionKey: inst.EncryptionKey,
		AppMigrations: &core.MigrationsList{}, // this app's JS migrations only
	})
	if opts.Init != nil {
		if err := opts.Init(inst, app); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}
	}

	env := make(map[string]string, len(inst.Secrets))
	for k, v := range inst.Secrets {
		env[k] = v
	}

	sb := &sandbox.Sandbox{
		Dir:                dir,
		Env:                env,
		MaxExecution:       lim.MaxJSExecution,
		MaxAllocation:      int64(lim.MaxJSAllocation),
		MaxCallStackSize:   lim.MaxCallStackSize,
		MaxSleep:           lim.MaxSleep,
		HTTPSendMaxTimeout: int(lim.HTTPSendMaxTimeout / time.Second),
		HTTPSendMaxBody:    int64(lim.HTTPSendMaxBody),
		MaxConcurrent:      lim.MaxConcurrentJS,
		Owner:              inst.ID,
		Console:            openInstanceLog(dir).Write,
	}
	err = jsvm.Register(app, jsvm.Config{
		HooksDir:      filepath.Join(dir, "pb_hooks"),
		MigrationsDir: filepath.Join(dir, "pb_migrations"),
		HooksWatch:    false, // one process serves every app: a hooks change must not restart it
		HooksPoolSize: opts.HooksPoolSize,
		OnInit: func(vm *goja.Runtime) {
			sb.OnInit(vm)
			if opts.InitJS != nil {
				opts.InitJS(inst, app, vm)
			}
		},
		OnCall: sb.OnCall,
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
	if err := syncSuperusers(app, inst.Superusers); err != nil {
		return nil, fmt.Errorf("superusers: %w", err)
	}
	if opts.Setup != nil {
		if err := opts.Setup(inst, app); err != nil {
			return nil, fmt.Errorf("setup: %w", err)
		}
	}

	handler, err := buildHandler(app, filepath.Join(dir, "pb_public"), inst.Access.CORSOrigins)
	if err != nil {
		return nil, err
	}
	return &runningApp{app: app, handler: handler, started: time.Now()}, nil
}

// buildHandler mirrors apis.Serve: router, CORS, dashboard, OnServe hooks (customer routes, pb_public) and the
// final mux. The OnServe event gets a listener that is never served, so no port is opened per app.
func buildHandler(app *core.BaseApp, publicDir string, corsOrigins []string) (http.Handler, error) {
	router, err := apis.NewRouter(app)
	if err != nil {
		return nil, err
	}

	if len(corsOrigins) == 0 {
		corsOrigins = []string{"*"}
	}
	router.Bind(apis.CORS(apis.CORSConfig{
		AllowOrigins: corsOrigins,
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

// syncSuperusers upserts the provider's superusers (id, email, token key, password hash) into _superusers.
func syncSuperusers(app core.App, users []Superuser) error {
	for _, u := range users {
		if u.Email == "" || u.PasswordHash == "" {
			continue
		}
		id := u.ID
		if id == "" {
			id = "pocker" + security.RandomStringWithAlphabet(10, "abcdefghijklmnopqrstuvwxyz0123456789")
			if existing, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, u.Email); err == nil {
				id = existing.Id
			}
		}
		tokenKey := u.TokenKey
		if tokenKey == "" {
			tokenKey = security.RandomString(50)
		}
		_, err := app.DB().NewQuery(
			"INSERT OR REPLACE INTO {{_superusers}} ([[id]], [[email]], [[tokenKey]], [[password]]) VALUES ({:id}, {:email}, {:tokenKey}, {:password})",
		).Bind(dbx.Params{"id": id, "email": u.Email, "tokenKey": tokenKey, "password": u.PasswordHash}).Execute()
		if err != nil {
			return fmt.Errorf("%s: %w", u.Email, err)
		}
	}
	return nil
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
