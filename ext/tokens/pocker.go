package tokens

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pluginkit"
)

// Name is the plugin's key under an instance's `plugins:`.
const Name = "tokens"

// Plugin is API tokens as a PocketOne plugin: on a single app with pluginkit.MustRegister, and under pocker every
// instance gets its own _tokens, routes and screen.
//
//	srv.Plugins = append(srv.Plugins, tokens.New(tokens.Config{}))
//
// Per instance, `plugins: {tokens: {enabled: false}}` turns it off and `collections: [...]` picks who may own
// tokens. Scopes are registered per app by the plugins that own them (they call tokens.For(app).RegisterScope).
type Plugin struct{ cfg Config }

var _ pluginkit.Plugin = (*Plugin)(nil)

// New returns the plugin. cfg holds the defaults for every app.
func New(cfg Config) *Plugin { return &Plugin{cfg: cfg} }

// Name implements [pluginkit.Plugin].
func (p *Plugin) Name() string { return Name }

type settings struct {
	Collections []string `json:"collections"`
}

// Register implements [pluginkit.Plugin].
func (p *Plugin) Register(app core.App, layers pluginkit.Config) error {
	cfg := p.cfg
	var s settings
	if err := layers.Decode(&s); err != nil {
		return err
	}
	if len(s.Collections) > 0 {
		cfg.Collections = s.Collections
	}
	_, err := Register(app, cfg)
	return err
}
