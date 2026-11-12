package pocker

import (
	"context"

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

// DecodePluginConfig fills out from an instance's plugin settings: a value of out's own type (or a pointer to it,
// as a host application's instance source would set) is copied; anything else (e.g. a map from YAML) is converted
// through YAML. A nil value leaves out unchanged.
func DecodePluginConfig[T any](v any, out *T) error {
	switch x := v.(type) {
	case nil:
		return nil
	case T:
		*out = x
		return nil
	case *T:
		if x != nil {
			*out = *x
		}
		return nil
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

// TopLevel returns the value node for a top-level key of a config document, or nil.
func TopLevel(doc *yaml.Node, key string) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
