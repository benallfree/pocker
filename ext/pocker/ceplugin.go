package pocker

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/ceplugin"
	"gopkg.in/yaml.v3"
)

// hostedPlugin runs a PocketOne plugin ([ceplugin.Plugin], e.g. bulkmail.New(...)) on every instance: put the
// plugin itself in [Server.Plugins], no pocker-specific wrapper needed.
//
// Settings: the config file's top-level `<name>:` key, then the instance's `plugins.<name>`, over the defaults the
// plugin was built with. `enabled: false` in either turns it off (the instance wins); off means Register and
// BindJS are never called for that instance.
type hostedPlugin struct {
	p       ceplugin.Plugin
	process map[string]any
}

func hostCEPlugin(p ceplugin.Plugin, doc *yaml.Node) (*hostedPlugin, error) {
	h := &hostedPlugin{p: p}
	if n := TopLevel(doc, p.Name()); n != nil {
		if err := n.Decode(&h.process); err != nil {
			return nil, fmt.Errorf("top-level %q: %w", p.Name(), err)
		}
	}
	return h, nil
}

func (h *hostedPlugin) config(inst Instance) (ceplugin.Config, error) {
	own, err := ceplugin.Layer(inst.PluginConfig(h.p.Name()))
	if err != nil {
		return nil, fmt.Errorf("instance %s: plugins.%s: %w", inst.ID, h.p.Name(), err)
	}
	return ceplugin.Config{h.process, own}, nil
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
	if err := h.p.Register(app, cfg); err != nil {
		return fmt.Errorf("plugin %s: instance %s: %w", h.p.Name(), inst.ID, err)
	}
	return nil
}

// InitJS implements [JSIniter].
func (h *hostedPlugin) InitJS(inst Instance, app core.App, vm *goja.Runtime) {
	b, ok := h.p.(ceplugin.JSBinder)
	if !ok {
		return
	}
	if cfg, err := h.config(inst); err == nil && cfg.Enabled() {
		b.BindJS(app, vm)
	}
}
