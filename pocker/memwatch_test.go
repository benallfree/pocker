package pocker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestMemoryWatchStopsGradualGrowth(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "hog")
	if err := os.MkdirAll(filepath.Join(dir, "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := `
routerAdd("GET", "/grow", (e) => {
  const keep = []
  while (true) { keep.push("x".repeat(1 << 20) + keep.length) } // 1 MiB at a time, each under the one-shot cap
})
routerAdd("GET", "/ok", (e) => e.json(200, { ok: true }))
`
	if err := os.WriteFile(filepath.Join(dir, "pb_hooks/main.pb.js"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	running, err := startApp(appOptions{PBRoot: root}, Instance{ID: "hog"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(running)

	runtime.GC()
	base := liveHeap()
	var mu sync.Mutex
	var struck []string
	watch := &MemoryWatch{
		SoftLimit: base + 150<<20,
		Interval:  10 * time.Millisecond,
		MinRunAge: 50 * time.Millisecond,
		OnStrike: func(id string) {
			mu.Lock()
			struck = append(struck, id)
			mu.Unlock()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watch.Run(ctx)

	start := time.Now()
	w := httptest.NewRecorder()
	running.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/grow", nil))
	took := time.Since(start)
	if w.Code == http.StatusOK || took > 10*time.Second {
		t.Fatalf("expected the growing hook to be interrupted, got status=%d after %s", w.Code, took)
	}
	mu.Lock()
	gotStrike := len(struck) > 0 && struck[0] == "hog"
	mu.Unlock()
	if !gotStrike {
		t.Fatalf("expected a strike for hog, got %v", struck)
	}

	// memory is released and the instance still answers
	runtime.GC()
	if after := liveHeap(); after > base+200<<20 {
		t.Errorf("heap not released: base %d MiB, after %d MiB", base>>20, after>>20)
	}
	w = httptest.NewRecorder()
	running.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != 200 || res["ok"] != true {
		t.Errorf("instance unhealthy after interrupt: %d %s", w.Code, w.Body.String())
	}
	t.Logf("interrupted after %s; heap base %d MiB", took, base>>20)
}

func TestQuarantineAfterStrikes(t *testing.T) {
	m := newTestManager(t, false, Instance{ID: "hog", Hosts: []string{"hog.example.com"}})
	m.quarantine = quarantineOptions{Strikes: 3, Window: time.Minute, Pause: time.Minute}

	m.Strike("hog")
	m.Strike("hog")
	if _, paused := m.pausedUntil("hog"); paused {
		t.Fatal("two strikes must not pause")
	}
	m.Strike("hog")
	if _, paused := m.pausedUntil("hog"); !paused {
		t.Fatal("three strikes within the window must pause")
	}

	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://hog.example.com/api/health", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("paused instance: got %d (Retry-After %q)", w.Code, w.Header().Get("Retry-After"))
	}

	m.mu.Lock()
	m.paused["hog"] = time.Now().Add(-time.Second)
	m.mu.Unlock()
	if _, paused := m.pausedUntil("hog"); paused {
		t.Fatal("pause must end")
	}
}
