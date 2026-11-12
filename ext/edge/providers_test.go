package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// A fake mothership: superuser login, then the edges collection; tokens expire on demand.
func fakeMothership(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	logins := &atomic.Int32{}
	expire := &atomic.Bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/collections/_superusers/auth-with-password", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["identity"] != "su@test" || in["password"] != "pw" {
			http.Error(w, `{"message":"bad"}`, http.StatusBadRequest)
			return
		}
		logins.Add(1)
		expire.Store(false)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("GET /api/collections/edges/records", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "tok" || expire.Load() {
			http.Error(w, `{}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{
			{"id": "rec1", "edge_id": "sgp-1", "internal_url": "http://10.88.0.4:8080", "region": "sgp", "status": "online", "last_seen": "2026-11-12 07:00:00.000Z"},
			{"id": "rec2", "edge_id": "ams-1", "internal_url": "http://10.88.0.3:8080", "status": "offline"},
		}})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s, logins, expire
}

func TestMothershipProvider(t *testing.T) {
	s, logins, expire := fakeMothership(t)
	p := &MothershipProvider{URL: s.URL, Email: "su@test", Password: "pw", SelfID: "lax-1", Logger: quietLog}
	if _, ok := p.Resolve("edge:rec1"); ok || p.Loaded() {
		t.Fatal("knows edges before the first read")
	}
	if err := p.RefreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, remote := range []string{"edge:rec1", "edge:@sgp-1", "sgp-1"} {
		e, ok := p.Resolve(remote)
		if !ok || e.ID != "sgp-1" || e.InternalURL != "http://10.88.0.4:8080" || e.Status != "online" || e.LastSeen.IsZero() {
			t.Fatalf("resolve %q: %+v %v", remote, e, ok)
		}
	}
	if len(p.Edges()) != 2 || p.Self() != "lax-1" {
		t.Fatalf("edges %v self %q", p.Edges(), p.Self())
	}
	// an expired token: log in again once
	expire.Store(true)
	if err := p.RefreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins: %d", logins.Load())
	}
	// a bad login keeps the last good list
	p2 := &MothershipProvider{URL: s.URL, Email: "su@test", Password: "nope", Logger: quietLog}
	if err := p2.RefreshNow(context.Background()); err == nil {
		t.Fatal("bad login accepted")
	}
}

func TestEdgesFromConfig(t *testing.T) {
	s, _, _ := fakeMothership(t)
	t.Setenv("MS_PASSWORD", "pw")
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
edges:
  self: lax-1
  list:
    - { id: sgp-1, internal_url: "http://override:8080" }
  mothership:
    url: `+s.URL+`
    email: su@test
    password: $MS_PASSWORD
    refresh: 50ms
`), &doc); err != nil {
		t.Fatal(err)
	}
	src, err := EdgesFromConfig(TopLevel(&doc, "edges"), quietLog)
	if err != nil {
		t.Fatal(err)
	}
	multi, ok := src.(MultiProvider)
	if !ok || len(multi) != 2 {
		t.Fatalf("got %T %v", src, src)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { multi.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	mp := multi[1].(*MothershipProvider)
	for deadline := time.Now().Add(3 * time.Second); !mp.Loaded(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("mothership never loaded")
		}
	}
	if e, _ := src.Resolve("sgp-1"); e.InternalURL != "http://override:8080" {
		t.Fatalf("YAML entry should win: %+v", e)
	}
	if e, ok := src.Resolve("edge:rec2"); !ok || e.ID != "ams-1" {
		t.Fatalf("mothership entry: %+v %v", e, ok)
	}
	if src.Self() != "lax-1" || len(src.Edges()) != 2 {
		t.Fatalf("self %q edges %v", src.Self(), src.Edges())
	}

	// list only: a plain YAMLProvider; bad entries are refused
	yaml.Unmarshal([]byte("edges: { self: a, list: [ { id: b, internal_url: 'http://b' } ] }"), &doc)
	if src, _ := EdgesFromConfig(TopLevel(&doc, "edges"), quietLog); src == nil {
		t.Fatal("nil")
	} else if _, ok := src.(*YAMLProvider); !ok {
		t.Fatalf("got %T", src)
	}
	yaml.Unmarshal([]byte("edges: { list: [ { id: b } ] }"), &doc)
	if _, err := EdgesFromConfig(TopLevel(&doc, "edges"), quietLog); err == nil {
		t.Fatal("entry without internal_url accepted")
	}
}
