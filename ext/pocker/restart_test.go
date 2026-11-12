package pocker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// app.Restart() (PocketBase's backup restore) must restart only that app, not re-exec the process.
func TestAppRestartRestartsOnlyThatApp(t *testing.T) {
	var inits []string
	a, b := Instance{ID: "a", Hosts: []string{"a.test"}}, Instance{ID: "b", Hosts: []string{"b.test"}}
	m := NewManager(staticSource{a, b}, ManagerOptions{
		PBRoot: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Init: func(inst Instance, app core.App) error {
			inits = append(inits, inst.ID) // runs before bootstrap
			if app.IsBootstrapped() {
				t.Errorf("Init ran after bootstrap")
			}
			return nil
		},
	})
	m.dir.Load([]Instance{a, b})
	defer m.Shutdown()

	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if err := m.StartInstance(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	ra, _ := m.app(a)
	rb, _ := m.app(b)

	// a plugin's OnTerminate handler must still see a normal terminate: DB closed after its e.Next()
	var closedAfterNext bool
	ra.app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		err := e.Next()
		closedAfterNext = !e.App.IsBootstrapped()
		return err
	})

	if err := ra.app.Restart(); err != nil {
		t.Fatal(err)
	}
	if !closedAfterNext {
		t.Error("plugin handler: databases still open after e.Next()")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.mu.RLock()
		slot := m.apps["a"]
		m.mu.RUnlock()
		if slot != nil {
			<-slot.done
			if slot.app != nil && slot.app != ra {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("a was not restarted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rb2, _ := m.app(b); rb2 != rb || !rb.app.IsBootstrapped() {
		t.Error("b was restarted too")
	}
	if len(inits) != 3 {
		t.Errorf("Init calls: %v", inits)
	}
}
