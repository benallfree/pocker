package pages

import (
	"fmt"

	"github.com/dop251/goja"
)

// builtinPlugins are the plugins turned on by name in +config.js (SPEC 9.1).
var builtinPlugins = map[string]bool{"auth": true, "realtime": true}

// pagesConfig is +config.js.
type pagesConfig struct {
	Debug    bool
	Builtins map[string]bool
}

// loadConfig runs +config.js (if any), validates it and returns the config and the JS plugin objects.
func (r *request) loadConfig(file string) (pagesConfig, []goja.Value, error) {
	var cfg pagesConfig
	if file == "" {
		return cfg, nil, nil
	}
	v := r.load(file)
	obj, ok := v.(*goja.Object)
	if !ok || goja.IsUndefined(v) || goja.IsNull(v) {
		return cfg, nil, fmt.Errorf("%s: must export an object", file)
	}
	var plugins []goja.Value
	seen := map[string]bool{}
	for _, key := range obj.Keys() {
		switch key {
		case "debug":
			cfg.Debug = obj.Get(key).ToBoolean()
		case "plugins":
			list, ok := obj.Get(key).Export().([]any)
			if !ok && !goja.IsUndefined(obj.Get(key)) {
				return cfg, nil, fmt.Errorf("%s: plugins must be an array", file)
			}
			arr := obj.Get(key).ToObject(r.vm)
			for i := range list {
				item := arr.Get(fmt.Sprint(i))
				if name, ok := item.Export().(string); ok {
					if !builtinPlugins[name] {
						return cfg, nil, fmt.Errorf("%s: unknown plugin %q", file, name)
					}
					if seen[name] {
						return cfg, nil, fmt.Errorf("%s: plugin %q listed twice", file, name)
					}
					seen[name] = true
					if cfg.Builtins == nil {
						cfg.Builtins = map[string]bool{}
					}
					cfg.Builtins[name] = true
					continue
				}
				factory, options := item, r.vm.NewObject()
				if o, ok := item.(*goja.Object); ok {
					if f := o.Get("factory"); f != nil && !goja.IsUndefined(f) {
						factory = f
						for _, k := range o.Keys() {
							if k != "factory" {
								_ = options.Set(k, o.Get(k))
							}
						}
					}
				}
				fn, ok := goja.AssertFunction(factory)
				if !ok {
					return cfg, nil, fmt.Errorf("%s: plugins[%d] must be a built-in name or a plugin factory", file, i)
				}
				p, err := fn(goja.Undefined(), options)
				if err != nil {
					return cfg, nil, err
				}
				po, ok := p.(*goja.Object)
				if !ok {
					return cfg, nil, fmt.Errorf("%s: plugins[%d]: the factory must return an object", file, i)
				}
				name := po.Get("name")
				if name == nil || goja.IsUndefined(name) || name.String() == "" {
					return cfg, nil, fmt.Errorf("%s: plugins[%d]: a plugin needs a name", file, i)
				}
				if seen[name.String()] {
					return cfg, nil, fmt.Errorf("%s: plugin %q listed twice", file, name.String())
				}
				seen[name.String()] = true
				plugins = append(plugins, po)
			}
		default:
			return cfg, nil, fmt.Errorf("%s: unknown key %q", file, key)
		}
	}
	return cfg, plugins, nil
}
