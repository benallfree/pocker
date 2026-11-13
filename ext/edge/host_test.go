package edge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pockethost/pocketone/ext/host"
	"gopkg.in/yaml.v3"
)

type noRuntime struct{}

func (noRuntime) Name() string              { return "none" }
func (noRuntime) Serves(host.Instance) bool { return true }
func (noRuntime) Ready() bool               { return true }
func (noRuntime) ServeInstance(w http.ResponseWriter, _ *http.Request, _ host.Instance) {
	http.Error(w, "local", 500)
}

// On a host, a config file's `edges:` section routes instances owned by another edge through the Router (the fleet
// layer filling the host's Forward hook).
func TestHostEdgesFromYAML(t *testing.T) {
	root := t.TempDir()
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == host.DefaultHealthPath {
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		_, _ = io.WriteString(w, "owner "+r.Host+" via "+r.Header.Get("X-Edge-Forwarded-By"))
	}))
	defer owner.Close()
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte("instances:\n  - id: away\n    hosts: [away.test]\n    remote: \"edge:@sgp-1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("edges:\n  self: lax-1\n  list:\n    - { id: sgp-1, internal_url: \""+owner.URL+"\" }\n"), &doc); err != nil {
		t.Fatal(err)
	}
	edges, err := EdgesFromConfig(host.TopLevel(&doc, "edges"), quietLog)
	if err != nil || edges == nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	h := &host.Host{Source: &host.YAMLSource{Path: instances, Required: true}, Runtime: noRuntime{},
		Forward: NewRouter(RouterConfig{Edges: edges, Logger: quietLog}).Forward, Listeners: host.Listeners{Listen: addr}, Logger: quietLog}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	defer func() { cancel(); <-done }()
	client := &http.Client{Transport: &http.Transport{}}
	var res *http.Response
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/x", nil)
		req.Host = "away.test"
		if res, err = client.Do(req); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "owner away.test via lax-1" {
		t.Fatalf("forwarded: %d %q", res.StatusCode, body)
	}
}
