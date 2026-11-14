package pocker

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/pluginkit"
)

// hostedPlugin runs a PocketOne plugin ([pluginkit.Plugin], e.g. bulkmail.New(...)) on every instance: put the
// plugin itself in [Server.Plugins], no pocker-specific wrapper needed.
//
// Settings: the instance's `plugins.<name>` over the defaults the plugin was built with (process-wide settings are
// the plugin's own, set in Go). `enabled: false` turns it off for that instance: Register and BindJS are never
// called.
type hostedPlugin struct {
	p pluginkit.Plugin
}

func hostPlugin(p pluginkit.Plugin) *hostedPlugin { return &hostedPlugin{p: p} }

func (h *hostedPlugin) config(inst Instance) (pluginkit.Config, error) {
	own, err := pluginkit.Layer(inst.PluginConfig(h.p.Name()))
	if err != nil {
		return nil, fmt.Errorf("instance %s: plugins.%s: %w", inst.ID, h.p.Name(), err)
	}
	return pluginkit.Config{own}, nil
}

// InitApp implements [AppIniter].
func (h *hostedPlugin) InitApp(inst Instance, app core.App) error {
	cfg, err := h.config(inst)
	if err != nil {
		return err
	}
	if !cfg.Enabled() {
		return nil
	}
	pluginkit.SetAppID(app, inst.ID)
	if err := h.p.Register(app, cfg); err != nil {
		return fmt.Errorf("plugin %s: instance %s: %w", h.p.Name(), inst.ID, err)
	}
	return nil
}

// InitJS implements [JSIniter].
func (h *hostedPlugin) InitJS(inst Instance, app core.App, vm *goja.Runtime) {
	b, ok := h.p.(pluginkit.JSBinder)
	if !ok {
		return
	}
	if cfg, err := h.config(inst); err == nil && cfg.Enabled() {
		b.BindJS(app, vm)
	}
}

// InitPlugins returns a ManagerOptions.Init that registers PocketOne plugins on each instance the way [Server] does,
// with each instance's `plugins.<name>` settings (there's no config file, so no process layer). For a Manager built
// by hand, e.g. in tests.
func InitPlugins(plugins ...pluginkit.Plugin) func(Instance, core.App) error {
	hosted := make([]*hostedPlugin, len(plugins))
	for i, p := range plugins {
		hosted[i] = &hostedPlugin{p: p}
	}
	return func(inst Instance, app core.App) error {
		for _, h := range hosted {
			if err := h.InitApp(inst, app); err != nil {
				return err
			}
		}
		return nil
	}
}
