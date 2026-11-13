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
	m.dir.Load(list)
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

func TestDevID(t *testing.T) {
	if DevID("BLOG.localhost:8090") != "blog.localhost" || DevID("blog.localhost.") != "blog.localhost" ||
		DevID("blog.localhost.:8090") != "blog.localhost" {
		t.Fatal("DevID must normalize case, port and trailing dot")
	}
	if DevID("bad_host") != "" {
		t.Fatal("unsafe hostnames have no dev id")
	}
}
