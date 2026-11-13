package pocker

import (
	"context"
	"encoding/json"
	"github.com/pockethost/pocketone/ext/sandbox"
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
	base := sandbox.LiveHeap()
	var mu sync.Mutex
	var struck []string
	watch := &sandbox.MemoryWatch{
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
	if after := sandbox.LiveHeap(); after > base+200<<20 {
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

func TestQuarantineBackoff(t *testing.T) {
	m := newTestManager(t, false,
		Instance{ID: "hog", Hosts: []string{"hog.example.com"}},
		Instance{ID: "trusted", Limits: Limits{Quarantine: &QuarantinePolicy{Off: true}}},
		Instance{ID: "lenient", Limits: Limits{Quarantine: &QuarantinePolicy{Strikes: 5}}},
	)
	var events []Event
	m.onEvent = func(e Event) { events = append(events, e) }

	pauseAfter := func(id string, strikes int) time.Duration {
		t.Helper()
		for i := 0; i < strikes; i++ {
			m.Strike(id)
		}
		until, paused := m.pausedUntil(id)
		if !paused {
			return 0
		}
		m.mu.Lock()
		delete(m.paused, id) // end the pause for the next round
		m.mu.Unlock()
		return time.Until(until).Round(time.Second)
	}

	if d := pauseAfter("hog", 2); d != 0 {
		t.Fatal("two strikes must not pause")
	}
	if d := pauseAfter("hog", 1); d != 30*time.Second {
		t.Fatalf("first pause: got %s, want 30s", d)
	}
	if d := pauseAfter("hog", 3); d != time.Minute {
		t.Fatalf("second pause: got %s, want 1m (backoff)", d)
	}
	// a quiet hour resets the backoff
	m.mu.Lock()
	m.qstate["hog"].lastStrike = time.Now().Add(-2 * time.Hour)
	m.mu.Unlock()
	if d := pauseAfter("hog", 3); d != 30*time.Second {
		t.Fatalf("after a quiet hour: got %s, want 30s", d)
	}

	if d := pauseAfter("trusted", 10); d != 0 {
		t.Fatal("quarantine: off must never pause")
	}
	if d := pauseAfter("lenient", 4); d != 0 {
		t.Fatal("per-instance strikes: 5 must not pause after 4")
	}
	if d := pauseAfter("lenient", 1); d == 0 {
		t.Fatal("per-instance strikes: 5 must pause on the 5th")
	}

	// a paused instance answers 503 with Retry-After
	m.Strike("hog")
	m.Strike("hog")
	m.Strike("hog")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://hog.example.com/api/health", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("paused instance: got %d (Retry-After %q)", w.Code, w.Header().Get("Retry-After"))
	}

	var strikes, pauses int
	for _, e := range events {
		switch e.Kind {
		case EventStrike:
			strikes++
		case EventPaused:
			pauses++
			if e.Until.IsZero() {
				t.Error("pause event without Until")
			}
		}
	}
	if strikes == 0 || pauses != 5 {
		t.Errorf("events: %d strikes, %d pauses (want 5 pauses)", strikes, pauses)
	}
}
