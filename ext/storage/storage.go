// Package storage is the operator's fall-through storage: named S3-compatible backends, and mounts that send each
// filesystem kind (record uploads, backup zips, PITR history, any plugin's own kind) to one of them. To an app
// nothing is configured: Settings show S3 off, uploads and the Backups screen work as before, and the backends'
// credentials never reach its settings, env or JS. Downloads stream through the server, or, with ext/s3redirect,
// become signed redirects (to s3redirect a mount is just another S3 filesystem).
//
//	srv.Plugins = append(srv.Plugins, storage.New(storage.Config{}))
//
//	storage:
//	  backends:
//	    r2-eu:   { endpoint: …, bucket: pocketbase-eu, region: auto, access_key: …, secret: …, prefix: "" }
//	  mounts:              # kind → backend
//	    storage:    r2-eu  # record uploads (app.NewFilesystem)
//	    backups:    r2-eu  # backup zips (app.NewBackupsFilesystem)
//	    litestream: r2-eu  # PITR history (ext/pitr asks for kind "litestream")
//
// Each app gets <backend prefix><root><kind>/ per mounted kind and can't reach anything outside it. The root is the
// caller's choice and opaque to the plugin: under pocker, InitApp passes "instances/<id>/"; a single app passes "" (or
// any name). Several deployments sharing one bucket keep apart with their own backend prefix or roots. When an app turns on its own S3 for uploads
// or backups, PocketBase's normal code takes over with the app's credentials. Other kinds are served whenever they're
// mounted: the plugin asking for them (e.g. ext/pitr) checks its own app setting first. Without mounts the plugin
// does nothing. Files already on an app's local disk are not read or moved.
//
// The old shape (`bucket:` + `uploads:`/`backups:`) still works: it becomes one backend named "default" mounted for
// storage and backups, with a warning.
package storage

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pockethost/pocketbase/ext/pocker"
	"github.com/pockethost/pocketbase/ext/s3redirect/sigv4"
)

// Name is the plugin's name and config key.
const Name = "storage"

// Config is the process configuration.
type Config struct {
	// Enabled is the default for every instance: on unless false. An instance's `plugins.storage.enabled`
	// overrides it.
	Enabled *bool `yaml:"enabled"`

	// Backends are the named S3-compatible destinations.
	Backends map[string]*Bucket `yaml:"backends"`

	// Mounts maps a filesystem kind ("storage", "backups", "litestream", …) to a backend name.
	Mounts map[string]string `yaml:"mounts"`

	// Deprecated: the old single-bucket shape. Bucket becomes backend "default", mounted for storage (unless
	// Uploads is false) and backups (unless Backups is false).
	Bucket  *Bucket `yaml:"bucket"`
	Uploads *bool   `yaml:"uploads"`
	Backups *bool   `yaml:"backups"`

	Logger *slog.Logger `yaml:"-"`
}

// Bucket is an S3-compatible destination.
type Bucket struct {
	Endpoint       string `yaml:"endpoint"`
	Bucket         string `yaml:"bucket"`
	Region         string `yaml:"region"`
	AccessKey      string `yaml:"access_key"`
	Secret         string `yaml:"secret"`
	Prefix         string `yaml:"prefix"` // prepended to every key
	ForcePathStyle bool   `yaml:"force_path_style"`
}

// InstanceConfig is an instance's `plugins.storage`.
type InstanceConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// Plugin is the pocker plugin.
type Plugin struct {
	cfg Config
	log *slog.Logger
}

var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
)

// New returns the plugin for cfg.
func New(cfg Config) *Plugin {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Plugin{cfg: cfg, log: log.With("plugin", Name)}
}

// Name implements pocker.Plugin.
func (p *Plugin) Name() string { return Name }

// Config returns the effective configuration.
func (p *Plugin) Config() Config { return p.cfg }

// Configure reads the config file's top-level `storage:` key; fields set in Go are kept.
func (p *Plugin) Configure(doc *yaml.Node, _ pocker.Config) error {
	n := pocker.TopLevel(doc, Name)
	if n == nil {
		return nil
	}
	var c Config
	if err := n.Decode(&c); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if p.cfg.Enabled == nil {
		p.cfg.Enabled = c.Enabled
	}
	if p.cfg.Backends == nil {
		p.cfg.Backends = c.Backends
	}
	if p.cfg.Mounts == nil {
		p.cfg.Mounts = c.Mounts
	}
	if p.cfg.Uploads == nil {
		p.cfg.Uploads = c.Uploads
	}
	if p.cfg.Backups == nil {
		p.cfg.Backups = c.Backups
	}
	if p.cfg.Bucket == nil {
		p.cfg.Bucket = c.Bucket
	}
	return nil
}

func on(b *bool) bool { return b == nil || *b }

