package pocker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func writeHooks(t *testing.T, root, id, js string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, id, "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id, "pb_hooks/main.pb.js"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testClient has its own transport: Pocker's egress policy guards Go's default transport (no loopback), which is
// exactly what host code that needs private addresses must also avoid.
var testClient = &http.Client{Transport: &http.Transport{}}

func waitHTTP(t *testing.T, url string, host string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Host = host
		if res, err := testClient.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", url)
}

// End to end through Server: per-instance limits from the YAML file reach the apps and the firewall.
func TestServerAppliesPerInstanceYAML(t *testing.T) {
	root := t.TempDir()
	spin := `routerAdd("GET", "/spin", (e) => { const s = Date.now(); while (Date.now() - s < 2500) {} return e.json(200, {}) })`
	writeHooks(t, root, "strict", spin)
	writeHooks(t, root, "relaxed", spin)
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte(`
instances:
  - id: limited
    hosts: [limited.test]
    limits: { rate_limits: { ip_hourly: 3 } }
  - id: cors
    hosts: [cors.test]
    access: { cors_origins: ["https://app.example.com"] }
  - id: strict
    hosts: [strict.test]
    limits: { max_js_execution: 1s }
  - id: relaxed
    hosts: [relaxed.test]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	cfg := DefaultConfig()
	cfg.Listen, cfg.PBRoot, cfg.InstancesFile = addr, root, instances
	cfg.MemorySoftLimit = -1
	cfg.Firewall.RateLimitHeaderPrefix = "X-PocketHost-RateLimit"
	srv := &Server{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		SetupApp: func(inst Instance, app core.App) error {
			app.OnServe().BindFunc(func(e *core.ServeEvent) error {
				e.Router.GET("/api/host-route", func(re *core.RequestEvent) error {
					return re.String(http.StatusOK, "host code for "+inst.ID)
				})
				return e.Next()
			})
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()
	base := "http://" + addr
	waitHTTP(t, base+"/api/health", "relaxed.test")

	get := func(host, path string, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}

	// rate limit from the instance's YAML (ip_hourly: 3), PocketHost header names
	for i := 1; i <= 3; i++ {
		res := get("limited.test", "/api/health", nil)
		if res.StatusCode != 200 || res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Limit") != "3" ||
			res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Remaining") != fmt.Sprint(3-i) {
			t.Fatalf("request %d: %d %v", i, res.StatusCode, res.Header)
		}
		if !strings.Contains(res.Header.Get("Access-Control-Expose-Headers"), "X-PocketHost-RateLimit-Instance-Concurrent-Remaining") {
			t.Fatalf("expose headers missing: %q", res.Header.Get("Access-Control-Expose-Headers"))
		}
	}
	if res := get("limited.test", "/api/health", nil); res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("4th request: %d (Retry-After %q)", res.StatusCode, res.Header.Get("Retry-After"))
	}
	// other instances keep the default (1000/hour)
	if res := get("relaxed.test", "/api/health", nil); res.Header.Get("X-PocketHost-RateLimit-Ip-Hourly-Limit") != "1000" {
		t.Fatalf("default limit: %v", res.Header)
	}

	// CORS origins per instance
	if got := get("cors.test", "/api/health", map[string]string{"Origin": "https://app.example.com"}).Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allowed origin: ACAO=%q", got)
	}
	if got := get("cors.test", "/api/health", map[string]string{"Origin": "https://evil.example"}).Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("other origin must not be allowed: ACAO=%q", got)
	}
	if got := get("relaxed.test", "/api/health", map[string]string{"Origin": "https://anything.example"}).Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("default CORS: ACAO=%q", got)
	}

	// JS time limit per instance, through the server
	if res := get("strict.test", "/spin", nil); res.StatusCode == 200 {
		t.Error("strict (max_js_execution: 1s) should be interrupted")
	}
	if res := get("relaxed.test", "/spin", nil); res.StatusCode != 200 {
		t.Errorf("relaxed should finish: %d", res.StatusCode)
	}

	// SetupApp reaches the apps through the server
	req, _ := http.NewRequest(http.MethodGet, base+"/api/host-route", nil)
	req.Host = "relaxed.test"
	res, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "host code for relaxed" {
		t.Errorf("SetupApp route: %d %q", res.StatusCode, body)
	}
}

func fwRequest(remote, path string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://x.test"+path, nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestFirewallPeersAndClientIP(t *testing.T) {
	fw, err := NewFirewall(FirewallConfig{
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

	// Server path: a refused peer gets 403 before anything else
	m := newTestManager(t, false, Instance{ID: "a", Hosts: []string{"a.test"}})
	m.fw = fw
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://a.test/api/health", nil)
	r.RemoteAddr = "8.8.8.8:1"
	m.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("refused peer: %d", w.Code)
	}
}

func TestRateLimitsTrustedWeightsConcurrency(t *testing.T) {
	fw, _ := NewFirewall(FirewallConfig{TrustedClients: []string{"198.51.100.0/24"}})
	limits := func(l Limits) Limits { return l.over(DefaultLimits()) }
	inst := Instance{ID: "a"}

	check := func(remote, path string, lim Limits) (int, func()) {
		w := httptest.NewRecorder()
		rel, ok := fw.rateCheck(w, fwRequest(remote, path, nil), inst, lim)
		if !ok {
			return w.Code, nil
		}
		return 200, rel
	}

	// files cost 1/10: with ip_hourly 1, ten file requests fit, the eleventh doesn't
	lim := limits(Limits{RateLimits: &RateLimits{IPHourly: 1}})
	for i := 0; i < 10; i++ {
		if code, rel := check("192.0.2.1:1", "/api/files/x/y/z.png", lim); code != 200 {
			t.Fatalf("file request %d: %d", i, code)
		} else {
			rel()
		}
	}
	if code, _ := check("192.0.2.1:1", "/api/files/x", lim); code != http.StatusTooManyRequests {
		t.Fatalf("11th file request: %d", code)
	}

	// trusted clients (process list or the instance's own) get the trusted limits
	inst = Instance{ID: "b", Access: Access{TrustedIPs: []string{"203.0.113.9"}}}
	lim = limits(Limits{RateLimits: &RateLimits{IPHourly: 1, Trusted: &RateLimitSet{IPHourly: 3}}})
	for _, ip := range []string{"198.51.100.5", "203.0.113.9"} {
		for i := 0; i < 3; i++ {
			if code, rel := check(ip+":1", "/api/health", lim); code != 200 {
				t.Fatalf("trusted %s request %d: %d", ip, i, code)
			} else {
				rel()
			}
		}
	}
	if code, _ := check("192.0.2.50:1", "/api/health", lim); code != 200 {
		t.Fatal("untrusted first request")
	}
	if code, _ := check("192.0.2.50:1", "/api/health", lim); code != http.StatusTooManyRequests {
		t.Fatal("untrusted second request should hit ip_hourly 1")
	}

	// concurrency: a held request keeps its slot until released
	inst = Instance{ID: "c"}
	lim = limits(Limits{RateLimits: &RateLimits{IPConcurrent: 1, InstanceConcurrent: 2}})
	_, hold := check("192.0.2.1:1", "/api/realtime", lim)
	if code, _ := check("192.0.2.1:1", "/api/health", lim); code != http.StatusTooManyRequests {
		t.Fatal("second concurrent request from the same IP should be refused")
	}
	_, hold2 := check("192.0.2.2:1", "/api/health", lim)
	if code, _ := check("192.0.2.3:1", "/api/health", lim); code != http.StatusTooManyRequests {
		t.Fatal("instance_concurrent 2 should refuse a third")
	}
	hold()
	hold2()
	if code, rel := check("192.0.2.1:1", "/api/health", lim); code != 200 {
		t.Fatal("slot not released")
	} else {
		rel()
	}

	// rate_limits: off
	inst = Instance{ID: "d"}
	lim = limits(Limits{RateLimits: &RateLimits{Off: true}})
	for i := 0; i < 50; i++ {
		if code, _ := check("192.0.2.1:1", "/api/health", lim); code != 200 {
			t.Fatal("rate_limits: off still limited")
		}
	}
}

func TestMaxConcurrentJS(t *testing.T) {
	root := t.TempDir()
	writeHooks(t, root, "busy", `routerAdd("GET", "/work", (e) => { sleep(1500); return e.json(200, {}) })`)
	running, err := startApp(appOptions{PBRoot: root, Defaults: DefaultLimits()}, Instance{
		ID:     "busy",
		Limits: Limits{MaxConcurrentJS: 1, MaxJSExecution: 2 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(running)

	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 50 * time.Millisecond)
			w := httptest.NewRecorder()
			running.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/work", nil))
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	ok, failed := 0, 0
	for _, c := range codes {
		if c == 200 {
			ok++
		} else {
			failed++
		}
	}
	// one slot, 1.5s each, 2s wait: the second waits its turn, the third gives up
	if ok != 2 || failed != 1 {
		t.Fatalf("codes %v: want two served (one after waiting) and one refused", codes)
	}
}

func selfSigned(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	cert, keyPath := filepath.Join(dir, "tls.cert"), filepath.Join(dir, "tls.key")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return cert, keyPath
}

func TestTLSRedirectAndReload(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir, "first.test")
	addr, httpAddr := freeAddr(t), freeAddr(t)
	cfg := DefaultConfig()
	cfg.Listen, cfg.PBRoot, cfg.Dev = addr, filepath.Join(dir, "pb"), true
	cfg.InstancesFile = filepath.Join(dir, "none.yaml")
	cfg.MemorySoftLimit = -1
	cfg.TLS = TLSConfig{Cert: certPath, Key: keyPath, HTTPListen: httpAddr}
	srv := &Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()

	insecure := &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	deadline := time.Now().Add(10 * time.Second)
	var res *http.Response
	var err error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/api/health", nil)
		req.Host = "blog.localhost"
		if res, err = insecure.Do(req); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || res.StatusCode != 200 || res.TLS.PeerCertificates[0].Subject.CommonName != "first.test" {
		t.Fatalf("https: %v %v", res, err)
	}
	res.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://"+httpAddr+"/api/health?x=1", nil)
	req.Host = "blog.localhost"
	res, err = insecure.Do(req)
	if err != nil || res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "https://blog.localhost/api/health?x=1" {
		t.Fatalf("http redirect: %v %v", res, err)
	}
	res.Body.Close()

	// renewed certificate files are picked up without a restart
	r, err := newCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	selfSigned(t, dir, "second.test")
	r.lastCheck = time.Time{}
	c, _ := r.GetCertificate(nil)
	leaf, _ := x509.ParseCertificate(c.Certificate[0])
	if leaf.Subject.CommonName != "second.test" {
		t.Fatalf("reload: still %s", leaf.Subject.CommonName)
	}
}
