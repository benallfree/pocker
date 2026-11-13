package bulkmail

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/ceplugin"
)

// Plugin is bulk mail as a PocketOne plugin ([ceplugin.Plugin]): the same value works on a single app and on
// every instance under pocker.
//
//	ceplugin.MustRegister(app, bulkmail.New(bulkmail.Config{}))         // a single app
//	srv.Plugins = append(srv.Plugins, bulkmail.New(bulkmail.Config{})) // every pocker instance
//
// Settings (a pocker config file's top-level `bulkmail:` key, then an instance's `plugins.bulkmail`) apply over
// the Config given to New: `rate`, `daily` (0 means this app may not send bulk mail; the screens say so) and
// `enabled`, which the host checks.
type Plugin struct {
	cfg Config
}

var (
	_ ceplugin.Plugin   = (*Plugin)(nil)
	_ ceplugin.JSBinder = (*Plugin)(nil)
)

// New returns the plugin. cfg holds the defaults for every app it's registered on.
func New(cfg Config) *Plugin { return &Plugin{cfg: cfg} }

// Name implements [ceplugin.Plugin].
func (p *Plugin) Name() string { return Name }

// settings is the shape of the plugin's settings layers.
type settings struct {
	Rate  *float64 `json:"rate"`
	Daily *int     `json:"daily"`
}

// Register implements [ceplugin.Plugin].
func (p *Plugin) Register(app core.App, cfg ceplugin.Config) error {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	c := p.cfg
	if s.Rate != nil && *s.Rate > 0 {
		c.Rate = *s.Rate
	}
	if s.Daily != nil {
		c.Daily = *s.Daily
		if *s.Daily <= 0 {
			c.Daily = NoBulkMail
		}
	}
	_, err := Register(app, c)
	return err
}

// BindJS implements [ceplugin.JSBinder]: $bulkmail in the app's JS hooks. It only reaches the app's own data, mail
// settings and limits, so it's safe for an untrusted tenant under pocker.
func (p *Plugin) BindJS(app core.App, vm *goja.Runtime) {
	if For(app) != nil {
		BindJS(vm, app)
	}
}
