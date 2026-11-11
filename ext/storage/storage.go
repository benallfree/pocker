// Package storage gives every instance under a pocker server a default bucket for record uploads and backup zips
// when the instance hasn't turned on its own S3. To the instance nothing is configured: Settings show S3 off, uploads
// and the Backups screen work as before, and the bucket's credentials never reach its settings, env or JS. Downloads
// stream through the server (in-process, no proxy endpoint to probe).
//
//	srv.Plugins = append(srv.Plugins, storage.New(storage.Config{}))
//
// Settings come from the config file's top-level `storage:` key (fields set in Go win) and each instance's
// `plugins.storage` (only `enabled`). Without a bucket the plugin does nothing: files stay on local disk.
//
// Each instance gets its own folder in the bucket, <prefix>instances/<id>/storage/ and .../backups/, and can't reach
// anything outside it. When the instance turns on its own S3, PocketBase's normal code takes over with the instance's
// credentials. Files already on an instance's local disk are not read or moved: the plugin is for instances created
// with it on (legacy instances are migrated separately).
package storage

import (
	"fmt"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pockethost/pocketbase/ext/pocker"
)

// Name is the plugin's name and config key.
const Name = "storage"

// Config is the process configuration.
type Config struct {
	// Enabled is the default for every instance: on unless false (with a bucket set). An instance's
	// `plugins.storage.enabled` overrides it.
	Enabled *bool `yaml:"enabled"`

	// Uploads and Backups pick what goes to the bucket (both default true).
	Uploads *bool `yaml:"uploads"`
	Backups *bool `yaml:"backups"`

	// Bucket is the S3-compatible destination. Nil = the plugin does nothing.
	Bucket *Bucket `yaml:"bucket"`

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

	mu        sync.Mutex
	instances map[core.App]string // apps using the default bucket -> instance id
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
	return &Plugin{cfg: cfg, log: log.With("plugin", Name), instances: map[core.App]string{}}
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

// InitApp gives the instance's app the default bucket for whatever its own settings leave on local disk.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	if p.cfg.Bucket == nil {
		return nil
	}
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
	root := p.cfg.Bucket.Prefix + "instances/" + inst.ID + "/"
	p.mu.Lock()
	p.instances[app] = inst.ID
	p.mu.Unlock()
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		p.mu.Lock()
		delete(p.instances, e.App)
		p.mu.Unlock()
		return e.Next()
	})
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		s := e.App.Settings()
		switch e.Kind {
		case core.FilesystemKindStorage:
			if !on(p.cfg.Uploads) || s.S3.Enabled {
				return e.Next()
			}
		case core.FilesystemKindBackups:
			if !on(p.cfg.Backups) || s.Backups.S3.Enabled {
				return e.Next()
			}
		default:
			return e.Next()
		}
		fsys, err := p.open(root + e.Kind + "/")
		if err != nil {
			return err
		}
		e.Filesystem = fsys
		return e.Next()
	})
	return nil
}

// open returns the bucket fenced to prefix.
func (p *Plugin) open(prefix string) (*filesystem.System, error) {
	b := p.cfg.Bucket
	s3, err := filesystem.NewS3(b.Bucket, b.Region, b.Endpoint, b.AccessKey, b.Secret, b.ForcePathStyle)
	if err != nil {
		return nil, err
	}
	return filesystem.New(prefixDriver{Driver: s3.Driver(), prefix: prefix}), nil
}

// Target is where an instance's uploads live in the bucket, for code that talks to the bucket directly (e.g.
// ext/s3redirect's Config.Target, to sign downloads). ok is false when the instance's uploads aren't in the default
// bucket (no bucket, plugin or uploads off, or the instance uses its own S3).
func (p *Plugin) Target(app core.App) (b Bucket, prefix string, ok bool) {
	if p.cfg.Bucket == nil || !on(p.cfg.Uploads) || app.Settings().S3.Enabled {
		return Bucket{}, "", false
	}
	p.mu.Lock()
	id, enabled := p.instances[app], p.instances[app] != ""
	p.mu.Unlock()
	if !enabled {
		return Bucket{}, "", false
	}
	return *p.cfg.Bucket, p.cfg.Bucket.Prefix + "instances/" + id + "/" + core.FilesystemKindStorage + "/", true
}
