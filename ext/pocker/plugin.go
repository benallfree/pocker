package pocker

import (
	"context"

	"github.com/pockethost/pocketone/ext/host"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

// Plugin extends a Pocker server (see [Server.Plugins]). A plugin implements Name and any of the optional
// interfaces below; the server checks each one with a type assertion.
type Plugin interface {
	// Name identifies the plugin: its key under an instance's `plugins:` block, and in logs.
	Name() string
}

// Configurer reads a plugin's settings. doc is the whole config file (nil when there is none), so a plugin can own
// its own top-level keys; cfg is the parsed core config. Configure runs once, before the server starts.
type Configurer interface {
	Configure(doc *yaml.Node, cfg Config) error
}

// AppIniter sets up each instance's app right after it is created, before its JS hooks load and before it
// bootstraps (opens its databases): the place to bind OnBootstrap, OnServe and OnTerminate handlers (e.g. the pitr
// plugin). It runs on every start of the app, including restarts. An error fails that start.
type AppIniter interface {
	InitApp(inst Instance, app core.App) error
}

// JSIniter adds globals to each JS runtime of an instance (hooks, routes, migrations), after the sandbox has set
// the runtime up, e.g. a plugin exposing its Go API as $name. What it exposes is a sandbox decision: only expose
// what is safe for a hostile tenant to call on its own app.
type JSIniter interface {
	InitJS(inst Instance, app core.App, vm *goja.Runtime)
}

func chainJSIniters(in []JSIniter) func(Instance, core.App, *goja.Runtime) {
	if len(in) == 0 {
		return nil
	}
	return func(inst Instance, app core.App, vm *goja.Runtime) {
		for _, i := range in {
			i.InitJS(inst, app, vm)
		}
	}
}

func chainIniters(in []AppIniter) func(Instance, core.App) error {
	if len(in) == 0 {
		return nil
	}
	return func(inst Instance, app core.App) error {
		for _, i := range in {
			if err := i.InitApp(inst, app); err != nil {
				return err
			}
		}
		return nil
	}
}

// Starter runs background work once the Manager exists, until ctx is done.
type Starter interface {
	Start(ctx context.Context, m *Manager)
}

// DecodePluginConfig is [host.DecodePluginConfig].
func DecodePluginConfig[T any](v any, out *T) error { return host.DecodePluginConfig(v, out) }

// TopLevel is [host.TopLevel].
func TopLevel(doc *yaml.Node, key string) *yaml.Node { return host.TopLevel(doc, key) }
