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
// anything outside it. Files already on local disk stay readable: the bucket is tried first, then the local folder,
// and a file read from the local folder is moved up in the background (lazy move, no cutover). When the instance
// turns on its own S3, PocketBase's normal code takes over with the instance's credentials.
package storage

import (
	"fmt"
	"log/slog"
	"path/filepath"
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

	mu     sync.Mutex
	moving map[string]bool // "<dir>|<key>" being moved up
	moves  sync.WaitGroup
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
	return &Plugin{cfg: cfg, log: log.With("plugin", Name), moving: map[string]bool{}}
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
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		s := e.App.Settings()
		var localDir string
		switch e.Kind {
		case core.FilesystemKindStorage:
			if !on(p.cfg.Uploads) || s.S3.Enabled {
				return e.Next()
			}
			localDir = filepath.Join(e.App.DataDir(), core.LocalStorageDirName)
		case core.FilesystemKindBackups:
			if !on(p.cfg.Backups) || s.Backups.S3.Enabled {
				return e.Next()
			}
			localDir = filepath.Join(e.App.DataDir(), core.LocalBackupsDirName)
		default:
			return e.Next()
		}
		fsys, err := p.open(root+e.Kind+"/", localDir)
		if err != nil {
			return err
		}
		e.Filesystem = fsys
		return e.Next()
	})
	return nil
}

// open returns the fenced bucket with the local folder as fallback.
func (p *Plugin) open(prefix, localDir string) (*filesystem.System, error) {
	primary, local, err := p.drivers(prefix, localDir)
	if err != nil {
		return nil, err
	}
	return filesystem.New(&fallbackDriver{primary: primary, local: local, onLocalHit: func(key string) {
		p.moveUp(prefix, localDir, key)
	}}), nil
}

func (p *Plugin) drivers(prefix, localDir string) (primary, local blobDriver, err error) {
	b := p.cfg.Bucket
	s3, err := filesystem.NewS3(b.Bucket, b.Region, b.Endpoint, b.AccessKey, b.Secret, b.ForcePathStyle)
	if err != nil {
		return nil, nil, err
	}
	l, err := filesystem.NewLocal(localDir)
	if err != nil {
		s3.Close()
		return nil, nil, err
	}
	return prefixDriver{Driver: s3.Driver(), prefix: prefix}, l.Driver(), nil
}

// moveUp copies a local file into the bucket and removes the local copy, in the background and once per key.
func (p *Plugin) moveUp(prefix, localDir, key string) {
	id := localDir + "|" + key
	p.mu.Lock()
	if p.moving[id] {
		p.mu.Unlock()
		return
	}
	p.moving[id] = true
	p.moves.Add(1)
	p.mu.Unlock()
	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.moving, id)
			p.mu.Unlock()
			p.moves.Done()
		}()
		primary, local, err := p.drivers(prefix, localDir)
		if err != nil {
			p.log.Warn("storage: lazy move", "key", key, "error", err)
			return
		}
		defer primary.Close()
		defer local.Close()
		if err := moveFile(local, primary, key); err != nil {
			p.log.Warn("storage: moving a file to the bucket failed; it stays local", "dir", localDir, "key", key, "error", err)
		}
	}()
}

// Wait blocks until background moves finish (tests, shutdown).
func (p *Plugin) Wait() { p.moves.Wait() }

