package pocker

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

	"github.com/pockethost/pocketbase/ext/edge"
)

// The whole path: pocker.yaml keys, per-instance plugins.firewall from instances.yaml, header names, 429.
func TestServerFirewall(t *testing.T) {
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	confPath := filepath.Join(root, "pocker.yaml")
	if err := os.WriteFile(confPath, []byte(fmt.Sprintf(`
listen: %q
pb_root: %q
instances_file: %q
memory_soft_limit: -1
firewall: { rate_limit_header_prefix: X-PocketHost-RateLimit }
`, addr, root, instances)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(confPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Firewall: &edge.Firewall{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()

	get := func(host string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = host
		res, err := fwTestClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = "relaxed.test"
		if res, err := fwTestClient.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("server stopped: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not come up")
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
	// the other instance keeps the default budget; the first request to it was the readiness probe
	if res := get("relaxed.test"); res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Limit") != "1000" {
		t.Fatalf("default limit: %v", res.Header)
	}
}


var fwTestClient = &http.Client{Transport: &http.Transport{}}
