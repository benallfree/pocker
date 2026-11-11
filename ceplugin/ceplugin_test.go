package ceplugin

import "testing"

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
