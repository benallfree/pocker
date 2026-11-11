package pocker

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteInstanceIsForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "upstream "+r.Host+r.URL.Path)
	}))
	defer upstream.Close()

	root := t.TempDir()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	remote := Instance{ID: "away", Hosts: []string{"away.example.com"}, Remote: upstream.URL}
	get := func(m *Manager) (int, string) {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://away.example.com/x", nil))
		return w.Code, w.Body.String()
	}

	// default: a reverse proxy to Remote; no app starts and no folder is created here
	m := NewManager(staticSource{remote}, ManagerOptions{PBRoot: root, Logger: quiet})
	m.mu.Lock()
	m.upsertLocked(remote)
	m.ready = true
	m.mu.Unlock()
	if code, body := get(m); code != 200 || body != "upstream away.example.com/x" {
		t.Fatalf("default forward: %d %q", code, body)
	}
	if m.Running("away") {
		t.Fatal("remote instance started locally")
	}
	if _, err := os.Stat(filepath.Join(root, "away")); !os.IsNotExist(err) {
		t.Fatalf("remote instance folder created: %v", err)
	}

	// Forward replaces the proxy, after the rate limits
	fw, err := NewFirewall(FirewallConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var forwarded []string
	limited := remote
	limited.Limits.RateLimits = &RateLimits{IPHourly: 1, InstanceHourly: 100, IPConcurrent: 10, InstanceConcurrent: 10}
	m = NewManager(staticSource{limited}, ManagerOptions{PBRoot: root, Logger: quiet, Firewall: fw,
		Forward: func(w http.ResponseWriter, r *http.Request, inst Instance) {
			forwarded = append(forwarded, inst.ID)
			_, _ = io.WriteString(w, "forwarded")
		}})
	m.mu.Lock()
	m.upsertLocked(limited)
	m.ready = true
	m.mu.Unlock()
	if code, body := get(m); code != 200 || body != "forwarded" {
		t.Fatalf("custom forward: %d %q", code, body)
	}
	if code, _ := get(m); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit before forwarding: got %d", code)
	}
	if strings.Join(forwarded, ",") != "away" {
		t.Fatalf("forwarded %v", forwarded)
	}
}
