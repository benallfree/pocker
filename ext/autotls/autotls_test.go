package autotls

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pockethost/pocketbase/ext/pocker"
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

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if res, err := testClient.Get(url); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never answered", url)
		}
	}
}

func TestAutoTLSConfigValidation(t *testing.T) {
	t.Setenv("TEST_CF_TOKEN", "token-from-env")
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"per-host, no DNS provider", Config{Auto: true, Email: "ops@example.com"}, ""},
		{"named domains without wildcards, no DNS provider", Config{Auto: true, Domains: []string{"example.com", "www.example.com"}}, ""},
		{"wildcard without DNS provider", Config{Auto: true, Domains: []string{"*.example.com"}}, "needs a DNS provider"},
		{"files and auto", Config{Auto: true}, "either cert/key files or auto"},
		{"unknown provider", Config{Auto: true, DNS: &DNSConfig{Provider: "route66"}}, "unknown provider"},
		{"cloudflare without token", Config{Auto: true, DNS: &DNSConfig{Provider: "cloudflare"}}, "needs api_token"},
		{"cloudflare token from env, wildcard", Config{Auto: true, Domains: []string{"*.example.com"}, DNS: &DNSConfig{Provider: "cloudflare", APITokenEnv: "TEST_CF_TOKEN"}}, ""},
	}
	for _, c := range cases {
		err := validateAutoTLS(c.cfg, c.name == "files and auto")
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: want error containing %q, got %v", c.name, c.wantErr, err)
		}
	}
}

func TestCoveredBy(t *testing.T) {
	domains := []string{"*.example.com", "example.com", "app.other.org"}
	for _, ok := range []string{"a.example.com", "A.Example.com:443", "example.com", "app.other.org"} {
		if !coveredBy(ok, domains) {
			t.Errorf("%q should be covered", ok)
		}
	}
	for _, no := range []string{"a.b.example.com", "badexample.com", "other.org", "x.app.other.org"} {
		if coveredBy(no, domains) {
			t.Errorf("%q should not be covered", no)
		}
	}
}

func TestAutoTLSIssuesOnlyForMappedHosts(t *testing.T) {
	mapped := map[string]bool{"blog.example.com": true}
	auto, err := newAutoTLS(context.Background(), Config{Auto: true, Email: "ops@example.com"}, false, t.TempDir(),
		func(h string) bool { return mapped[normalizeHost(h)] }, true)
	if err != nil {
		t.Fatal(err)
	}
	decide := auto.magic.OnDemand.DecisionFunc
	if err := decide(context.Background(), "blog.example.com"); err != nil {
		t.Errorf("mapped host refused: %v", err)
	}
	if err := decide(context.Background(), "random.example.com"); err == nil {
		t.Error("unmapped host must not get a certificate")
	}
	if c := auto.tlsConfig(); c.NextProtos[0] != "h2" || !containsString(c.NextProtos, "acme-tls/1") {
		t.Errorf("ALPN: %v", c.NextProtos)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestServerAutoTLSRefusesUnmappedSNI(t *testing.T) {
	root := t.TempDir()
	instances := filepath.Join(root, "instances.yaml")
	if err := os.WriteFile(instances, []byte("instances:\n  - id: blog\n    hosts: [blog.example.com]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addr, httpAddr := freeAddr(t), freeAddr(t)
	cfg := pocker.DefaultConfig()
	cfg.Listen, cfg.PBRoot, cfg.InstancesFile = addr, root, instances
	cfg.MemorySoftLimit = -1
	cfg.TLS.HTTPListen = httpAddr
	auto := New()
	auto.Config = Config{Auto: true, Email: "ops@example.com", CA: "letsencrypt-staging"}
	srv := &pocker.Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Plugins: []pocker.Plugin{auto}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// plain HTTP: health passes, the rest redirects to HTTPS
	waitHTTP(t, "http://"+httpAddr+pocker.HealthPath)
	noFollow := &http.Client{Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, "http://"+httpAddr+"/api/health", nil)
	req.Host = "blog.example.com"
	res, err := noFollow.Do(req)
	if err != nil || res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "https://blog.example.com/api/health" {
		t.Fatalf("http redirect: %v %v", res, err)
	}
	res.Body.Close()

	// a TLS handshake for a host no instance serves fails without contacting any CA
	start := time.Now()
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "random.example.com", InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
		t.Fatal("handshake for an unmapped host should fail")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("refusal took %s; it must not wait on ACME", time.Since(start))
	}
}

// testClient has its own transport: the server installs its egress policy on http.DefaultTransport, which refuses
// loopback.
var testClient = &http.Client{Transport: &http.Transport{}}
