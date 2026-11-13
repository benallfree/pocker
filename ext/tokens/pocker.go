package tokens

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pocker"
)

// Name is the plugin's key under an instance's `plugins:`.
const Name = "tokens"

// Plugin is API tokens under a pocker server: every instance gets its own _tokens, routes and screen.
//
//	srv.Plugins = append(srv.Plugins, tokens.New(tokens.Config{}))
//
// Per instance, `plugins: {tokens: {enabled: false}}` turns it off and `collections: [...]` picks who may own
// tokens. Scopes are registered per app by the plugins that own them (they call tokens.For(app).RegisterScope).
type Plugin struct{ cfg Config }

var (
	_ pocker.Plugin    = (*Plugin)(nil)
	_ pocker.AppIniter = (*Plugin)(nil)
)

// New returns the pocker plugin. cfg holds the defaults for every instance.
func New(cfg Config) *Plugin { return &Plugin{cfg: cfg} }

// Name implements [pocker.Plugin].
func (p *Plugin) Name() string { return Name }

type instanceConfig struct {
	Enabled     *bool    `yaml:"enabled"`
	Collections []string `yaml:"collections"`
}

// InitApp implements [pocker.AppIniter].
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	cfg := p.cfg
	var y instanceConfig
	if err := pocker.DecodePluginConfig(inst.PluginConfig(Name), &y); err != nil {
		return fmt.Errorf("tokens: instance %s: %w", inst.ID, err)
	}
	if y.Enabled != nil && !*y.Enabled {
		return nil
	}
	if len(y.Collections) > 0 {
		cfg.Collections = y.Collections
	}
	_, err := Register(app, cfg)
	return err
}
