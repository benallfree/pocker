// Package pitr adds point-in-time restore to PocketBase: Litestream continuously copies the app's data.db (to
// pb_data/litestream/ or to an S3-compatible bucket), and the app's superuser can put the database back as it was at
// any moment inside the retention window, from Settings → Backups.
//
// Single app:
//
//	app := pocketbase.New()
//	pitr.MustRegister(app, pitr.Config{})
//
// Many apps in one process: add it to a pocker server's plugins; settings come from the config file's top-level
// `pitr:` key and each instance's `plugins.pitr`:
//
//	srv.Plugins = append(srv.Plugins, pitr.New(pitr.Config{}))
package pitr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/file"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
)

// Name is the plugin's name: its config key (plugins.pitr), route prefix (/api/pitr) and admin extension name.
const Name = "pitr"

// DirName is the pb_data subfolder that holds everything this plugin writes. It is left out of backup zips.
const DirName = "litestream"

const (
	DefaultRetention        = 72 * time.Hour
	DefaultSnapshotInterval = 24 * time.Hour
)

// Config is the process-wide configuration.
type Config struct {
	// Enabled is the default for every app: on unless set to false (including the plugin is the opt-in). An app's
	// [AppConfig.Enabled] overrides it, e.g. off for a hosting plan without PITR. There is no admin switch.
	Enabled *bool `yaml:"enabled"`

	// Retention is how far back a restore can go (default 72h). An app's [AppConfig.Retention] overrides it.
	Retention time.Duration `yaml:"retention"`

	// SnapshotInterval is how often a full copy of the database is taken (default 24h). Restores replay the changes
	// since the snapshot before the chosen time, so a shorter interval makes restores faster and uses more storage.
	SnapshotInterval time.Duration `yaml:"snapshot_interval"`

	Logger *slog.Logger `yaml:"-"`
}

// AppConfig is one app's configuration.
type AppConfig struct {
	// Enabled overrides [Config.Enabled] when set.
	Enabled *bool `yaml:"enabled"`

	// Retention overrides [Config.Retention] when non-zero (e.g. a hosting plan's limit).
	Retention time.Duration `yaml:"retention"`
}

// Plugin is one process's point-in-time restore: one Litestream store per retention value, shared by every app.
type Plugin struct {
	cfg Config
	log *slog.Logger

	mu     sync.Mutex
	stores map[time.Duration]*litestream.Store
	apps   map[core.App]*appPITR
	closed bool

	retentionSet, intervalSet bool // set in Go: the config file doesn't override them
}

// New returns the plugin for cfg, filling defaults.
func New(cfg Config) *Plugin {
	retentionSet, intervalSet := cfg.Retention > 0, cfg.SnapshotInterval > 0
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultRetention
	}
	if cfg.SnapshotInterval <= 0 {
		cfg.SnapshotInterval = DefaultSnapshotInterval
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Plugin{cfg: cfg, log: log.With("plugin", Name), stores: map[time.Duration]*litestream.Store{}, apps: map[core.App]*appPITR{},
		retentionSet: retentionSet, intervalSet: intervalSet}
}

// Name implements the plugin interface.
func (p *Plugin) Name() string { return Name }

// Config returns the effective process configuration.
func (p *Plugin) Config() Config { return p.cfg }

// store returns the Litestream store for a retention value, opening it on first use. Litestream applies snapshot
// retention per store, so apps with different retention (hosting plans) get different stores.
func (p *Plugin) store(retention time.Duration) (*litestream.Store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("pitr: closed")
	}
	if s := p.stores[retention]; s != nil {
		return s, nil
	}
	s := litestream.NewStore(nil, litestream.CompactionLevels{
		{Level: 0},
		{Level: 1, Interval: 10 * time.Second},
		{Level: 2, Interval: 5 * time.Minute},
	})
	s.SnapshotInterval = p.cfg.SnapshotInterval
	s.SnapshotRetention = retention
	s.Logger = quietLogger(p.log)
	if err := s.Open(context.Background()); err != nil {
		return nil, err
	}
	p.stores[retention] = s
	return s, nil
}

// Close flushes and closes every store. Apps should be terminated first.
func (p *Plugin) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	for _, s := range p.stores {
		errs = append(errs, s.Close(ctx))
	}
	return errors.Join(errs...)
}

