package conformance_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
)

// plugin is a configurable test plugin: each field turns on one way of breaking the rules.
type plugin struct {
	name       string
	collection string // created on bootstrap
	touchUsers bool   // edits PocketBase's own users collection
	leak       bool   // starts a goroutine that never stops
}

func (p *plugin) Name() string { return p.name }

func (p *plugin) Register(app core.App, _ pluginkit.Config) error {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { return e.Next() })
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if p.collection != "" {
			c := core.NewBaseCollection(p.collection)
			if err := e.App.Save(c); err != nil {
				return err
			}
		}
		if p.touchUsers {
			users, err := e.App.FindCollectionByNameOrId("users")
			if err != nil {
				return err
			}
			users.ListRule = nil
			users.Fields.Add(&core.TextField{Name: "nickname"})
			if err := e.App.Save(users); err != nil {
				return err
			}
		}
		return nil
	})
	if p.leak {
		go func() { select {} }()
	}
	return nil
}

// sticky is a plugin that keeps the first app it sees and binds every later app's hooks there.
type sticky struct {
	mu    sync.Mutex
	first core.App
}

func (s *sticky) Name() string { return "sticky" }
func (s *sticky) Register(app core.App, _ pluginkit.Config) error {
	s.mu.Lock()
	if s.first == nil {
		s.first = app
	}
	target := s.first
	s.mu.Unlock()
	target.OnServe().BindFunc(func(e *core.ServeEvent) error { return e.Next() })
	return nil
}

// recorder is a conformance.TB that records failures instead of failing the real test.
type recorder struct {
	t      *testing.T
	errors []string
}

type fatal struct{}

func (r *recorder) Helper()                   {}
func (r *recorder) Errorf(f string, a ...any) { r.errors = append(r.errors, fmt.Sprintf(f, a...)) }
func (r *recorder) Fatalf(f string, a ...any) { r.Errorf(f, a...); panic(fatal{}) }
func (r *recorder) Fatal(a ...any)            { r.errors = append(r.errors, fmt.Sprint(a...)); panic(fatal{}) }
func (r *recorder) Failed() bool              { return len(r.errors) > 0 }
func (r *recorder) TempDir() string           { return r.t.TempDir() }
func (r *recorder) has(sub string) bool       { return strings.Contains(strings.Join(r.errors, "\n"), sub) }

func check(t *testing.T, fn func(conformance.TB)) *recorder {
	r := &recorder{t: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatal); !ok {
					panic(v)
				}
			}
		}()
		fn(r)
	}()
	return r
}

func TestGoodPluginPasses(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return &plugin{name: "good", collection: "_good_items"} })
}

func TestBadNamesFail(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckCollections(tb, &plugin{name: "bad", collection: "items"}, conformance.Options{})
	})
	if !r.has(`collection "items"`) {
		t.Fatalf("unprefixed collection not reported: %v", r.errors)
	}
}

func TestAllowedCollectionPasses(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckCollections(tb, &plugin{name: "bad", collection: "items"},
			conformance.Options{AllowCollections: map[string]string{"items": "the app's users read it"}})
	})
	if r.Failed() {
		t.Fatalf("allowed collection reported: %v", r.errors)
	}
}

func TestChangingCoreCollectionsFails(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckCollections(tb, &plugin{name: "meddler", touchUsers: true}, conformance.Options{})
	})
	if !r.has(`collection "users" was changed`) {
		t.Fatalf("change to users not reported: %v", r.errors)
	}
}

func TestLeakFails(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckLifecycle(tb, &plugin{name: "leaky", leak: true}, conformance.Options{})
	})
	if !r.has("outlived the app") {
		t.Fatalf("leaked goroutine not reported: %v", r.errors)
	}
	time.Sleep(0) // the leaked goroutine stays parked for the rest of the test binary; harmless
}

func TestCrossAppHooksFail(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckManyApps(tb, &sticky{}, conformance.Options{})
	})
	if !r.has("to app 0's OnServe") {
		t.Fatalf("hooks bound on another app not reported: %v", r.errors)
	}
}

// fakeMCP stands in for ext/mcp's server: Setup puts one on each app, and toolPlugin adds its tools to it on serve.
type fakeMCP struct {
	mu    sync.Mutex
	tools []pluginkit.ToolInfo
}

func (f *fakeMCP) ToolInfos() []pluginkit.ToolInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pluginkit.ToolInfo(nil), f.tools...)
}

func withMCP(app core.App) error {
	f := &fakeMCP{tools: []pluginkit.ToolInfo{{Name: "pb_list_collections", Description: "built in"}}}
	app.Store().Set("fakemcp", f)
	pluginkit.SetToolLister(app, f)
	return nil
}

type toolPlugin struct {
	name  string
	tools []pluginkit.ToolInfo
}

func (p *toolPlugin) Name() string { return p.name }
func (p *toolPlugin) Register(app core.App, _ pluginkit.Config) error {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		f := e.App.Store().Get("fakemcp").(*fakeMCP)
		f.mu.Lock()
		f.tools = append(f.tools, p.tools...)
		f.mu.Unlock()
		return e.Next()
	})
	return nil
}

var objectSchema = map[string]any{"type": "object", "properties": map[string]any{}}

func TestGoodToolsPass(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin {
		return &toolPlugin{name: "oauth-server", tools: []pluginkit.ToolInfo{
			{Name: "oauth_server_list_clients", Description: "Lists OAuth clients.", InputSchema: objectSchema},
			{Name: "oauth_server_revoke_client_tokens", Description: "Revokes a client's tokens."},
		}}
	}, conformance.Options{Setup: withMCP})
}

func TestBadToolsFail(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckMCPTools(tb, &toolPlugin{name: "shop", tools: []pluginkit.ToolInfo{
			{Name: "list_orders", Description: "no prefix"},
			{Name: "pb_shop_orders", Description: "PocketBase's prefix"},
			{Name: "shop_orders", Description: "one word after the prefix"},
			{Name: "shop_ListOrders", Description: "not lowercase"},
			{Name: "shop_list_orders"},
			{Name: "shop_get_order", Description: "Gets an order.", InputSchema: map[string]any{"type": "string"}},
		}}, conformance.Options{Setup: withMCP})
	})
	for _, want := range []string{`"list_orders": name it shop_`, `"pb_shop_orders": name it`, `"shop_orders": name it`,
		`"shop_ListOrders": name it`, `"shop_list_orders" has no description`, `"shop_get_order": input schema`} {
		if !r.has(want) {
			t.Errorf("not reported: %s\ngot: %v", want, r.errors)
		}
	}
	if len(r.errors) != 6 {
		t.Errorf("want 6 problems, got %d: %v", len(r.errors), r.errors)
	}
}

func TestToolCheckNeedsAnMCPServer(t *testing.T) {
	r := check(t, func(tb conformance.TB) {
		conformance.CheckMCPTools(tb, &toolPlugin{name: "shop"}, conformance.Options{Setup: func(core.App) error { return nil }})
	})
	if !r.has("put no MCP server") {
		t.Fatalf("missing MCP server not reported: %v", r.errors)
	}
}
