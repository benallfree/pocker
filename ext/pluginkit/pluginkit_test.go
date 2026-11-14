package pluginkit

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

func TestConfigLayers(t *testing.T) {
	type settings struct {
		Rate  *float64 `json:"rate"`
		Daily *int     `json:"daily"`
		Name  string   `json:"name"`
	}
	out := settings{Name: "default"}
	cfg := Config{
		{"rate": 9, "daily": 100}, // process
		nil,                       // no instance settings
		{"daily": 0},              // instance
	}
	if err := cfg.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Rate == nil || *out.Rate != 9 || out.Daily == nil || *out.Daily != 0 || out.Name != "default" {
		t.Fatalf("got %+v", out)
	}

	if !(Config{}).Enabled() || !(Config{{"rate": 1}}).Enabled() {
		t.Fatal("on unless a layer says enabled: false")
	}
	if (Config{{"enabled": true}, {"enabled": false}}).Enabled() {
		t.Fatal("the last layer wins")
	}
	if !(Config{{"enabled": false}, {"enabled": true}}).Enabled() {
		t.Fatal("an instance can turn on what the process turned off")
	}
}

func TestLayer(t *testing.T) {
	type typed struct {
		Daily int `json:"daily"`
	}
	m, err := Layer(&typed{Daily: 3})
	if err != nil || m["daily"] != float64(3) {
		t.Fatalf("got %v %v", m, err)
	}
	if m, _ := Layer(nil); m != nil {
		t.Fatal("nil")
	}
	if _, err := Layer("nope"); err == nil {
		t.Fatal("a non-map value must fail")
	}
}

func TestAppID(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if got := AppID(app); got != "" {
		t.Fatalf("unset AppID = %q, want empty", got)
	}
	SetAppID(app, "blog")
	if got := AppID(app); got != "blog" {
		t.Fatalf("AppID = %q, want blog", got)
	}
}

func TestDuration(t *testing.T) {
	var out struct {
		A *Duration `json:"a"`
		B Duration  `json:"b"`
	}
	if err := (Config{{"a": "72h", "b": float64(5)}}).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.A == nil || *out.A != Duration(72*3600*1e9) || out.B != 5 {
		t.Fatalf("got %v %v", out.A, out.B)
	}
	if err := (Config{{"a": "soon"}}).Decode(&out); err == nil {
		t.Fatal("bad duration accepted")
	}
}
