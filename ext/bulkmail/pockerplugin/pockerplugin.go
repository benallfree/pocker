// Package pockerplugin turns on bulk mail for every pocker instance. It's its own package so single-app builds of
// ext/bulkmail (like the standard CE binary) don't link pocker.
//
//	srv.Plugins = append(srv.Plugins, pockerplugin.New(bulkmail.Config{}))
package pockerplugin

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"

	"github.com/pockethost/pocketbase/ext/bulkmail"
	"github.com/pockethost/pocketbase/ext/pocker"
)

// Name is the plugin's key in a pocker config file and under an instance's `plugins:`.
const Name = bulkmail.Name

// Plugin is bulk mail under a multitenant pocker server: every instance gets its own campaigns, sender, Mail screens
// and $bulkmail, within the limits below.
//
//	srv.Plugins = append(srv.Plugins, pockerplugin.New(bulkmail.Config{}))
//
// Process settings come from the config file's top-level `bulkmail:` key (fields set in Go win); per instance,
// `plugins: {bulkmail: {enabled, rate, daily}}` (the instance source sets them, e.g. per plan). Including the plugin
// is the opt-in; `enabled: false` turns it off for the server or for one instance. `daily: 0` in YAML means the
// instance may not send bulk mail (the screens say so).
type Plugin struct {
	cfg     bulkmail.Config
	enabled *bool
}

var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
	_ pocker.JSIniter   = (*Plugin)(nil)
)

// New returns the pocker plugin. cfg holds the defaults for every instance.
func New(cfg bulkmail.Config) *Plugin { return &Plugin{cfg: cfg} }

// Name implements [pocker.Plugin].
func (p *Plugin) Name() string { return Name }

// yamlConfig is the shape of the top-level `bulkmail:` key and of an instance's `plugins.bulkmail`.
type yamlConfig struct {
	Enabled *bool    `yaml:"enabled"`
	Rate    *float64 `yaml:"rate"`
	Daily   *int     `yaml:"daily"`
}

func (y yamlConfig) apply(c *bulkmail.Config) {
	if y.Rate != nil && *y.Rate > 0 {
		c.Rate = *y.Rate
	}
	if y.Daily != nil {
		c.Daily = *y.Daily
		if *y.Daily <= 0 {
			c.Daily = bulkmail.NoBulkMail
		}
	}
}

// Configure implements [pocker.Configurer]: the config file's top-level `bulkmail:` key. Fields set in Go win.
func (p *Plugin) Configure(doc *yaml.Node, _ pocker.Config) error {
	n := pocker.TopLevel(doc, Name)
	if n == nil {
		return nil
	}
	var y yamlConfig
	if err := n.Decode(&y); err != nil {
		return fmt.Errorf("bulkmail: %w", err)
	}
	p.enabled = y.Enabled
	fromGo := p.cfg
	y.apply(&p.cfg)
	if fromGo.Rate > 0 {
		p.cfg.Rate = fromGo.Rate
	}
	if fromGo.Daily != 0 {
		p.cfg.Daily = fromGo.Daily
	}
	return nil
}

// InitApp implements [pocker.AppIniter]: registers bulk mail on the instance's app before it bootstraps.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	cfg := p.cfg
	enabled := p.enabled == nil || *p.enabled
	if raw := inst.PluginConfig(Name); raw != nil {
		var y yamlConfig
		if err := pocker.DecodePluginConfig(raw, &y); err != nil {
			return fmt.Errorf("bulkmail: instance %s: %w", inst.ID, err)
		}
		if y.Enabled != nil {
			enabled = *y.Enabled
		}
		y.apply(&cfg)
	}
	if !enabled {
		return nil
	}
	_, err := bulkmail.Register(app, cfg)
	return err
}

// InitJS implements [pocker.JSIniter]: $bulkmail for the instance's JS hooks. It only reaches the instance's own
// app (its data, its mail settings, its limits), so it's safe for an untrusted tenant.
func (p *Plugin) InitJS(_ pocker.Instance, app core.App, vm *goja.Runtime) {
	if bulkmail.For(app) != nil {
		bulkmail.BindJS(vm, app)
	}
}
