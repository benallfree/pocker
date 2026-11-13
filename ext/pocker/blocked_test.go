package pocker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
)

// Blocking a running instance (e.g. powering it off) stops its app, so cron jobs stop too, and unblocking starts a
// fresh app rather than reviving the old one.
func TestBlockedInstanceStopsApp(t *testing.T) {
	var mu sync.Mutex
	var events []EventKind
	inst := Instance{ID: "a", Hosts: []string{"a.test"}}
	m := NewManager(staticSource{inst}, ManagerOptions{
		PBRoot: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnEvent: func(e Event) {
			mu.Lock()
			events = append(events, e.Kind)
			mu.Unlock()
		},
	})
	m.dir.Load([]Instance{inst})
	defer m.Shutdown()

	ctx := context.Background()
	if err := m.StartInstance(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	first, _ := m.app(inst)

	off := inst
	off.Blocked = &Blocked{Status: http.StatusServiceUnavailable, Message: "powered off"}
	m.dir.Apply(Change{Kind: Upsert, Instance: off})

	if m.Running("a") {
		t.Fatal("a blocked instance's app is still registered")
	}
	if err := m.StartInstance(ctx, "a"); err == nil {
		t.Fatal("a blocked instance must not start")
	}
	mu.Lock()
	stopped := containsString(kinds(events), string(EventStopped))
	mu.Unlock()
	if !stopped {
		t.Fatalf("no %q event: %v", EventStopped, events)
	}

	m.dir.Apply(Change{Kind: Upsert, Instance: inst})
	if err := m.StartInstance(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	second, _ := m.app(inst)
	if second == first {
		t.Fatal("unblocking reused the old app instead of starting a fresh one")
	}
}

func kinds(es []EventKind) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = string(e)
	}
	return out
}
