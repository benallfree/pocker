package pocker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticSource []Instance

func (s staticSource) Snapshot(context.Context) ([]Instance, error) { return s, nil }
func (s staticSource) Watch(context.Context) (<-chan Change, error) { return nil, nil }

func newTestManager(t *testing.T, dev bool, list ...Instance) *Manager {
	t.Helper()
	m := NewManager(staticSource(list), ManagerOptions{
		PBRoot: t.TempDir(),
		Dev:    dev,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	m.mu.Lock()
	for _, inst := range list {
		m.upsertLocked(inst)
	}
	m.ready = true
	m.mu.Unlock()
	return m
}

func req(host string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "http://"+host+"/api/health", nil)
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
		host       string
		wantID     string
		wantStatus int
	}{
		{"blog.example.com", "blog", 0},
		{"WWW.Blog.Example.com:443", "blog", 0},
		{"shop.example.com", "shop", 0},
		{"other.example.com", "", http.StatusNotFound},
		{"../etc", "", http.StatusNotFound},
	}
	for _, c := range cases {
		inst, status, _ := m.resolve(req(c.host))
		if status != c.wantStatus || inst.ID != c.wantID {
			t.Errorf("%s: got id=%q status=%d, want id=%q status=%d", c.host, inst.ID, status, c.wantID, c.wantStatus)
		}
	}
	if _, ok := m.Lookup("other.example.com"); ok {
		t.Fatal("production must not create instances for unknown hosts")
	}
}

func TestResolveDev(t *testing.T) {
	m := newTestManager(t, true, Instance{ID: "mapped", Hosts: []string{"mapped.localhost"}})

	inst, status, _ := m.resolve(req("Blog.Localhost:8090"))
	if status != 0 || inst.ID != DevID("blog.localhost") || !inst.Dev || !ValidID(inst.ID) {
		t.Fatalf("dev host: got %+v status=%d", inst, status)
	}
	if again, _, _ := m.resolve(req("blog.localhost")); again.ID != inst.ID {
		t.Fatal("same host must map to the same dev id")
	}
	if other, _, _ := m.resolve(req("shop.localhost")); other.ID == inst.ID {
		t.Fatal("different hosts must get different dev ids")
	}
	if mapped, _, _ := m.resolve(req("mapped.localhost")); mapped.ID != "mapped" {
		t.Fatalf("a mapped host wins in dev too, got %q", mapped.ID)
	}
	if weird, status, _ := m.resolve(req("../../etc")); status != 0 || !ValidID(weird.ID) {
		t.Fatalf("any hostname hashes to a safe id, got %+v status=%d", weird, status)
	}
}

func TestDevIDStable(t *testing.T) {
	if DevID("blog.localhost") != DevID("BLOG.localhost:8090") {
		t.Fatal("DevID must normalize case and port")
	}
	if len(DevID("x")) != 16 {
		t.Fatal("DevID is 16 hex chars")
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
