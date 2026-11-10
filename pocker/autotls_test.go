package pocker

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoTLSConfigValidation(t *testing.T) {
	t.Setenv("TEST_CF_TOKEN", "token-from-env")
	cases := []struct {
		name    string
		cfg     TLSConfig
		wantErr string
	}{
		{"per-host, no DNS provider", TLSConfig{Auto: true, Email: "ops@example.com"}, ""},
		{"named domains without wildcards, no DNS provider", TLSConfig{Auto: true, Domains: []string{"example.com", "www.example.com"}}, ""},
		{"wildcard without DNS provider", TLSConfig{Auto: true, Domains: []string{"*.example.com"}}, "needs a DNS provider"},
		{"files and auto", TLSConfig{Auto: true, Cert: "a", Key: "b"}, "either cert/key files or auto"},
		{"unknown provider", TLSConfig{Auto: true, DNS: &DNSConfig{Provider: "route66"}}, "unknown provider"},
		{"cloudflare without token", TLSConfig{Auto: true, DNS: &DNSConfig{Provider: "cloudflare"}}, "needs api_token"},
		{"cloudflare token from env, wildcard", TLSConfig{Auto: true, Domains: []string{"*.example.com"}, DNS: &DNSConfig{Provider: "cloudflare", APITokenEnv: "TEST_CF_TOKEN"}}, ""},
	}
	for _, c := range cases {
		err := validateAutoTLS(c.cfg)
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
	auto, err := newAutoTLS(context.Background(), TLSConfig{Auto: true, Email: "ops@example.com"}, t.TempDir(),
		func(h string) bool { return mapped[normalizeHost(h)] })
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
	cfg := DefaultConfig()
	cfg.Listen, cfg.PBRoot, cfg.InstancesFile = addr, root, instances
	cfg.MemorySoftLimit = -1
	cfg.TLS = TLSConfig{Auto: true, Email: "ops@example.com", CA: "letsencrypt-staging", HTTPListen: httpAddr}
	srv := &Server{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// plain HTTP: health passes, the rest redirects to HTTPS
	waitHTTP(t, "http://"+httpAddr+HealthPath, "anything")
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
