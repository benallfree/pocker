package pockerplugin

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"

	"github.com/pockethost/pocketbase/ext/bulkmail"
	"github.com/pockethost/pocketbase/ext/pocker"
)

func TestPockerPlugin(t *testing.T) {
	p := New(bulkmail.Config{Rate: 3})
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("bulkmail:\n  rate: 9\n  daily: 100\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if err := p.Configure(&doc, pocker.Config{}); err != nil {
		t.Fatal(err)
	}
	if p.cfg.Rate != 3 || p.cfg.Daily != 100 {
		t.Fatalf("Go fields win, file fills the rest: %+v", p.cfg)
	}

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	if err := p.InitApp(pocker.Instance{ID: "i1", Plugins: map[string]any{Name: map[string]any{"daily": 0}}}, app); err != nil {
		t.Fatal(err)
	}
	bm := bulkmail.For(app)
	if bm == nil {
		t.Fatal("not registered")
	}
	if _, daily, _, _ := bm.Limits(); daily != bulkmail.NoBulkMail {
		t.Fatalf("instance daily: 0 means no bulk mail, got %d", daily)
	}
	vm := goja.New()
	p.InitJS(pocker.Instance{ID: "i1"}, app, vm)
	if v, err := vm.RunString("typeof $bulkmail.start"); err != nil || v.String() != "function" {
		t.Fatalf("$bulkmail: %v %v", v, err)
	}

	off, _ := tests.NewTestApp()
	defer off.Cleanup()
	if err := p.InitApp(pocker.Instance{ID: "i2", Plugins: map[string]any{Name: map[string]any{"enabled": false}}}, off); err != nil {
		t.Fatal(err)
	}
	if bulkmail.For(off) != nil {
		t.Fatal("enabled: false still registered")
	}
}
