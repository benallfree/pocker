// Package pages serves server-rendered multi-page apps from an app's pb_pages folder (PocketPages v1).
//
// The contract is SPEC.md in this folder. This package owns the framework side: route discovery and matching,
// static files and fingerprints, and (later) the EJS compiler; page code runs in the app's JS VM.
package pages

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

// MiddlewareID is the id of the router middleware that serves pages.
const MiddlewareID = "extPages"

// MiddlewarePriority runs pages after PocketBase's own early middlewares (rate limit, body limit, auth token) and
// before routes and user middlewares.
const MiddlewarePriority = apis.DefaultBodyLimitMiddlewarePriority + 10

const storeKey = "extPages"

// Config configures pages for one app.
type Config struct {
	// Dir is the pb_pages folder. Default: pb_pages next to the app's pb_data. A relative path resolves against the
	// working directory.
	Dir string

	// Dev turns on detailed error pages and debug logging. Default: the app's dev mode.
	Dev *bool

	// RescanInterval is how often, at most, a request checks pb_pages for changes (default 1s; negative turns the
	// check off, so the table is built once).
	RescanInterval time.Duration
}

// Pages is the pages service registered on one app.
type Pages struct {
	app core.App
	cfg Config
	dir string

	mu        sync.Mutex
	table     *Table
	stamp     string // fingerprint of the tree the table was built from
	lastCheck time.Time
	buildErrs []error

	hashes   hashCache
	programs programs

	router  *router.Router[*core.RequestEvent] // for ctx.pb()'s in-process transport
	muxOnce sync.Once
	mux     http.Handler
	muxErr  error
}

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *Pages {
	p, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return p
}

// Register adds pages to app. Call it before app.Start(), or from pocker's SetupApp. If the pb_pages folder doesn't
// exist, pages does nothing for that app (it starts serving if the folder appears later).
func Register(app core.App, cfg Config) (*Pages, error) {
	dir := cfg.Dir
	if dir == "" {
		dir = filepath.Join(app.DataDir(), "..", "pb_pages")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if cfg.RescanInterval == 0 {
		cfg.RescanInterval = time.Second
	}

	p := &Pages{app: app, cfg: cfg, dir: abs}
	app.Store().Set(storeKey, p)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		p.refresh(true)
		p.router = se.Router
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       MiddlewareID,
			Priority: MiddlewarePriority,
			Func:     p.serve,
		})
		return se.Next()
	})

	return p, nil
}

// For returns the pages service registered on app, or nil.
func For(app core.App) *Pages {
	p, _ := app.Store().Get(storeKey).(*Pages)
	return p
}

// Dir returns the pb_pages folder.
func (p *Pages) Dir() string { return p.dir }

// Table returns the current route table (nil when pb_pages doesn't exist or never built cleanly).
func (p *Pages) Table() *Table {
	p.refresh(false)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.table
}

// Errors returns the problems found by the last build (bad names, conflicts). Empty when it built cleanly.
func (p *Pages) Errors() []error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]error(nil), p.buildErrs...)
}

func (p *Pages) dev() bool {
	if p.cfg.Dev != nil {
		return *p.cfg.Dev
	}
	return p.app.IsDev()
}

func (p *Pages) logger() *slog.Logger { return p.app.Logger().With("plugin", "pages") }

// refresh rebuilds the table when pb_pages changed. force skips the rate limit.
func (p *Pages) refresh(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !force {
		if p.cfg.RescanInterval < 0 && p.lastCheck != (time.Time{}) {
			return
		}
		if time.Since(p.lastCheck) < p.cfg.RescanInterval {
			return
		}
	}
	p.lastCheck = time.Now()

	stamp, err := treeStamp(p.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			p.table, p.stamp, p.buildErrs = nil, "", nil
			return
		}
		p.logger().Error("pages: can't read pb_pages", "dir", p.dir, "error", err)
		return
	}
	if stamp == p.stamp && !force {
		return
	}

	table, errs := Build(p.dir)
	p.stamp = stamp
	p.buildErrs = errs
	if len(errs) > 0 {
		for _, e := range errs {
			p.logger().Error("pages: "+e.Error(), "dir", p.dir)
		}
		if p.table != nil {
			p.logger().Warn("pages: keeping the previous routes until pb_pages builds cleanly")
			return
		}
		// no previous table: serve what built, so one bad file doesn't take the whole site down
	}
	p.table = table
	p.hashes.reset()
}
