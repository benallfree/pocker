package host

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

// resolverGate trusts X-Test-Client from 10.0.0.0/8 peers, like the firewall trusts CF-Connecting-IP from Cloudflare.
type resolverGate struct{ noopGate }

func (resolverGate) ClientIP(r *http.Request) netip.Addr {
	peer := PeerAddr(r)
	if netip.MustParsePrefix("10.0.0.0/8").Contains(peer) {
		if a, err := netip.ParseAddr(r.Header.Get("X-Test-Client")); err == nil {
			return a
		}
	}
	return peer
}

// The app sees the gate's client as RemoteAddr (also through a gate chain); without one, RemoteAddr is unchanged.
func TestServedRequestCarriesClientIP(t *testing.T) {
	dir := NewDirectory(slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir.Load([]Instance{{ID: "a", Hosts: []string{"a.test"}}})
	rt := &addrRuntime{}

	for _, tc := range []struct {
		gate               Gate
		peer, client, want string
	}{
		{ChainGates(noopGate{}, resolverGate{}), "10.1.2.3:4433", "203.0.113.7", "203.0.113.7:4433"},
		{ChainGates(noopGate{}, resolverGate{}), "8.8.8.8:1234", "203.0.113.7", "8.8.8.8:1234"},
		{resolverGate{}, "10.1.2.3:4433", "", "10.1.2.3:4433"},
		{noopGate{}, "10.1.2.3:4433", "203.0.113.7", "10.1.2.3:4433"},
	} {
		h := &Handler{Dir: dir, Gate: tc.gate, Runtimes: []Runtime{rt}}
		r := httptest.NewRequest(http.MethodGet, "http://a.test/api/health", nil)
		r.RemoteAddr = tc.peer
		if tc.client != "" {
			r.Header.Set("X-Test-Client", tc.client)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || rt.got != tc.want {
			t.Errorf("peer %s client %q: status %d, app saw %q, want %q", tc.peer, tc.client, w.Code, rt.got, tc.want)
		}
	}
}