// mounts returns kind → backend, with the deprecated single-bucket shape folded in and every mount checked.
func (p *Plugin) mounts() (map[string]*Bucket, error) {
	backends := map[string]*Bucket{}
	for name, b := range p.cfg.Backends {
		if b == nil {
			return nil, fmt.Errorf("storage: backend %q is empty", name)
		}
		backends[name] = b
	}
	mounts := map[string]string{}
	for kind, name := range p.cfg.Mounts {
		mounts[kind] = name
	}
	if p.cfg.Bucket != nil {
		if _, taken := backends["default"]; taken {
			return nil, fmt.Errorf("storage: `bucket:` (deprecated) and a backend named \"default\" can't both be set")
		}
		backends["default"] = p.cfg.Bucket
		if _, set := mounts[core.FilesystemKindStorage]; !set && on(p.cfg.Uploads) {
			mounts[core.FilesystemKindStorage] = "default"
		}
		if _, set := mounts[core.FilesystemKindBackups]; !set && on(p.cfg.Backups) {
			mounts[core.FilesystemKindBackups] = "default"
		}
		p.log.Warn("storage: `bucket:`/`uploads:`/`backups:` are deprecated; use `backends:` and `mounts:`")
	}
	out := map[string]*Bucket{}
	kinds := make([]string, 0, len(mounts))
	for kind := range mounts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		b, ok := backends[mounts[kind]]
		if !ok {
			return nil, fmt.Errorf("storage: mount %q names unknown backend %q", kind, mounts[kind])
		}
		out[kind] = b
	}
	return out, nil
}

// FS returns the filesystem for kind under root, fenced to <backend prefix><root><kind>/. ok is false when the kind
// isn't mounted. Close the result when done.
func (p *Plugin) FS(root, kind string) (fsys *filesystem.System, ok bool, err error) {
	mounts, err := p.mounts()
	if err != nil {
		return nil, false, err
	}
	b, ok := mounts[kind]
	if !ok {
		return nil, false, nil
	}
	fsys, err = open(b, keyPrefix(b, root, kind))
	return fsys, err == nil, err
}

// keyPrefix is <backend prefix><root><kind>/, with root ending in "/" when set.
func keyPrefix(b *Bucket, root, kind string) string {
	if root != "" && !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return b.Prefix + root + kind + "/"
}

// Bind serves app's filesystems from the mounts under root (opaque, the caller's choice: "" for a single app,
// "instances/<id>/" under pocker). Uploads and backups step aside when the app turned on its own S3 for them; any
// other mounted kind is served whenever it's asked for.
func (p *Plugin) Bind(app core.App, root string) error {
	mounts, err := p.mounts()
	if err != nil || len(mounts) == 0 {
		return err
	}
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		b, mounted := mounts[e.Kind]
		if !mounted {
			return e.Next()
		}
		s := e.App.Settings()
		switch e.Kind {
		case core.FilesystemKindStorage:
			if s.S3.Enabled {
				return e.Next()
			}
		case core.FilesystemKindBackups:
			if s.Backups.S3.Enabled {
				return e.Next()
			}
		}
		fsys, err := open(b, keyPrefix(b, root, e.Kind))
		if err != nil {
			return err
		}
		e.Filesystem = fsys
		return e.Next()
	})
	return nil
}

// InitApp binds the instance's app to the mounts under "instances/<id>/", unless the instance has the plugin off.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	var ic InstanceConfig
	if raw := inst.PluginConfig(Name); raw != nil {
		var probe map[string]any
		if err := pocker.DecodePluginConfig(raw, &probe); err == nil {
			for k := range probe {
				if k != "enabled" {
					return fmt.Errorf("storage: instance %s: %q can't be set per instance (only enabled)", inst.ID, k)
				}
			}
		}
		if err := pocker.DecodePluginConfig(raw, &ic); err != nil {
			return fmt.Errorf("storage: instance %s: %w", inst.ID, err)
		}
	}
	enabled := on(p.cfg.Enabled)
	if ic.Enabled != nil {
		enabled = *ic.Enabled
	}
	if !enabled {
		return nil
	}
	return p.Bind(app, "instances/"+inst.ID+"/")
}

// open returns b fenced to prefix.
func open(b *Bucket, prefix string) (*filesystem.System, error) {
	s3, err := filesystem.NewS3(b.Bucket, b.Region, b.Endpoint, b.AccessKey, b.Secret, b.ForcePathStyle)
	if err != nil {
		return nil, err
	}
	sign := sigv4.Config{Endpoint: b.Endpoint, Bucket: b.Bucket, Region: b.Region, AccessKey: b.AccessKey,
		SecretKey: b.Secret, PathStyle: b.ForcePathStyle}
	return filesystem.New(prefixDriver{Driver: s3.Driver(), prefix: prefix, sign: sign}), nil
}
