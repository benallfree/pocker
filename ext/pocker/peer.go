package pocker

import (
	"net"
	"net/http"
	"net/netip"
)

// peerAddr is the address of the connecting peer (not a client IP from headers).
func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}
