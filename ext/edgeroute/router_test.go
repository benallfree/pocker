package edgeroute

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pockethost/pocketbase/ext/edge"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// owner is a fake owner edge: its health path answers {"status":"ok"} and everything else echoes host + path.
func owner(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == edge.DefaultHealthPath {
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		w.Header().Set("X-Powered-By", "owner")
		w.Header().Set("Access-Control-Allow-Origin", "https://owner-says.example")
		_, _ = io.WriteString(w, "owner "+r.Host+r.URL.Path+" from "+r.Header.Get("X-Edge-Forwarded-By"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func serve(rt *Router, inst edge.Instance, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "http://"+inst.Hosts[0]+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w.Header().Set("X-Powered-By", "entry")
	rt.Forward(w, req, inst)
	return w
}

// A YAML/static edge list: forwarding by "edge:@<id>" and by bare id, the owner probed (no heartbeat data).
func TestForwardViaStaticEdges(t *testing.T) {
	o := owner(t)
	edges := &edge.StaticEdges{SelfID: "lax-1", List: []edge.StaticEdge{{ID: "sgp-1", InternalURL: o.URL}}}
	rt := New(Config{Edges: edges, Logger: quiet})
	for _, remote := range []string{"edge:@sgp-1", "sgp-1"} {
		inst := edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: remote}
		w := serve(rt, inst, http.MethodGet, "/api/health", nil)
		if w.Code != 200 || w.Body.String() != "owner x.example.com/api/health from lax-1" {
			t.Fatalf("%s: %d %q", remote, w.Code, w.Body.String())
		}
		if got := w.Header().Values("X-Powered-By"); len(got) != 1 || got[0] != "owner" {
			t.Fatalf("X-Powered-By: %v", got)
		}
	}
}

// An unknown owner, an offline one, and one that doesn't answer its health check: 503.
func TestUnavailableOwners(t *testing.T) {
	o := owner(t)
	dead := "http://" + freeAddr(t)
	edges := sourceFunc{
		"sgp-1": {ID: "sgp-1", InternalURL: o.URL, Status: "offline", LastSeen: time.Now()},
		"ams-1": {ID: "ams-1", InternalURL: dead},
	}
	rt := New(Config{Edges: edges, Logger: quiet, UnavailableMessage: "down"})
	for _, remote := range []string{"nope", "sgp-1", "ams-1"} {
		w := serve(rt, edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: remote}, http.MethodGet, "/", nil)
		if w.Code != http.StatusServiceUnavailable || w.Body.String() != "down" || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: %d %q", remote, w.Code, w.Body.String())
		}
	}
}

// A request another edge already forwarded is never forwarded again (421).
func TestNoBounce(t *testing.T) {
	o := owner(t)
	rt := New(Config{Edges: &edge.StaticEdges{List: []edge.StaticEdge{{ID: "sgp-1", InternalURL: o.URL}}}, Logger: quiet})
	w := serve(rt, edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: "sgp-1"}, http.MethodGet, "/", map[string]string{"X-Edge-Forwarded-By": "ams-1"})
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("bounce: %d", w.Code)
	}
}

// A healthy owner (fresh heartbeat) that refuses the connection: a GET is held and replayed once it's back.
func TestHoldAndReplay(t *testing.T) {
	addr := freeAddr(t)
	var probes atomic.Int32
	edges := sourceFunc{"sgp-1": {ID: "sgp-1", InternalURL: "http://" + addr, Status: "online", LastSeen: time.Now()}}
	rt := New(Config{Edges: edges, Logger: quiet, Retry: 20 * time.Millisecond})
	inner := rt.probe
	rt.probe = func(ctx context.Context, url string, timeout time.Duration) bool { probes.Add(1); return inner(ctx, url, timeout) }

	go func() {
		time.Sleep(150 * time.Millisecond)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == edge.DefaultHealthPath {
				_, _ = io.WriteString(w, `{"status":"ok"}`)
				return
			}
			_, _ = io.WriteString(w, "back")
		})}
		t.Cleanup(func() { _ = srv.Close() })
		_ = srv.Serve(ln)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x.example.com/", nil).WithContext(ctx)
	rt.Forward(w, req, edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: "sgp-1"})
	if w.Code != 200 || w.Body.String() != "back" || probes.Load() == 0 {
		t.Fatalf("held: %d %q (probes %d)", w.Code, w.Body.String(), probes.Load())
	}
	// a POST to a refusing owner is not replayed
	edges["sgp-1"] = edge.Edge{ID: "sgp-1", InternalURL: "http://" + freeAddr(t), Status: "online", LastSeen: time.Now()}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "http://x.example.com/", strings.NewReader("body")).WithContext(ctx)
	rt.Forward(w, req, edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: "sgp-1"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST to a refusing owner: %d", w.Code)
	}
}

// An instance with an origin list: this edge answers preflights and its Allow-Origin replaces the owner's.
func TestCORSAtTheEntryEdge(t *testing.T) {
	o := owner(t)
	rt := New(Config{Edges: &edge.StaticEdges{List: []edge.StaticEdge{{ID: "sgp-1", InternalURL: o.URL}}}, Logger: quiet})
	inst := edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: "sgp-1", Access: edge.Access{CORSOrigins: []string{"https://app.example"}}}
	w := serve(rt, inst, http.MethodOptions, "/api/x", map[string]string{"Origin": "https://app.example", "Access-Control-Request-Method": "POST"})
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	w = serve(rt, inst, http.MethodGet, "/api/x", map[string]string{"Origin": "https://app.example"})
	if got := w.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "https://app.example" {
		t.Fatalf("allow-origin: %v", got)
	}
}

// MultiEdges: the first source that resolves a Remote wins (YAML entries next to a mirror).
func TestMultiEdges(t *testing.T) {
	o := owner(t)
	m := edge.MultiEdges{&edge.StaticEdges{SelfID: "lax-1", List: []edge.StaticEdge{{ID: "lab-1", InternalURL: o.URL}}},
		sourceFunc{"sgp-1": {ID: "sgp-1", InternalURL: o.URL, Status: "online", LastSeen: time.Now()}}}
	rt := New(Config{Edges: m, Logger: quiet})
	for _, remote := range []string{"lab-1", "sgp-1"} {
		if w := serve(rt, edge.Instance{ID: "x", Hosts: []string{"x.example.com"}, Remote: remote}, http.MethodGet, "/", nil); w.Code != 200 {
			t.Fatalf("%s: %d", remote, w.Code)
		}
	}
	if m.Self() != "lax-1" || len(m.Edges()) != 2 {
		t.Fatalf("self %q edges %d", m.Self(), len(m.Edges()))
	}
}

// sourceFunc is an EdgeSource with heartbeat data, like a control plane's mirror; Remote is the edge id.
type sourceFunc map[string]edge.Edge

func (s sourceFunc) Self() string { return "lax-1" }
func (s sourceFunc) Resolve(remote string) (edge.Edge, bool) {
	e, ok := s[remote]
	return e, ok
}
func (s sourceFunc) Edges() []edge.Edge {
	var out []edge.Edge
	for _, e := range s {
		out = append(out, e)
	}
	return out
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}
