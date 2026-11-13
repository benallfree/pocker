// Package ceplugin is PocketBase CE's plugin contract: one plugin value that works on a single app and,
// unchanged, on every instance under pocker (ext/pocker hosts any [Plugin] in Server.Plugins).
//
// A plugin implements Name and Register, and optionally [JSBinder]:
//
//	type Plugin struct{ cfg Config }
//
//	func New(cfg Config) *Plugin { return &Plugin{cfg: cfg} }
//	func (p *Plugin) Name() string { return "hello" }
//	func (p *Plugin) Register(app core.App, cfg ceplugin.Config) error {
//		c := p.cfg
//		if err := cfg.Decode(&c); err != nil { // settings for this app, over the defaults given to New
//			return err
//		}
//		app.OnServe().BindFunc(...) // hooks, routes, collections, dashboard screens
//		return nil
//	}
//
// A single app registers it with [MustRegister] (and [BindJS] from jsvm's OnInit). Pocker calls Register for every
// instance with that instance's settings.
package ceplugin

import (
	"encoding/json"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
)

// Plugin is a PocketBase CE plugin.
type Plugin interface {
	// Name is the plugin's key: a top-level key in a pocker config file, an instance's `plugins.<name>` block, the
	// prefix of its collections (`_<name>_`) and its name in logs.
	Name() string

	// Register installs the plugin on app before it bootstraps: hooks, routes, collections, dashboard screens.
	// cfg holds the settings for this app (empty on a plain single app). It runs on every start of the app.
	Register(app core.App, cfg Config) error
}

// JSBinder is a plugin that exposes its Go API to the app's JS hooks (e.g. as a $name global). It's called for each
// JS runtime of an app the plugin is registered on. Under pocker, what it exposes must be safe for a hostile tenant
// to call on its own app.
type JSBinder interface {
	BindJS(app core.App, vm *goja.Runtime)
}

// Config is a plugin's settings for one app, as layers applied in order: the process config (a pocker config
// file's top-level `<name>:` key), then the app's own (an instance's `plugins.<name>`). Each layer is a map from
// YAML or JSON. The plugin's own defaults, the ones given to its constructor, sit under all of them.
type Config []map[string]any

// Decode applies every layer, in order, onto out (a pointer to a struct with json tags, or a map): keys a layer
// sets replace what earlier layers or out had, keys it doesn't set are kept. Use pointer fields to tell "not set"
// from a zero value.
func (c Config) Decode(out any) error {
	for _, layer := range c {
		if len(layer) == 0 {
			continue
		}
		b, err := json.Marshal(layer)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, out); err != nil {
			return err
		}
	}
	return nil
}

// Enabled reports the `enabled` setting: the last layer that sets it wins, and a plugin is on unless a layer says
// `enabled: false`. Hosts check it before calling Register, so plugins don't have to.
func (c Config) Enabled() bool {
	enabled := true
	for _, layer := range c {
		if v, ok := layer["enabled"]; ok {
			if b, isBool := v.(bool); isBool {
				enabled = b
			}
		}
	}
	return enabled
}

// Layer converts a settings value (a map from YAML, or a typed struct a host's instance source set) into a Config
// layer. nil gives nil.
func Layer(v any) (map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return x, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("plugin settings must be a map: %w", err)
	}
	return m, nil
}

// MustRegister registers plugins on a single app, with no settings layers (each plugin uses its defaults). It
// panics on error, like PocketBase's own MustRegister helpers.
func MustRegister(app core.App, plugins ...Plugin) {
	for _, p := range plugins {
		if err := p.Register(app, nil); err != nil {
			panic(fmt.Errorf("plugin %s: %w", p.Name(), err))
		}
	}
}

// BindJS calls BindJS on every plugin that implements [JSBinder]. Call it from jsvm's OnInit on a single app.
func BindJS(app core.App, vm *goja.Runtime, plugins ...Plugin) {
	for _, p := range plugins {
		if b, ok := p.(JSBinder); ok {
			b.BindJS(app, vm)
		}
	}
}
