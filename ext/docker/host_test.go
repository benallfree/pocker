package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

// tagGate stands in for a host gate (e.g. ext/firewall): it admits everything and tags the response, so the test can
// see that gates run in front of the docker runtime.
type tagGate struct{}

func (tagGate) AllowPeer(*http.Request) bool { return true }
func (tagGate) Admit(w http.ResponseWriter, _ *http.Request, inst host.Instance) (func(), bool) {
	w.Header().Set("X-Gate", "admitted "+inst.ID)
	return func() {}, true
}

// docker as the one runtime of a host, without edge: instances from a YAML file, routed by hostname, the gate in
// front, a blocked instance answered by the host, an unknown host 404.
func TestHostWithDocker(t *testing.T) {
	socket := dockerSocket(t)
	tb := testBinaries(t)
	root := dataDir(t)
	data := filepath.Join(root, "instances")
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte(`
instances:
  - id: cehostup
    hosts: [up.test]
    version: "pb:0.23.*"
  - id: cehostce
    hosts: [ce.test]
    version: "p1:0.0.*"
  - id: cehostoff
    hosts: [off.test]
    version: "pb:0.23.*"
    blocked: { status: 409, message: instance is powered off }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := New(Config{Socket: socket, Image: "alpine:3.20", DataRoot: data, Binaries: tb, Versions: testVersions(t, tb), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range []string{"cehostup", "cehostce", "cehostoff"} {
			_ = rt.e.remove(context.Background(), id)
		}
	})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	h := &host.Host{Source: &host.YAMLSource{Path: instances, Required: true}, Runtime: rt, Gates: []host.Gate{tagGate{}},
		Listeners: host.Listeners{Listen: addr}, Logger: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	defer func() { cancel(); <-done }()

	client := &http.Client{Transport: &http.Transport{}, Timeout: 90 * time.Second}
	get := func(hostname, path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		req.Host = hostname
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s%s: %v", hostname, path, err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if res, err := client.Get("http://" + addr + host.DefaultHealthPath); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break // Start ran (image present, adoption done) and the instances loaded
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("host not ready")
		}
	}
	if !rt.Ready() {
		t.Fatal("the host didn't start the runtime")
	}

	for _, hn := range []string{"up.test", "ce.test"} {
		code, body, hdr := get(hn, "/api/health")
		if code != 200 || !strings.Contains(body, "healthy") || !strings.HasPrefix(hdr.Get("X-Gate"), "admitted cehost") {
			t.Fatalf("%s: %d %q gate=%q", hn, code, body, hdr.Get("X-Gate"))
		}
	}
	if !rt.Running("cehostup") || !rt.Running("cehostce") {
		t.Fatal("containers not running")
	}
	if code, body, _ := get("off.test", "/api/health"); code != 409 || !strings.Contains(body, "powered off") || rt.Running("cehostoff") {
		t.Fatalf("blocked: %d %q (running %v)", code, body, rt.Running("cehostoff"))
	}
	if code, _, _ := get("nope.test", "/api/health"); code != 404 {
		t.Fatalf("unknown host: %d", code)
	}
	// the instances' data lives in their folders under the data root
	if _, err := os.Stat(filepath.Join(data, "cehostce", "pb_data", "data.db")); err != nil {
		t.Fatalf("data folder: %v", err)
	}
}

// Maintenance: WithInstanceStopped stops the container and holds requests; StartInstance starts it again.
func TestMaintenance(t *testing.T) {
	socket := dockerSocket(t)
	tb := testBinaries(t)
	rt, err := New(Config{Socket: socket, Image: "alpine:3.20", DataRoot: dataDir(t), Binaries: tb, Versions: testVersions(t, tb),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = rt.e.remove(context.Background(), "p4maint") })
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer rt.Shutdown()
	inst := host.Instance{ID: "p4maint", Version: "p1:0.0.*"}
	if err := rt.StartInstance(ctx, inst); err != nil || !rt.Running(inst.ID) {
		t.Fatalf("start: %v running=%v", err, rt.Running(inst.ID))
	}
	inFn := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- rt.WithInstanceStopped(ctx, inst.ID, func(dir string) error {
			if dir != rt.InstanceDir(inst.ID) || rt.Running(inst.ID) {
				return fmt.Errorf("fn ran with dir %s, running %v", dir, rt.Running(inst.ID))
			}
			close(inFn)
			time.Sleep(300 * time.Millisecond)
			return nil
		})
	}()
	<-inFn
	// a request while held waits for the release, then the instance serves again
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://p4maint.test/api/health", nil)
	start := time.Now()
	if !rt.Precheck(w, r, inst) || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("precheck didn't hold: %v after %s", w.Code, time.Since(start))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := rt.WithInstanceStopped(ctx, inst.ID, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, rt, inst, "/api/health"); code != 200 {
		t.Fatalf("after maintenance: %d", code)
	}
}

// Changing a running instance's pb_hooks restarts its container, which then serves the new code.
func TestHooksChangeRestartsContainer(t *testing.T) {
	socket := dockerSocket(t)
	tb := testBinaries(t)
	data := dataDir(t)
	rt, err := New(Config{Socket: socket, Image: "alpine:3.20", DataRoot: data, Binaries: tb, Versions: testVersions(t, tb),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = rt.e.remove(context.Background(), "p4hooks") })
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer rt.Shutdown()
	inst := host.Instance{ID: "p4hooks", Version: "p1:0.0.*"}
	hook := func(v string) {
		t.Helper()
		_ = os.MkdirAll(filepath.Join(data, inst.ID, "pb_hooks"), 0o755)
		src := `routerAdd("GET", "/v", (e) => e.string(200, "` + v + `"))`
		if err := os.WriteFile(filepath.Join(data, inst.ID, "pb_hooks", "main.pb.js"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hook("v1")
	if code, body := get(t, rt, inst, "/v"); code != 200 || body != "v1" {
		t.Fatalf("before: %d %q", code, body)
	}
	before := rt.get(inst.ID).container
	hook("v2")
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, body := get(t, rt, inst, "/v")
		if code == 200 && body == "v2" {
			break
		}
		if code != 200 && code != 404 {
			t.Fatalf("during restart: %d %q (requests should wait, not fail)", code, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %d %q after the hook changed", code, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rt.get(inst.ID).container == before {
		t.Fatal("served v2 without a new container")
	}
}
