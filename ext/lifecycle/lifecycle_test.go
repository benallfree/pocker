package lifecycle

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

type recordSink struct {
	mu      sync.Mutex
	reports [][]Facts
	loaded  []Facts
}

func (s *recordSink) Report(_ context.Context, facts []Facts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, facts)
	return nil
}

func (s *recordSink) Load(context.Context) ([]Facts, error) { return s.loaded, nil }

func (s *recordSink) last() []Facts {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		return nil
	}
	return s.reports[len(s.reports)-1]
}

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Lifecycle, *recordSink, *time.Time, string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "pb_data", "data.db"), 1000)
	write(t, filepath.Join(root, "a", "pb_hooks", "main.pb.js"), 24)
	// b has no folder (never started); c runs on another edge
	dir := host.NewDirectory(slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir.Load([]host.Instance{
		{ID: "a", Hosts: []string{"a.test"}},
		{ID: "b", Hosts: []string{"b.test"}},
		{ID: "c", Hosts: []string{"c.test"}, Remote: "edge:@other"},
	})
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	sink := &recordSink{}
	every := SectionConfig{MeasureEvery: 24 * time.Hour, ReportEvery: time.Hour, Concurrency: 2}
	lc := &Lifecycle{
		Directory: dir,
		Folder:    func(i host.Instance) string { return filepath.Join(root, i.ID) },
		Policy:    YAMLPolicy{Config: &every, Now: func() time.Time { return now }},
		Sink:      sink,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return now },
	}
	return lc, sink, &now, root
}

func TestMeasureOnlyLocalInstancesOnSchedule(t *testing.T) {
	lc, sink, now, root := setup(t)
	ctx := context.Background()

	lc.Step(ctx)
	got := sink.last()
	if len(got) != 2 || got[0].Instance != "a" || got[0].DiskBytes != 1024 || got[1].Instance != "b" || got[1].DiskBytes != 0 {
		t.Fatalf("first pass (local instances only; a folder-less one is 0): %+v", got)
	}

	// nothing is due again until measure_every has passed
	n := len(sink.reports)
	*now = now.Add(23 * time.Hour)
	lc.Step(ctx)
	if len(sink.reports) != n {
		t.Fatal("measured again before measure_every")
	}
	write(t, filepath.Join(root, "a", "pb_data", "logs.db"), 500)
	*now = now.Add(time.Hour)
	lc.Step(ctx)
	if got := sink.last(); len(got) != 2 || got[0].DiskBytes != 1524 || !got[0].MeasuredAt.Equal(*now) {
		t.Fatalf("after measure_every: %+v", got)
	}
}

func TestLastActiveFromServedRequests(t *testing.T) {
	lc, sink, now, _ := setup(t)
	ctx := context.Background()
	admit := func(id, remote string) {
		rel, ok := lc.Admit(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), host.Instance{ID: id, Remote: remote})
		if !ok {
			t.Fatal("lifecycle refused a request")
		}
		rel()
	}
	admit("a", "")
	admit("c", "edge:@other") // forwarded: not this host's activity
	lc.reportActive(ctx)
	got := sink.last()
	if len(got) != 1 || got[0].Instance != "a" || !got[0].LastActive.Equal(*now) {
		t.Fatalf("active report: %+v", got)
	}
	// nothing new: no report; a request within the minute doesn't count as a change
	n := len(sink.reports)
	*now = now.Add(30 * time.Second)
	admit("a", "")
	lc.reportActive(ctx)
	if len(sink.reports) != n {
		t.Fatal("reported an unchanged instance")
	}
}

func TestLoadedFactsAreNotMeasuredAgain(t *testing.T) {
	lc, sink, now, _ := setup(t)
	sink.loaded = []Facts{
		{Instance: "a", DiskBytes: 7, MeasuredAt: now.Add(-time.Hour), LastActive: now.Add(-2 * time.Hour)},
		{Instance: "b", DiskBytes: 0, MeasuredAt: now.Add(-25 * time.Hour)},
	}
	lc.seed(sink.loaded)
	lc.Step(context.Background())
	got := sink.last()
	if len(got) != 1 || got[0].Instance != "b" {
		t.Fatalf("only the stale measurement is redone: %+v", got)
	}
	if f := lc.Local()[0]; f.DiskBytes != 7 || f.LastActive.IsZero() {
		t.Fatalf("loaded facts kept: %+v", f)
	}
}

func TestDirBytesSkipsSymlinksAndMissing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "x", "f"), 10)
	if err := os.Symlink(filepath.Join(root, "x", "f"), filepath.Join(root, "x", "link")); err != nil {
		t.Fatal(err)
	}
	if n, err := DirBytes(context.Background(), filepath.Join(root, "x")); err != nil || n != 10 {
		t.Fatalf("got %d %v", n, err)
	}
	if n, err := DirBytes(context.Background(), filepath.Join(root, "missing")); err != nil || n != 0 {
		t.Fatalf("missing folder: %d %v", n, err)
	}
}
