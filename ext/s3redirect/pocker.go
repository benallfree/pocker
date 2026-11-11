package s3redirect

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/pocker"
)

// The plugin under a multitenant pocker server:
//
//	srv.Plugins = append(srv.Plugins, s3redirect.New(s3redirect.Config{}))
//
// Process settings come from the config file's top-level `s3redirect:` key (fields set in Go win), per-instance
// settings from the instance's `plugins: {s3redirect: {enabled, ttl}}`. Each instance signs with its own S3
// settings (or [Config.Target]); nothing is shared between instances.
var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
)

// Configure reads the config file's top-level `s3redirect:` key. Fields already set in Go are kept.
func (p *Plugin) Configure(doc *yaml.Node, _ pocker.Config) error {
	n := pocker.TopLevel(doc, Name)
	if n == nil {
		return nil
	}
	var c Config
	if err := n.Decode(&c); err != nil {
		return fmt.Errorf("s3redirect: %w", err)
	}
	if p.cfg.Enabled == nil {
		p.cfg.Enabled = c.Enabled
	}
	if !p.ttlSet && c.TTL > 0 {
		p.cfg.TTL = c.TTL
	}
	return nil
}

// InitApp sets up an instance's app before it bootstraps.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	ac, err := instanceConfig(inst)
	if err != nil {
		return err
	}
	return p.SetupApp(app, ac)
}

func instanceConfig(inst pocker.Instance) (AppConfig, error) {
	var ac AppConfig
	raw := inst.PluginConfig(Name)
	if raw == nil {
		return ac, nil
	}
	switch v := raw.(type) {
	case AppConfig:
		return v, nil
	case *AppConfig:
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
