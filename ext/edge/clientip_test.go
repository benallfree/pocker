package edge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type addrRuntime struct{ got string }

func (a *addrRuntime) Name() string         { return "test" }
func (a *addrRuntime) Serves(Instance) bool { return true }
func (a *addrRuntime) Ready() bool          { return true }
func (a *addrRuntime) ServeInstance(w http.ResponseWriter, r *http.Request, _ Instance) {
	a.got = r.RemoteAddr
	w.WriteHeader(http.StatusOK)
}

type noopGate struct{}

func (noopGate) AllowPeer(*http.Request) bool { return true }
func (noopGate) Admit(http.ResponseWriter, *http.Request, Instance) (func(), bool) {
	return func() {}, true
}

// The app sees the visitor (from a trusted proxy's CF-Connecting-IP) as RemoteAddr, also through a gate chain;
// a header from an untrusted peer changes nothing.
func TestServedRequestCarriesClientIP(t *testing.T) {
	fw, err := NewFirewall(FirewallConfig{
		AllowCIDRs:     []string{"173.245.48.0/20", "8.8.8.8"},
		TrustedProxies: []string{"173.245.48.0/20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := NewDirectory(slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir.Load([]Instance{{ID: "a", Hosts: []string{"a.test"}}})
	rt := &addrRuntime{}
	h := &Handler{Dir: dir, Gate: ChainGates(noopGate{}, fw), Runtimes: []Runtime{rt}}

	for _, tc := range []struct{ peer, cf, want string }{
		{"173.245.48.9:4433", "203.0.113.7", "203.0.113.7:4433"},
		{"8.8.8.8:1234", "203.0.113.7", "8.8.8.8:1234"},
		{"173.245.48.9:4433", "", "173.245.48.9:4433"},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://a.test/api/health", nil)
		r.RemoteAddr = tc.peer
		if tc.cf != "" {
			r.Header.Set("CF-Connecting-IP", tc.cf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || rt.got != tc.want {
			t.Errorf("peer %s cf %q: status %d, app saw %q, want %q", tc.peer, tc.cf, w.Code, rt.got, tc.want)
		}
	}
}