// resolve merges an app's config over the process config.
func (p *Plugin) resolve(ac AppConfig) (enabled bool, retention time.Duration) {
	enabled = p.cfg.Enabled == nil || *p.cfg.Enabled
	if ac.Enabled != nil {
		enabled = *ac.Enabled
	}
	retention = p.cfg.Retention
	if ac.Retention > 0 {
		retention = ac.Retention
	}
	return enabled, retention
}

// destination is where an app's history goes, in order: the app's own S3 (its PITR settings), the operator's
// fall-through storage (core filesystem kind "litestream", e.g. an ext/storage mount), else pb_data/litestream/.
type destination struct {
	client litestream.ReplicaClient
	kind   string // "s3" (the app's own bucket), "storage" (the operator's) or "local"
	label  string // shown in the admin
	close  func() // releases the filesystem behind client
}

func (a *appPITR) destination() (*destination, error) {
	s, err := LoadSettings(a.app)
	if err != nil {
		return nil, err
	}
	if s.S3.Enabled {
		fsys, err := s.S3.open()
		if err != nil {
			return nil, err
		}
		return &destination{client: newBucketClient(fsys, s.S3.prefix()), kind: "s3", label: s.S3.Bucket + "/" + s.S3.prefix(),
			close: func() { fsys.Close() }}, nil
	}
	fsys, err := a.app.NewFilesystemOfKind(Kind, nil)
	if err != nil {
		return nil, err
	}
	if fsys != nil {
		return &destination{client: newBucketClient(fsys, ""), kind: "storage", label: "server storage", close: func() { fsys.Close() }}, nil
	}
	return &destination{client: file.NewReplicaClient(filepath.Join(a.dataDir(), DirName, "replica")), kind: "local",
		label: "pb_data/" + DirName, close: func() {}}, nil
}

// SetupApp adds point-in-time restore to app. Call it before the app bootstraps (pocketbase.New → before Start;
// under pocker the plugin's InitApp calls it). Replication starts when the app serves (OnServe), so CLI commands run in another
// process never write to the history. With PITR disabled for the app it adds nothing.
func (p *Plugin) SetupApp(app core.App, ac AppConfig) error {
	enabled, retention := p.resolve(ac)
	if !enabled {
		return nil
	}
	a := &appPITR{p: p, app: app, retention: retention}
	p.mu.Lock()
	p.apps[app] = a
	p.mu.Unlock()
	app.OnBootstrap().BindFunc(a.onBootstrap)
	app.OnTerminate().BindFunc(a.onTerminate)
	exclude := func(e *core.BackupEvent) error {
		e.Exclude = append(e.Exclude, DirName)
		return e.Next()
	}
	app.OnBackupCreate().BindFunc(exclude)
	app.OnBackupRestore().BindFunc(exclude)
	app.OnServe().BindFunc(a.onServe) // replication starts when the app serves, not for CLI commands
	return nil
}

// appPITR is one app's replication.
type appPITR struct {
	p         *Plugin
	app       core.App
	retention time.Duration

	mu        sync.Mutex
	db        *litestream.DB
	store     *litestream.Store
	dest      *destination // where the attached db replicates to
	restoring bool
}

func (a *appPITR) dataDir() string  { return a.app.DataDir() }
func (a *appPITR) dbPath() string   { return filepath.Join(a.dataDir(), "data.db") }
func (a *appPITR) stateDir() string { return filepath.Join(a.dataDir(), DirName) }

func (a *appPITR) logger() *slog.Logger { return a.p.log.With("dataDir", a.dataDir()) }

func (a *appPITR) onBootstrap(e *core.BootstrapEvent) error {
	swapped, err := applyPending(a.dataDir())
	if err != nil {
		a.logger().Error("pitr: applying the staged restore failed", "error", err)
	}
	if err := e.Next(); err != nil {
		if swapped {
			if rerr := rollback(a.dataDir(), err); rerr != nil {
				a.logger().Error("pitr: rollback failed", "error", rerr)
			}
		}
		return err
	}
	if swapped {
		a.logger().Info("pitr: restored")
	}
	return nil
}

