package host

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/netip"
)

// Gate admits requests (e.g. a firewall). AllowPeer runs before routing and refuses with 403; Admit runs before an
// instance's request is served here or forwarded to another server, may answer it itself (ok=false), and otherwise
// returns a release function called when the response ends (long-lived responses hold it until they end).
type Gate interface {
	AllowPeer(r *http.Request) bool
	Admit(w http.ResponseWriter, r *http.Request, inst Instance) (release func(), ok bool)
}

// ClientIPResolver is an optional [Gate] extension: the request's real client (e.g. from CF-Connecting-IP when the
// peer is a trusted proxy). When the gate implements it, a request served here reaches the app with RemoteAddr set
// to that client, so PocketBase's logs, e.RealIP() and its own rate limits see the visitor instead of the proxy,
// without the app having to trust any header.
type ClientIPResolver interface {
	ClientIP(r *http.Request) netip.Addr
}

// TLSProvider supplies the main listener's TLS (e.g. automatic certificates). served reports whether a hostname
// belongs to an instance, for providers that must not issue certificates for anything else. httpHandler, if not
// nil, wraps the plain-HTTP listener's handler (tls.http_listen), e.g. to answer ACME challenges.
type TLSProvider interface {
	TLS(ctx context.Context, served func(host string) bool) (cfg *tls.Config, httpHandler func(http.Handler) http.Handler, err error)
}

// Wrapper wraps the server's handler. Wrappers nest in the order given: the first listed is the outermost.
type Wrapper interface {
	Wrap(next http.Handler) http.Handler
}

// chainGates combines gates: AllowPeer needs every gate, Admit runs them in order and releases in reverse.
func chainGates(gates []Gate) Gate {
	switch len(gates) {
	case 0:
		return nil
	case 1:
		return gates[0]
	}
	return gateChain(gates)
}

type gateChain []Gate

func (c gateChain) AllowPeer(r *http.Request) bool {
	for _, g := range c {
		if !g.AllowPeer(r) {
			return false
		}
	}
	return true
}

func (c gateChain) Admit(w http.ResponseWriter, r *http.Request, inst Instance) (func(), bool) {
	var releases []func()
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, g := range c {
		rel, ok := g.Admit(w, r, inst)
		if !ok {
			releaseAll()
			return nil, false
		}
		releases = append(releases, rel)
	}
	return releaseAll, true
}

// ClientIP is the first gate's answer that implements [ClientIPResolver] (the zero Addr when none does).
func (c gateChain) ClientIP(r *http.Request) netip.Addr {
	for _, g := range c {
		if cr, ok := g.(ClientIPResolver); ok {
			return cr.ClientIP(r)
		}
	}
	return netip.Addr{}
}

// ChainGates combines gates into one (nil when there are none).
func ChainGates(gates ...Gate) Gate { return chainGates(gates) }
