package servertiming

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pockethost/pocketbase/ext/pocker"
)

type staticSource []pocker.Instance

func (s staticSource) Snapshot(context.Context) ([]pocker.Instance, error) { return s, nil }
func (s staticSource) Watch(context.Context) (<-chan pocker.Change, error) { return nil, nil }

// timings parses `a;dur=1.0;desc="x", b;dur=2.0` into names and millisecond durations.
func timings(h string) (names []string, durs []float64) {
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ";")
		names = append(names, fields[0])
		d := -1.0
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "dur="); ok {
				d, _ = strconv.ParseFloat(v, 64)
			}
		}
		durs = append(durs, d)
	}
	return
}

func TestFormatTiming(t *testing.T) {
	cases := map[string]TimingEntry{
		`edge;dur=63.5;desc="sfo"`: {Name: "edge", Dur: 63456 * time.Microsecond, Desc: "sfo"},
		`wake;dur=0.0`:             {Name: "wake"},
		`x;dur=0.0;desc="abc"`:     {Name: "x", Dur: -time.Second, Desc: `a"b\c`},
	}
	for want, e := range cases {
		if got := FormatTiming(e); got != want {
			t.Errorf("FormatTiming(%+v) = %q, want %q", e, got, want)
		}
	}
}

func TestAppendServerTimingKeepsUpstream(t *testing.T) {
	h := http.Header{}
	h.Add(Header, `cfL4;desc="x"`)
	h.Add(Header, "origin;dur=1.0")
	AppendServerTiming(h, TimingEntry{Name: "entry", Dur: 5 * time.Millisecond, Desc: "sgp-1"})
	want := `cfL4;desc="x", origin;dur=1.0, entry;dur=5.0;desc="sgp-1"`
	if got := h.Values(Header); len(got) != 1 || got[0] != want {
		t.Fatalf("got %q, want [%q]", got, want)
	}
}

func TestWithServerTimingStampsOnceBeforeHeaders(t *testing.T) {
	calls := 0
	rec := httptest.NewRecorder()
	w := WithServerTiming(rec, func() []TimingEntry {
		calls++
		return []TimingEntry{{Name: "edge", Dur: time.Millisecond}}
	})
	w.WriteHeader(http.StatusEarlyHints) // interim responses don't count
	w.Header().Set(Header, "origin;dur=2.0")
	_, _ = w.Write([]byte("a"))
	w.(http.Flusher).Flush()
	_, _ = w.Write([]byte("b"))
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	if got := rec.Header().Get(Header); got != "origin;dur=2.0, edge;dur=1.0" {
		t.Fatalf("Server-Timing = %q", got)
	}
	if !rec.Flushed {
		t.Fatal("Flush did not reach the underlying writer")
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("ResponseController through the wrapper: %v", err)
	}
}

// A real instance: the Manager reports origin (the instance's handler) and wake (starting it on the first request).
func TestManagerServerTiming(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := `routerAdd("GET", "/slow", (e) => { sleep(40); return e.string(200, "ok") })`
	if err := os.WriteFile(filepath.Join(root, "app", "pb_hooks/main.pb.js"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := pocker.Instance{ID: "app", Hosts: []string{"app.example.com"}}
	m := pocker.NewManager(staticSource{inst}, pocker.ManagerOptions{
		PBRoot: root,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	h := New().Wrap(m) // the plugin collects the timings the Manager registers

	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://app.example.com/slow", nil))
		return w
	}
	var first *httptest.ResponseRecorder
	for deadline := time.Now().Add(10 * time.Second); ; {
		if first = get(); first.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance never answered: %d %s", first.Code, first.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	names, durs := timings(first.Header().Get(Header))
	if strings.Join(names, ",") != "origin,wake" {
		t.Fatalf("Server-Timing names = %v (%q)", names, first.Header().Get(Header))
	}
	if durs[0] < 35 {
		t.Errorf("origin = %.1f ms, want at least the hook's 40 ms sleep", durs[0])
	}
	if durs[1] <= 0 {
		t.Errorf("first request's wake = %.1f ms, want > 0 (the instance started for it)", durs[1])
	}

	_, durs = timings(get().Header().Get(Header))
	if durs[1] > 5 {
		t.Errorf("running instance's wake = %.1f ms, want ~0", durs[1])
	}
	if ok, _ := regexp.MatchString(`^origin;dur=[0-9.]+, wake;dur=[0-9.]+$`, get().Header().Get(Header)); !ok {
		t.Errorf("unexpected header shape")
	}
}