func (a *appPITR) onTerminate(e *core.TerminateEvent) error {
	err := e.Next() // PocketBase closes its connections first (POSIX locks are per process)
	a.detach()
	a.p.mu.Lock()
	delete(a.p.apps, a.app)
	a.p.mu.Unlock()
	return err
}

func (p *Plugin) appFor(app core.App) *appPITR {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.apps[app]
}

var errNotEnabled = errors.New("pitr: not enabled for this app")

// Status reports an app's restorable window (for host code; the admin uses GET /api/pitr).
func (p *Plugin) Status(ctx context.Context, app core.App) (*Status, error) {
	a := p.appFor(app)
	if a == nil {
		return nil, errNotEnabled
	}
	return a.Status(ctx)
}

// Restore puts an app's database back to `at`: staged while the app serves, swapped in by app.Restart().
func (p *Plugin) Restore(ctx context.Context, app core.App, at time.Time) error {
	a := p.appFor(app)
	if a == nil {
		return errNotEnabled
	}
	return a.Restore(ctx, at)
}

// storeSync flushes an app's pending changes to its history.
func (p *Plugin) storeSync(app core.App) error {
	a := p.appFor(app)
	if a == nil {
		return errNotEnabled
	}
	return a.sync(context.Background())
}

// attach registers the app's database with Litestream.
func (a *appPITR) attach() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return nil
	}
	store, err := a.p.store(a.retention)
	if err != nil {
		return err
	}
	dest, err := a.destination()
	if err != nil {
		return err
	}
	client := dest.client
	if err := os.MkdirAll(a.stateDir(), 0o755); err != nil {
		dest.close()
		return err
	}
	db := litestream.NewDB(a.dbPath())
	db.SetMetaPath(filepath.Join(a.stateDir(), "meta")) // not next to data.db: it would end up in backup zips
	db.Logger = quietLogger(a.logger())
	r := litestream.NewReplicaWithClient(db, client)
	db.Replica = r
	if fc, ok := client.(*file.ReplicaClient); ok {
		fc.Replica = r
	}
	if err := store.RegisterDB(db); err != nil {
		dest.close()
		return fmt.Errorf("pitr: %w", err)
	}
	a.db, a.store, a.dest = db, store, dest
	return nil
}

// detach flushes and unregisters the app's database.
func (a *appPITR) detach() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := a.db.Path()
	if _, err := a.store.SyncDB(ctx, path, true); err != nil {
		a.logger().Warn("pitr: final sync failed", "error", err)
	}
	if err := a.store.UnregisterDB(ctx, path); err != nil {
		a.logger().Warn("pitr: unregister failed", "error", err)
	}
	if a.dest != nil {
		a.dest.close()
	}
	a.db, a.store, a.dest = nil, nil, nil
}

// sync flushes the app's pending changes to the replica (before a restore, so the newest changes are included).
func (a *appPITR) sync(ctx context.Context) error {
	a.mu.Lock()
	store, db := a.store, a.db
	a.mu.Unlock()
	if db == nil {
		return nil
	}
	_, err := store.SyncDB(ctx, db.Path(), true)
	return err
}

// quietLogger keeps Litestream's own info logs out of the app's logs.
func quietLogger(l *slog.Logger) *slog.Logger {
	return slog.New(levelHandler{min: slog.LevelWarn, h: l.Handler()})
}

type levelHandler struct {
	min slog.Level
	h   slog.Handler
}

func (l levelHandler) Enabled(ctx context.Context, lv slog.Level) bool {
	return lv >= l.min && l.h.Enabled(ctx, lv)
}
func (l levelHandler) Handle(ctx context.Context, r slog.Record) error { return l.h.Handle(ctx, r) }
func (l levelHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return levelHandler{l.min, l.h.WithAttrs(a)}
}
func (l levelHandler) WithGroup(n string) slog.Handler { return levelHandler{l.min, l.h.WithGroup(n)} }

var _ io.Closer = (*filesystem.System)(nil)

// reattach restarts replication against the current destination (after the app's PITR settings changed). History
// already at the old destination stays there; Litestream starts a fresh snapshot at the new one.
func (a *appPITR) reattach() error {
	a.detach()
	return a.attach()
}
