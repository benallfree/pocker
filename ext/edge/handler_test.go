package edge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler(dev bool, list ...Instance) *Handler {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDirectory(quiet)
	d.Load(list)
	return &Handler{Dir: d, Dev: dev, Logger: quiet}
}

func req(host string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "http://"+host+"/api/health", nil)
}

func TestResolveProduction(t *testing.T) {
	h := newTestHandler(false,
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
		inst, status, _ := h.resolve(req(c.host))
		if status != c.wantStatus || inst.ID != c.wantID {
			t.Errorf("%s: got id=%q status=%d, want id=%q status=%d", c.host, inst.ID, status, c.wantID, c.wantStatus)
		}
	}
	if _, ok := h.Dir.Lookup("other.example.com"); ok {
		t.Fatal("production must not create instances for unknown hosts")
	}
}

func TestResolveDev(t *testing.T) {
	h := newTestHandler(true, Instance{ID: "mapped", Hosts: []string{"mapped.localhost"}})

	inst, status, _ := h.resolve(req("Blog.Localhost:8090"))
	if status != 0 || inst.ID != "blog.localhost" || !inst.Dev {
		t.Fatalf("dev host: got %+v status=%d", inst, status)
	}
	if again, _, _ := h.resolve(req("blog.localhost.")); again.ID != "blog.localhost" {
		t.Fatal("same host in another form must map to the same id")
	}
	if mapped, _, _ := h.resolve(req("mapped.localhost")); mapped.ID != "mapped" {
		t.Fatalf("a mapped host wins in dev too, got %q", mapped.ID)
	}
	for _, bad := range []string{"bad_host", "a..b", "-x.localhost"} {
		if _, status, _ := h.resolve(req(bad)); status != http.StatusBadRequest {
			t.Errorf("%q: got status %d, want 400", bad, status)
		}
	}
}
