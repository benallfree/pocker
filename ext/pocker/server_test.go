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
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	addr, httpAddr, plainAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	cfg := DefaultConfig()
	cfg.Listen, cfg.PBRoot, cfg.Dev = addr, filepath.Join(dir, "pb"), true
	cfg.PlainListen = []string{plainAddr}
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

	// the plain listener serves the same instances without redirecting
	req, _ = http.NewRequest(http.MethodGet, "http://"+plainAddr+"/api/health", nil)
	req.Host = "blog.localhost"
	res, err = insecure.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("plain listener: %v %v", res, err)
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
