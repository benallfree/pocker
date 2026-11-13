package pocker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLimitsYAMLAndMerge(t *testing.T) {
	var f struct {
		Instances []Instance `yaml:"instances"`
	}
	err := yaml.Unmarshal([]byte(`
instances:
  - id: shop
    hosts: [shop.example.com]
    limits:
      max_js_execution: 60s
      max_js_allocation: 128MiB
      http_send_max_body: 1.5MB
      quarantine: { strikes: 5, max_pause: 1h }
  - id: mine
    limits:
      quarantine: off
`), &f)
	if err != nil {
		t.Fatal(err)
	}
	shop := f.Instances[0].Limits.Over(DefaultLimits())
	if shop.MaxJSExecution != time.Minute || shop.MaxJSAllocation != 128<<20 || shop.HTTPSendMaxBody != 1500000 {
		t.Fatalf("shop overrides: %+v", shop)
	}
	if shop.MaxQueryDuration != 30*time.Second || shop.MaxSleep != 10*time.Second {
		t.Fatalf("shop defaults: %+v", shop)
	}
	if q := shop.Quarantine; q.Off || q.Strikes != 5 || q.MaxPause != time.Hour || q.FirstPause != 30*time.Second {
		t.Fatalf("shop quarantine: %+v", *q)
	}
	if q := f.Instances[1].Limits.Over(DefaultLimits()).Quarantine; !q.Off {
		t.Fatalf("quarantine: off not applied: %+v", *q)
	}

	// process-wide config overrides the built-in defaults; instances override the process
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pocker.yaml")
	if err := os.WriteFile(cfgPath, []byte("max_js_execution: 5s\nquarantine: { first_pause: 1m }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxJSExecution != 5*time.Second || cfg.Quarantine.FirstPause != time.Minute || cfg.Quarantine.Strikes != 3 {
		t.Fatalf("config limits: %+v / %+v", cfg.Limits, *cfg.Quarantine)
	}
	if got := shop.Over(cfg.Limits).MaxJSExecution; got != time.Minute {
		t.Fatalf("instance must win over config, got %s", got)
	}
}

func TestPerInstanceJSTimeLimit(t *testing.T) {
	root := t.TempDir()
	hooks := `routerAdd("GET", "/spin", (e) => { const s = Date.now(); while (Date.now() - s < 3000) {} return e.json(200, {}) })`
	for _, id := range []string{"strict", "relaxed"} {
		if err := os.MkdirAll(filepath.Join(root, id, "pb_hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, id, "pb_hooks/main.pb.js"), []byte(hooks), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := appOptions{PBRoot: root, Defaults: DefaultLimits()}
	strict, err := startApp(opts, Instance{ID: "strict", Limits: Limits{MaxJSExecution: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(strict)
	relaxed, err := startApp(opts, Instance{ID: "relaxed"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(relaxed)

	w := httptest.NewRecorder()
	strict.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/spin", nil))
	if w.Code == http.StatusOK {
		t.Error("strict (1s) should have been interrupted")
	}
	w = httptest.NewRecorder()
	relaxed.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/spin", nil))
	if w.Code != http.StatusOK {
		t.Errorf("relaxed (default 30s) should finish, got %d %s", w.Code, w.Body.String())
	}
}

type chanSource struct {
	list []Instance
	ch   chan Change
}

func (s *chanSource) Snapshot(context.Context) ([]Instance, error) { return s.list, nil }
func (s *chanSource) Watch(context.Context) (<-chan Change, error) { return s.ch, nil }

func TestSecretChangeRestartsApp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := `routerAdd("GET", "/secret", (e) => e.string(200, $os.getenv("API_KEY")))`
	if err := os.WriteFile(filepath.Join(root, "app", "pb_hooks/main.pb.js"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := Instance{ID: "app", Hosts: []string{"app.example.com"}, Secrets: map[string]string{"API_KEY": "old"}}
	src := &chanSource{list: []Instance{inst}, ch: make(chan Change)}
	var restarted []string
	m := NewManager(src, ManagerOptions{
		PBRoot:  root,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnEvent: func(e Event) { restarted = append(restarted, string(e.Kind)) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()

	get := func() string {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://app.example.com/secret", nil))
		return strings.TrimSpace(w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for get() != "old" {
		if time.Now().After(deadline) {
			t.Fatalf("app never served the old secret: %q", get())
		}
		time.Sleep(20 * time.Millisecond)
	}

	inst.Secrets = map[string]string{"API_KEY": "rotated"}
	src.ch <- Change{Kind: Upsert, Instance: inst}
	time.Sleep(50 * time.Millisecond)
	if got := get(); got != "rotated" {
		t.Fatalf("after rotation: got %q, want rotated", got)
	}
	if !containsString(restarted, string(EventStarted)) || !containsString(restarted, string(EventRestarted)) {
		t.Fatalf("expected started and restarted events, got %v", restarted)
	}
	countRestarts := func() int {
		n := 0
		for _, k := range restarted {
			if k == string(EventRestarted) {
				n++
			}
		}
		return n
	}

	// a hosts-only change keeps the running app
	inst.Hosts = append(inst.Hosts, "www.app.example.com")
	src.ch <- Change{Kind: Upsert, Instance: inst}
	time.Sleep(50 * time.Millisecond)
	if countRestarts() != 1 {
		t.Fatalf("hosts change must not restart, events: %v", restarted)
	}
}

func TestWithInstanceStopped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(v string) {
		_ = os.WriteFile(filepath.Join(root, "app", "pb_hooks/main.pb.js"), []byte(`routerAdd("GET", "/v", (e) => e.string(200, "`+v+`"))`), 0o644)
	}
	write("v1")
	inst := Instance{ID: "app", Hosts: []string{"app.example.com"}}
	m := NewManager(staticSource{inst}, ManagerOptions{PBRoot: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	get := func() string {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://app.example.com/v", nil))
		return strings.TrimSpace(w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for get() != "v1" {
		if time.Now().After(deadline) {
			t.Fatal("v1 never served")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// a request that arrives during the update waits, then sees the new files
	got := make(chan string, 1)
	err := m.WithInstanceStopped(ctx, "app", func(dir string) error {
		if dir != filepath.Join(root, "app") {
			t.Errorf("dir: %s", dir)
		}
		go func() { got <- get() }()
		time.Sleep(100 * time.Millisecond)
		write("v2")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := <-got; v != "v2" {
		t.Fatalf("held request saw %q, want v2", v)
	}
	if err := m.WithInstanceStopped(ctx, "nope", func(string) error { return nil }); err == nil {
		t.Fatal("unknown instance must fail")
	}

	// StartInstance reports a broken start right away; Running follows the app
	_ = m.WithInstanceStopped(ctx, "app", func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "pb_hooks/main.pb.js"), []byte(`this is not js (`), 0o644)
	})
	if m.Running("app") {
		t.Fatal("stopped app reported running")
	}
	if err := m.StartInstance(ctx, "app"); err == nil {
		t.Fatal("broken hook must fail to start")
	}
	write("v3")
	if err := m.StartInstance(ctx, "app"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !m.Running("app") || get() != "v3" {
		t.Fatal("app not running v3")
	}
	if err := m.StartInstance(ctx, "nope"); err == nil {
		t.Fatal("unknown instance must fail")
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
