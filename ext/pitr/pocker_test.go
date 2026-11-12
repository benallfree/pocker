package pitr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/pocker"
)

type fixedSource []pocker.Instance

func (s fixedSource) Snapshot(context.Context) ([]pocker.Instance, error) { return s, nil }
func (s fixedSource) Watch(context.Context) (<-chan pocker.Change, error) { return nil, nil }

// Under the multitenant host: the restore route stages, app.Restart() restarts only that instance, the staged DB is
// swapped in before the new app opens it, and the other instance keeps running untouched.
func TestPockerRestoreRestartsOnlyThatInstance(t *testing.T) {
	if raceEnabled {
		t.Skip("PocketBase's notify watcher races with ResetBootstrapState on app restart (upstream, see race_test.go); run without -race")
	}
	p := New(Config{Logger: quiet}) // on by default
	defer p.Close(context.Background())

	var mu sync.Mutex
	apps := map[string][]core.App{} // every app started, by instance
	a := pocker.Instance{ID: "a", Hosts: []string{"a.test"}, Plugins: map[string]any{"pitr": map[string]any{"retention": "168h"}}}
	b := pocker.Instance{ID: "b", Hosts: []string{"b.test"}, Plugins: map[string]any{"pitr": map[string]any{"enabled": false}}}
	m := pocker.NewManager(fixedSource{a, b}, pocker.ManagerOptions{
		PBRoot: t.TempDir(),
		Logger: quiet,
		Init:   p.InitApp, // what pocker.Server wires for plugins implementing pocker.AppIniter
		Setup: func(inst pocker.Instance, app core.App) error {
			mu.Lock()
			apps[inst.ID] = append(apps[inst.ID], app)
			mu.Unlock()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	for !m.Ready() {
		time.Sleep(10 * time.Millisecond)
	}
	latest := func(id string) core.App {
		mu.Lock()
		defer mu.Unlock()
		l := apps[id]
		if len(l) == 0 {
			return nil
		}
		return l[len(l)-1]
	}
	for _, id := range []string{"a", "b"} {
		if err := m.StartInstance(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	appA, appB := latest("a"), latest("b")
	if p.appFor(appB) != nil {
		t.Fatal("pitr enabled for b, whose plugins.pitr.enabled is false")
	}
	if got := p.appFor(appA).retention; got != 168*time.Hour {
		t.Fatalf("a's retention = %v, want its plugins.pitr.retention 168h", got)
	}
	tok := superuserToken(t, appA) // before t1, so it survives the restore
	notesCollection(t, appA)
	addNotes(t, appA, "a", 30)
	if err := p.storeSync(appA); err != nil {
		t.Fatal(err)
	}
	t1 := mark()
	if _, err := appA.DB().NewQuery(`DELETE FROM notes`).Execute(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://a.test/api/pitr/restore", strings.NewReader(`{"at":"`+t1.UTC().Format(time.RFC3339Nano)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", tok)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}

	deadline := time.Now().Add(20 * time.Second)
	for latest("a") == appA {
		if time.Now().After(deadline) {
			t.Fatal("instance a was not restarted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n, err := latest("a").CountRecords("notes"); err != nil || n != 30 {
		t.Fatalf("after restore: %d notes (%v), want 30", n, err)
	}
	if latest("b") != appB || !appB.IsBootstrapped() {
		t.Error("instance b was restarted")
	}

	// status from the new app reports the restore
	req = httptest.NewRequest(http.MethodGet, "http://a.test/api/pitr", nil)
	req.Header.Set("Authorization", tok)
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lastRestore"`) {
		t.Fatalf("status after restore: %d %s", rec.Code, rec.Body)
	}

	// PocketBase's own zip restore: restarts only a, keeps the history, replication carries on
	appA = latest("a")
	if err := appA.CreateBackup(ctx, "z.zip"); err != nil {
		t.Fatal(err)
	}
	addNotes(t, appA, "after-zip", 4)
	if err := appA.RestoreBackup(ctx, "z.zip"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for latest("a") == appA {
		if time.Now().After(deadline) {
			t.Fatal("instance a was not restarted after the zip restore")
		}
		time.Sleep(50 * time.Millisecond)
	}
	appA = latest("a")
	if n, _ := appA.CountRecords("notes"); n != 30 {
		t.Fatalf("after zip restore: %d notes, want 30", n)
	}
	addNotes(t, appA, "z", 2)
	if err := p.storeSync(appA); err != nil {
		t.Fatal(err)
	}
	st, err := p.Status(ctx, appA)
	if err != nil || st.Window == nil || st.Window.Earliest.After(t1) {
		t.Fatalf("history after zip restore: %+v %v", st, err)
	}
	if latest("b") != appB {
		t.Error("instance b was restarted by a's zip restore")
	}
}

func TestConfigureAndInstanceConfig(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("pitr:\n  enabled: false\n  retention: 48h\n  bucket:\n    bucket: x\n    label: EU\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p := New(Config{SnapshotInterval: time.Hour, Logger: quiet})
	if err := p.Configure(&doc, pocker.Config{}); err != nil {
		t.Fatal(err)
	}
	c := p.Config()
	if c.Enabled == nil || *c.Enabled || c.Retention != 48*time.Hour || c.SnapshotInterval != time.Hour {
		t.Fatalf("configured: %+v", c)
	}
	// Go-set values win over the file
	p2 := New(Config{Retention: 24 * time.Hour, Logger: quiet})
	_ = p2.Configure(&doc, pocker.Config{})
	if p2.Config().Retention != 24*time.Hour {
		t.Fatalf("file overrode a Go-set retention: %v", p2.Config().Retention)
	}
	// instances can't carry a bucket
	inst := pocker.Instance{ID: "x", Plugins: map[string]any{"pitr": map[string]any{"bucket": map[string]any{"secret": "s"}}}}
	if err := p.InitApp(inst, core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})); err == nil || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("bucket in instance config: %v", err)
	}
}
