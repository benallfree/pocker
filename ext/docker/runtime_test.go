package docker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

// dockerSocket finds a reachable Docker daemon, or skips the test.
func dockerSocket(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker (skipped with -short)")
	}
	home, _ := os.UserHomeDir()
	for _, s := range []string{DefaultSocket(), filepath.Join(home, ".docker/run/docker.sock")} {
		e := newEngine(s)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := e.call(ctx, 0, http.MethodGet, "/_ping", nil, nil)
		cancel()
		if err == nil {
			return s
		}
	}
	t.Skip("no Docker daemon reachable")
	return ""
}

// testBinaries is a cache of real PocketBase releases for the daemon's platform, kept between test runs.
func testBinaries(t *testing.T) *Binaries {
	t.Helper()
	m, err := LoadManifest("testdata/versions.json")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := os.UserCacheDir()
	return &Binaries{Root: filepath.Join(dir, "ce-docker-test", "pocketbase"), Manifest: m}
}

func get(t *testing.T, rt *Runtime, inst host.Instance, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://"+inst.ID+".test"+path, nil)
	rt.ServeInstance(w, r, inst)
	return w.Code, w.Body.String()
}

func TestRuntime(t *testing.T) {
	socket := dockerSocket(t)
	data := t.TempDir()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("CE_DOCKER_DEBUG") != "" {
		quiet = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	cfg := Config{Socket: socket, Image: "alpine:3.20", DataRoot: data, Binaries: testBinaries(t),
		MaxConcurrentStarts: 1, NofileSoft: 4096, NofileHard: 8192, PidsLimit: 256, Logger: quiet,
		Prepare: func(_ context.Context, inst host.Instance, version string, spec *Spec) error {
			spec.Env["PH_APP_NAME"] = inst.ID
			spec.Env["PB_VERSION_SEEN"] = version
			return nil
		}}
	rt, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		for _, id := range []string{"cedockera", "cedockerb"} {
			_ = rt.e.remove(context.Background(), id)
		}
	})
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// instance a (upstream 0.23): a hook that reads env, as customers' hooks do; b runs a PocketOne release
	a := host.Instance{ID: "cedockera", Version: "pb:0.23.*", Secrets: map[string]string{"FOO": "bar"}}
	b := host.Instance{ID: "cedockerb", Version: "p1:0.0.*"}
	if err := os.MkdirAll(filepath.Join(data, a.ID, "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := `routerAdd("GET", "/env", (e) => e.string(200, $os.getenv("FOO") + "|" + $os.getenv("PH_APP_NAME") + "|" + $os.getenv("PB_VERSION_SEEN")))`
	if err := os.WriteFile(filepath.Join(data, a.ID, "pb_hooks", "env.pb.js"), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}

	// both start at once through a 1-slot start queue
	var wg sync.WaitGroup
	codes := map[string]int{}
	var mu sync.Mutex
	for _, inst := range []host.Instance{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := get(t, rt, inst, "/api/health")
			mu.Lock()
			codes[inst.ID] = code
			mu.Unlock()
			if code != 200 {
				t.Errorf("%s health: %d %s", inst.ID, code, body)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	if code, body := get(t, rt, a, "/env"); code != 200 || body != "bar|cedockera|pb:0.23.12" {
		t.Fatalf("a /env: %d %q", code, body)
	}
	if rt.RunningCount() != 2 {
		t.Fatalf("running: %d", rt.RunningCount())
	}
	list, _ := rt.e.list(ctx, map[string][]string{"label": {LabelRuntime + "=docker"}})
	versions := map[string]string{}
	for _, s := range list {
		versions[s.Labels[LabelInstance]] = s.Labels[LabelVersion]
	}
	if versions["cedockera"] != "pb:0.23.12" || versions["cedockerb"] != "p1:0.0.1" {
		t.Fatalf("labels: %v", versions)
	}
	if _, err := os.Stat(filepath.Join(data, a.ID, "pb_data", "data.db")); err != nil {
		t.Fatalf("instance data not in its folder: %v", err)
	}

	// a changed secret restarts it on its next request; an unrelated change doesn't
	before := rt.get(a.ID).container
	a2 := a
	a2.Secrets = map[string]string{"FOO": "baz"}
	rt.Sync(host.Change{Kind: host.Upsert, Instance: a2}, &a)
	if code, body := get(t, rt, a2, "/env"); code != 200 || !strings.HasPrefix(body, "baz|") {
		t.Fatalf("after secret change: %d %q", code, body)
	}
	if rt.get(a.ID).container == before {
		t.Fatal("not restarted after a secret change")
	}
	before = rt.get(a.ID).container
	a3 := a2
	a3.Hosts = []string{"new.example.test"} // routing only: the container is the same
	rt.Sync(host.Change{Kind: host.Upsert, Instance: a3}, &a2)
	if code, _ := get(t, rt, a3, "/api/health"); code != 200 || rt.get(a.ID).container != before {
		t.Fatal("restarted for a change that doesn't touch the container")
	}

	// a new edge process adopts the running containers instead of restarting them
	rt.Shutdown()
	cfg.IdleTTL = 2 * time.Second
	rt2, _ := New(cfg)
	if err := rt2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if rt2.RunningCount() != 2 || !rt2.Ready() {
		t.Fatalf("adopted: %d", rt2.RunningCount())
	}
	if code, body := get(t, rt2, a3, "/env"); code != 200 || !strings.HasPrefix(body, "baz|") || rt2.get(a.ID).container != before {
		t.Fatalf("adopted a: %d %q (restarted: %v)", code, body, rt2.get(a.ID).container != before)
	}

	// idle: both stop after IdleTTL without requests; the next request starts it again
	deadline := time.Now().Add(15 * time.Second)
	for rt2.RunningCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("still running after idle: %d", rt2.RunningCount())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code, _ := get(t, rt2, b, "/api/health"); code != 200 {
		t.Fatal("no wake after idle")
	}

	// deleted: stopped
	rt2.Sync(host.Change{Kind: host.Delete, Instance: host.Instance{ID: b.ID}}, &b)
	deadline = time.Now().Add(20 * time.Second)
	for rt2.Running(b.ID) {
		if time.Now().After(deadline) {
			t.Fatal("deleted instance still running")
		}
		time.Sleep(200 * time.Millisecond)
	}
	rt2.Shutdown()
}

// A container that can't start (no such version) answers 503 with the reason.
func TestRuntimeBadVersion(t *testing.T) {
	socket := dockerSocket(t)
	rt, _ := New(Config{Socket: socket, Image: "alpine:3.20", DataRoot: t.TempDir(), Binaries: testBinaries(t),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Shutdown()
	if code, body := get(t, rt, host.Instance{ID: "cedockerx", Version: "0.99.*"}, "/"); code != 503 || !strings.Contains(body, "no PocketBase version") {
		t.Fatalf("%d %q", code, body)
	}
	if code, body := get(t, rt, host.Instance{ID: "cedockery", Version: "0.22.*"}, "/"); code != 503 || !strings.Contains(body, "no longer supported") {
		t.Fatalf("sunset version: %d %q", code, body)
	}
}
