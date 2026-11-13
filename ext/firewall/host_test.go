package firewall

import (
	"context"
	"fmt"
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
	"gopkg.in/yaml.v3"
)

// okRuntime answers every request 200: the firewall's headers and 429s are what's under test.
type okRuntime struct{}

func (okRuntime) Name() string              { return "ok" }
func (okRuntime) Serves(host.Instance) bool { return true }
func (okRuntime) Ready() bool               { return true }
func (okRuntime) ServeInstance(w http.ResponseWriter, r *http.Request, _ host.Instance) {
	_, _ = io.WriteString(w, "ok")
}

// The whole path on a host: config sections turn the registered firewall on, per-instance plugins.firewall from the
// instances file, header names, 429.
func TestHostWithFirewall(t *testing.T) {
	root := t.TempDir()
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte(`
instances:
  - id: limited
    hosts: [limited.test]
    plugins: { firewall: { rate_limits: { ip_hourly: 3 } } }
  - id: relaxed
    hosts: [relaxed.test]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("firewall: { rate_limit_header_prefix: X-PocketHost-RateLimit }\n"), &doc); err != nil {
		t.Fatal(err)
	}
	gates := host.ConfiguredGates(&doc)
	if len(gates) != 1 {
		t.Fatalf("registered gates turned on by the config: %d", len(gates))
	}
	if err := gates[0].Configure(&doc, false); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	h := &host.Host{Source: &host.YAMLSource{Path: instances, Required: true}, Runtime: okRuntime{},
		Gates: []host.Gate{gates[0]}, Listeners: host.Listeners{Listen: addr}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	defer func() { cancel(); <-done }()

	client := &http.Client{Transport: &http.Transport{}}
	get := func(hostname string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = hostname
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/_pocker/health", nil)
		if res, err := client.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("host did not come up")
		}
	}
	for i := 1; i <= 3; i++ {
		res := get("limited.test")
		if res.StatusCode != 200 || res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Limit") != "3" ||
			res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Remaining") != fmt.Sprint(3-i) {
			t.Fatalf("request %d: %d %v", i, res.StatusCode, res.Header)
		}
		if !strings.Contains(res.Header.Get("Access-Control-Expose-Headers"), "X-PocketHost-RateLimit-Instance-Concurrent-Remaining") {
			t.Fatalf("expose headers missing: %q", res.Header.Get("Access-Control-Expose-Headers"))
		}
	}
	if res := get("limited.test"); res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("4th request: %d (Retry-After %q)", res.StatusCode, res.Header.Get("Retry-After"))
	}
	if res := get("relaxed.test"); res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Limit") != "1000" {
		t.Fatalf("default limit: %v", res.Header)
	}
}
