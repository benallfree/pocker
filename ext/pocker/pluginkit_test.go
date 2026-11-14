package pocker

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/pluginkit"
)

type fakePlugin struct {
	got   map[string]int // instance id -> decoded daily
	bound map[string]bool
}

type fakeSettings struct {
	Daily int `json:"daily"`
}

func (f *fakePlugin) Name() string { return "fake" }

func (f *fakePlugin) Register(app core.App, cfg pluginkit.Config) error {
	s := fakeSettings{Daily: 1} // the plugin's own default
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	f.got[app.Settings().Meta.AppName] = s.Daily
	return nil
}

func (f *fakePlugin) BindJS(app core.App, _ *goja.Runtime) {
	f.bound[app.Settings().Meta.AppName] = true
}

func TestHostedPlugin(t *testing.T) {
	f := &fakePlugin{got: map[string]int{}, bound: map[string]bool{}}
	h := hostPlugin(f)

	run := func(inst Instance) {
		t.Helper()
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
		app.Settings().Meta.AppName = inst.ID
		if err := h.InitApp(inst, app); err != nil {
			t.Fatal(err)
		}
		h.InitJS(inst, app, goja.New())
	}

	run(Instance{ID: "plain"})                                                                  // the plugin's own defaults
	run(Instance{ID: "own", Plugins: map[string]any{"fake": map[string]any{"daily": 7}}})       // instance wins
	run(Instance{ID: "typed", Plugins: map[string]any{"fake": &fakeSettings{Daily: 3}}})        // a host's typed value
	run(Instance{ID: "off", Plugins: map[string]any{"fake": map[string]any{"enabled": false}}}) // turned off

	want := map[string]int{"plain": 1, "own": 7, "typed": 3}
	for id, d := range want {
		if f.got[id] != d || !f.bound[id] {
			t.Fatalf("%s: daily %d bound %v, want %d", id, f.got[id], f.bound[id], d)
		}
	}
	if _, ok := f.got["off"]; ok || f.bound["off"] {
		t.Fatal("enabled: false still registered or bound")
	}
}

// Plugins read their settings at Register, so a change to an instance's plugin settings restarts its app.
func TestPluginSettingsChangeRestarts(t *testing.T) {
	a := Instance{ID: "a", Plugins: map[string]any{"fake": map[string]any{"daily": 7}}}
	b := a
	b.Plugins = map[string]any{"fake": map[string]any{"daily": 8}}
	if !sameRuntime(a, a) {
		t.Fatal("identical instances must share an app")
	}
	if sameRuntime(a, b) {
		t.Fatal("changed plugin settings must restart the app")
	}
}
