package pocker

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticSource []Instance

func (s staticSource) Snapshot(context.Context) ([]Instance, error) { return s, nil }
func (s staticSource) Watch(context.Context) (<-chan Change, error) { return nil, nil }

func newTestManager(t *testing.T, dev bool, list ...Instance) *Manager {
	t.Helper()
	trusted, err := parseCIDRs([]string{"127.0.0.1/32", "::1/128"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(staticSource(list), ManagerOptions{
		PBRoot:         t.TempDir(),
		Dev:            dev,
		TrustedProxies: trusted,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	m.mu.Lock()
	for _, inst := range list {
		m.upsertLocked(inst)
	}
	m.ready = true
	m.mu.Unlock()
	return m
}

func req(host, remote, idHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/health", nil)
	r.RemoteAddr = remote
	if idHeader != "" {
		r.Header.Set(IDHeader, idHeader)
	}
	return r
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"a", "blog.localhost", "3f9a2c", "shop-1.example.com"} {
		if !ValidID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "..", "a..b", "../etc", "a/b", "A", "-a", "a-", ".a", "a b", "a\\b"} {
		if ValidID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestResolveProduction(t *testing.T) {
	m := newTestManager(t, false,
		Instance{ID: "blog", Hosts: []string{"blog.example.com", "www.blog.example.com"}},
		Instance{ID: "shop", Hosts: []string{"shop.example.com"}},
	)

	cases := []struct {
		name, host, remote, header string
		wantID                     string
		wantStatus                 int
	}{
		{"mapped host", "blog.example.com", "203.0.113.5:1234", "", "blog", 0},
		{"host with port and case", "WWW.Blog.Example.com:443", "203.0.113.5:1234", "", "blog", 0},
		{"unknown host", "other.example.com", "203.0.113.5:1234", "", "", http.StatusNotFound},
		{"header from loopback", "blog.example.com", "127.0.0.1:5555", "shop", "shop", 0},
		{"header from the internet is ignored", "blog.example.com", "203.0.113.5:1234", "shop", "blog", 0},
		{"header for unknown id", "blog.example.com", "127.0.0.1:5555", "nope", "", http.StatusNotFound},
		{"unsafe header id", "blog.example.com", "127.0.0.1:5555", "../etc", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := req(c.host, c.remote, c.header)
			inst, status, _ := m.resolve(r)
			if status != c.wantStatus || inst.ID != c.wantID {
				t.Fatalf("got id=%q status=%d, want id=%q status=%d", inst.ID, status, c.wantID, c.wantStatus)
			}
			if r.Header.Get(IDHeader) != "" {
				t.Fatalf("%s must be stripped before reaching the app", IDHeader)
			}
		})
	}

	if _, ok := m.Lookup("other.example.com"); ok {
		t.Fatal("production must not create instances for unknown hosts")
	}
}

func TestResolveDev(t *testing.T) {
	m := newTestManager(t, true, Instance{ID: "mapped", Hosts: []string{"mapped.localhost"}})

	inst, status, _ := m.resolve(req("Blog.Localhost:8090", "203.0.113.5:1", ""))
	if status != 0 || inst.ID != "blog.localhost" || !inst.Dev {
		t.Fatalf("dev host: got %+v status=%d", inst, status)
	}
	if again, _ := m.Lookup("blog.localhost"); again.ID != "blog.localhost" {
		t.Fatal("dev instance should be registered for later requests")
	}

	inst, status, _ = m.resolve(req("anything.localhost", "203.0.113.5:1", "by-header"))
	if status != 0 || inst.ID != "by-header" {
		t.Fatalf("dev header: got %+v status=%d", inst, status)
	}

	inst, _, _ = m.resolve(req("mapped.localhost", "203.0.113.5:1", ""))
	if inst.ID != "mapped" {
		t.Fatalf("mapped host should win in dev too, got %q", inst.ID)
	}

	if _, status, _ = m.resolve(req("bad_host", "203.0.113.5:1", "")); status != http.StatusBadRequest {
		t.Fatalf("unsafe dev hostname: got status %d", status)
	}
}

func TestParseCIDRs(t *testing.T) {
	nets, err := parseCIDRs([]string{"10.0.0.0/8", "192.0.2.7", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	if !nets[1].Contains(net.ParseIP("192.0.2.7")) || nets[1].Contains(net.ParseIP("192.0.2.8")) {
		t.Fatal("bare IPv4 should be a /32")
	}
	if _, err := parseCIDRs([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestYAMLSourceRequired(t *testing.T) {
	s := &YAMLSource{Path: t.TempDir() + "/missing.yaml", Required: true}
	if _, err := s.Snapshot(context.Background()); err == nil {
		t.Fatal("production must fail without an instances file")
	}
	s.Required = false
	if list, err := s.Snapshot(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("dev: got %v, %v", list, err)
	}
}

func TestDiffInstances(t *testing.T) {
	prev := []Instance{{ID: "a"}, {ID: "b", Dev: true}}
	next := []Instance{{ID: "b", Dev: false}, {ID: "c"}}
	changes := diffInstances(prev, next)
	got := map[string]ChangeKind{}
	for _, c := range changes {
		got[c.Instance.ID] = c.Kind
	}
	if len(got) != 3 || got["a"] != Delete || got["b"] != Upsert || got["c"] != Upsert {
		t.Fatalf("unexpected changes: %+v", changes)
	}
}
