package docker

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
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
	root := t.TempDir()
	data := filepath.Join(root, "instances")
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte(`
instances:
  - id: cehostup
    hosts: [up.test]
    version: "pb:0.23.*"
  - id: cehostce
    hosts: [ce.test]
    version: "ce:0.0.*"
  - id: cehostoff
    hosts: [off.test]
    version: "pb:0.23.*"
    blocked: { status: 409, message: instance is powered off }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := New(Config{Socket: socket, Image: "alpine:3.20", DataRoot: data, Binaries: testBinaries(t), Logger: quiet})
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
