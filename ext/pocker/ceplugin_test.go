package pocker

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

type fakeCEPlugin struct {
	got   map[string]int // instance id -> decoded daily
	bound map[string]bool
}

type fakeSettings struct {
	Daily int `json:"daily"`
}

func (f *fakeCEPlugin) Name() string { return "fake" }

func (f *fakeCEPlugin) Register(app core.App, cfg ceplugin.Config) error {
	s := fakeSettings{Daily: 1} // the plugin's own default
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	f.got[app.Settings().Meta.AppName] = s.Daily
	return nil
}

func (f *fakeCEPlugin) BindJS(app core.App, _ *goja.Runtime) {
	f.bound[app.Settings().Meta.AppName] = true
}

func TestHostedCEPlugin(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("fake:\n  daily: 100\n"), &doc); err != nil {
		t.Fatal(err)
	}
	f := &fakeCEPlugin{got: map[string]int{}, bound: map[string]bool{}}
	h, err := hostCEPlugin(f, &doc)
	if err != nil {
		t.Fatal(err)
	}

	run := func(inst Instance) {
		t.Helper()
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
		app.Settings().Meta.AppName = inst.ID
		if err := h.InitApp(inst, app); err != nil {
			t.Fatal(err)
		}
		h.InitJS(inst, app, goja.New())
	}

	run(Instance{ID: "process"})                                                                // config file only
	run(Instance{ID: "own", Plugins: map[string]any{"fake": map[string]any{"daily": 7}}})       // instance wins
	run(Instance{ID: "typed", Plugins: map[string]any{"fake": &fakeSettings{Daily: 3}}})        // a host's typed value
	run(Instance{ID: "off", Plugins: map[string]any{"fake": map[string]any{"enabled": false}}}) // turned off

	want := map[string]int{"process": 100, "own": 7, "typed": 3}
	for id, d := range want {
		if f.got[id] != d || !f.bound[id] {
			t.Fatalf("%s: daily %d bound %v, want %d", id, f.got[id], f.bound[id], d)
		}
	}
	if _, ok := f.got["off"]; ok || f.bound["off"] {
		t.Fatal("enabled: false still registered or bound")
	}
}
