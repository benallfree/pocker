// Package pockerplugin turns on s3redirect for every pocker instance. It's its own package so single-app builds of
// ext/s3redirect (like the standard CE binary) don't link pocker.
//
//	srv.Plugins = append(srv.Plugins, pockerplugin.New(s3redirect.Config{}))
//
// Process settings come from the config file's top-level `s3redirect:` key (fields set in Go win), per-instance
// settings from the instance's `plugins: {s3redirect: {enabled, ttl}}`. Each instance signs through its own files
// filesystem (its own S3, or a bucket another plugin supplies, like ext/storage); nothing is shared between
// instances.
package pockerplugin

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/pocker"
	"github.com/pockethost/pocketbase/ext/s3redirect"
)

// Name is the plugin's name under pocker: the top-level config key and an instance's `plugins.s3redirect` block.
const Name = s3redirect.Name

var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
)

// Plugin is s3redirect under a pocker server.
type Plugin struct {
	cfg s3redirect.Config
	p   *s3redirect.Plugin
}

// New returns the pocker plugin for cfg.
func New(cfg s3redirect.Config) *Plugin { return &Plugin{cfg: cfg, p: s3redirect.New(cfg)} }

// Name implements [pocker.Plugin].
func (pp *Plugin) Name() string { return Name }

// Configure reads the config file's top-level `s3redirect:` key. Fields already set in Go are kept.
func (pp *Plugin) Configure(doc *yaml.Node, _ pocker.Config) error {
	n := pocker.TopLevel(doc, Name)
	if n == nil {
		return nil
	}
	var c s3redirect.Config
	if err := n.Decode(&c); err != nil {
		return fmt.Errorf("s3redirect: %w", err)
	}
	if pp.cfg.Enabled == nil {
		pp.cfg.Enabled = c.Enabled
	}
	if pp.cfg.TTL <= 0 && c.TTL > 0 {
		pp.cfg.TTL = c.TTL
	}
	pp.p = s3redirect.New(pp.cfg)
	return nil
}

// InitApp implements [pocker.AppIniter]: it binds the redirect on the instance's app before it bootstraps.
func (pp *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	ac, err := instanceConfig(inst)
	if err != nil {
		return err
	}
	return pp.p.SetupApp(app, ac)
}

func instanceConfig(inst pocker.Instance) (s3redirect.AppConfig, error) {
	var ac s3redirect.AppConfig
	raw := inst.PluginConfig(Name)
	if raw == nil {
		return ac, nil
	}
	switch v := raw.(type) {
	case s3redirect.AppConfig:
		return v, nil
	case *s3redirect.AppConfig:
		if v != nil {
			return *v, nil
		}
		return ac, nil
	}
	var probe map[string]any
	if err := pocker.DecodePluginConfig(raw, &probe); err != nil {
		return ac, fmt.Errorf("s3redirect: instance %s: %w", inst.ID, err)
	}
	for k := range probe {
		if k != "enabled" && k != "ttl" {
			return ac, fmt.Errorf("s3redirect: instance %s: %q can't be set per instance (only enabled, ttl)", inst.ID, k)
		}
	}
	if err := pocker.DecodePluginConfig(raw, &ac); err != nil {
		return ac, fmt.Errorf("s3redirect: instance %s: %w", inst.ID, err)
	}
	return ac, nil
}
