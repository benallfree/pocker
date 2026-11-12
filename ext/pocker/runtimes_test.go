package pocker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pockethost/pocketbase/ext/edge"
)

// fakeRuntime stands in for ext/docker: it serves instances asking for "fake" and records the hooks it got.
type fakeRuntime struct {
	started, stopped atomic.Bool
	mu               sync.Mutex
	synced           []string
}

func (f *fakeRuntime) Name() string                   { return "fake" }
func (f *fakeRuntime) Serves(inst edge.Instance) bool { return inst.Runtime == "fake" }
func (f *fakeRuntime) Ready() bool                    { return f.started.Load() }
func (f *fakeRuntime) ServeInstance(w http.ResponseWriter, r *http.Request, inst edge.Instance) {
	_, _ = io.WriteString(w, "fake "+inst.ID+" v"+inst.Version)
}
func (f *fakeRuntime) Start(context.Context) error { f.started.Store(true); return nil }
func (f *fakeRuntime) Shutdown()                   { f.stopped.Store(true) }
func (f *fakeRuntime) Sync(c edge.Change, _ *edge.Instance) {
	f.mu.Lock()
	f.synced = append(f.synced, c.Instance.ID)
	f.mu.Unlock()
}

// Instances asking for another runtime go there; the rest run in-process; the extra runtime is started before the
// server listens, follows directory changes and is shut down after.
func TestServerExtraRuntime(t *testing.T) {
	root := t.TempDir()
	addr := freeAddr(t)
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte("instances:\n  - id: inproc\n    hosts: [inproc.test]\n  - id: old\n    hosts: [old.test]\n    version: 0.22.21\n    runtime: fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "pocker.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("listen: %q\npb_root: %q\ninstances_file: %q\nmemory_soft_limit: -1\n", addr, filepath.Join(root, "pb"), instances)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeRuntime{}
	srv := &Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runtimes: []edge.Runtime{fake}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	client := &http.Client{Transport: &http.Transport{}}
	get := func(host string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = host
		res, err := client.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if code, _ := get("inproc.test"); code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server not up")
		}
	}
	if !fake.started.Load() {
		t.Fatal("extra runtime not started")
	}
	if code, body := get("old.test"); code != 200 || body != "fake old v0.22.21" {
		t.Fatalf("old: %d %q", code, body)
	}
	if code, body := get("inproc.test"); code != 200 || body == "fake inproc v" {
		t.Fatalf("inproc went to the extra runtime: %d %q", code, body)
	}
	srv.Manager.Directory().Apply(edge.Change{Kind: edge.Delete, Instance: edge.Instance{ID: "old"}})
	fake.mu.Lock()
	synced := fmt.Sprint(fake.synced)
	fake.mu.Unlock()
	if synced != "[old]" {
		t.Fatalf("sync: %s", synced)
	}
	cancel()
	<-done
	if !fake.stopped.Load() {
		t.Fatal("extra runtime not shut down")
	}
}
