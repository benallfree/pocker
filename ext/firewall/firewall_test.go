package firewall

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

	"github.com/pockethost/pocketbase/ext/pocker"
)

func fwRequest(remote, path string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://x.test"+path, nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

type staticSource []pocker.Instance

func (s staticSource) Snapshot(context.Context) ([]pocker.Instance, error) { return s, nil }
func (s staticSource) Watch(context.Context) (<-chan pocker.Change, error) { return nil, nil }

func TestFirewallPeersAndClientIP(t *testing.T) {
	fw, err := NewFirewall(Config{
		AllowCIDRs:     []string{"173.245.48.0/20", "127.0.0.1"},
		TrustedProxies: []string{"173.245.48.0/20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fw.AllowPeer(fwRequest("8.8.8.8:1", "/", nil)) {
		t.Error("peer outside allow_cidrs must be refused")
	}
	if !fw.AllowPeer(fwRequest("173.245.48.9:1", "/", nil)) || !fw.AllowPeer(fwRequest("127.0.0.1:1", "/", nil)) {
		t.Error("allowed peers refused")
	}
	if got := fw.ClientIP(fwRequest("173.245.48.9:1", "/", map[string]string{"CF-Connecting-IP": "203.0.113.7"})); got.String() != "203.0.113.7" {
		t.Errorf("trusted proxy header: got %s", got)
	}
	if got := fw.ClientIP(fwRequest("173.245.48.9:1", "/", map[string]string{"X-Forwarded-For": "198.51.100.4, 10.0.0.1"})); got.String() != "198.51.100.4" {
		t.Errorf("X-Forwarded-For first hop: got %s", got)
	}
	if got := fw.ClientIP(fwRequest("127.0.0.1:1", "/", map[string]string{"CF-Connecting-IP": "203.0.113.7"})); got.String() != "127.0.0.1" {
		t.Errorf("header from an untrusted peer must be ignored: got %s", got)
	}

	// through the Manager: a refused peer gets 403 before anything else
	inst := pocker.Instance{ID: "a", Hosts: []string{"a.test"}}
	m := pocker.NewManager(staticSource{inst}, pocker.ManagerOptions{
		PBRoot: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Gate: fw,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://a.test/api/health", nil)
	r.RemoteAddr = "8.8.8.8:1"
	m.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("refused peer: %d", w.Code)
	}
}

func TestRateLimitsTrustedWeightsConcurrency(t *testing.T) {
	fw, _ := NewFirewall(Config{TrustedClients: []string{"198.51.100.0/24"}})
	withLimits := func(id string, rl RateLimits, trusted ...string) pocker.Instance {
		return pocker.Instance{ID: id, Plugins: map[string]any{"firewall": InstanceConfig{RateLimits: &rl, TrustedIPs: trusted}}}
	}
	check := func(remote, path string, inst pocker.Instance) (int, func()) {
		w := httptest.NewRecorder()
		rel, ok := fw.Admit(w, fwRequest(remote, path, nil), inst)
		if !ok {
			return w.Code, nil
		}
		return 200, rel
	}

	// files cost 1/10: with ip_hourly 1, ten file requests fit, the eleventh doesn't
	inst := withLimits("a", RateLimits{IPHourly: 1})
	for i := 0; i < 10; i++ {
		if code, rel := check("192.0.2.1:1", "/api/files/x/y/z.png", inst); code != 200 {
			t.Fatalf("file request %d: %d", i, code)
		} else {
			rel()
		}
	}
	if code, _ := check("192.0.2.1:1", "/api/files/x", inst); code != http.StatusTooManyRequests {
		t.Fatalf("11th file request: %d", code)
	}

	// trusted clients (process list or the instance's own) get the trusted limits
	inst = withLimits("b", RateLimits{IPHourly: 1, Trusted: &RateLimitSet{IPHourly: 3}}, "203.0.113.9")
	for _, ip := range []string{"198.51.100.5", "203.0.113.9"} {
		for i := 0; i < 3; i++ {
			if code, rel := check(ip+":1", "/api/health", inst); code != 200 {
				t.Fatalf("trusted %s request %d: %d", ip, i, code)
			} else {
				rel()
			}
		}
	}
	if code, _ := check("192.0.2.50:1", "/api/health", inst); code != 200 {
		t.Fatal("untrusted first request")
	}
	if code, _ := check("192.0.2.50:1", "/api/health", inst); code != http.StatusTooManyRequests {
		t.Fatal("untrusted second request should hit ip_hourly 1")
	}

	// concurrency: a held request keeps its slot until released
	inst = withLimits("c", RateLimits{IPConcurrent: 1, InstanceConcurrent: 2})
	_, hold := check("192.0.2.1:1", "/api/realtime", inst)
	if code, _ := check("192.0.2.1:1", "/api/health", inst); code != http.StatusTooManyRequests {
		t.Fatal("second concurrent request from the same IP should be refused")
	}
	_, hold2 := check("192.0.2.2:1", "/api/health", inst)
	if code, _ := check("192.0.2.3:1", "/api/health", inst); code != http.StatusTooManyRequests {
		t.Fatal("instance_concurrent 2 should refuse a third")
	}
	hold()
	hold2()
	if code, rel := check("192.0.2.1:1", "/api/health", inst); code != 200 {
		t.Fatal("slot not released")
	} else {
		rel()
	}

	// rate_limits: off
	inst = withLimits("d", RateLimits{Off: true})
	for i := 0; i < 50; i++ {
		if code, _ := check("192.0.2.1:1", "/api/health", inst); code != 200 {
			t.Fatal("rate_limits: off still limited")
		}
	}
}

// The whole path: pocker.yaml keys, per-instance plugins.firewall from instances.yaml, header names, 429.
func TestServerWithFirewallPlugin(t *testing.T) {
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
	cfg, err := pocker.LoadConfig(confPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &pocker.Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Plugins: []pocker.Plugin{New()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()

	get := func(host string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = host
		res, err := testClient.Do(req)
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
		if res, err := testClient.Do(req); err == nil {
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

// testClient has its own transport: the server installs its egress policy on http.DefaultTransport, which refuses
// loopback.
var testClient = &http.Client{Transport: &http.Transport{}}
