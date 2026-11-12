package sandbox

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

// Egress policy: every outbound connection an app can cause (JS $http.send and $filesystem.fileFromURL, S3 storage
// and backups, OAuth2 and JWK fetches, SMTP) may only reach public addresses. The check runs in the dialer's Control
// hook, on the address actually being connected to after DNS resolution, so DNS rebinding can't get around it.
//
// Pocker installs it process-wide: Go's default HTTP transport (used by PocketBase's HTTP clients) and PocketBase's
// SMTP client dial through it. Defence in depth for anything else is an OS firewall on Pocker's user.

// blockedPrefixes are never reachable unless listed in an allow list.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32",
		"2002::/16", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// EgressPolicy decides which addresses apps may connect to.
type EgressPolicy struct {
	// Allow lists networks that are reachable even though they are private (e.g. an internal SMTP relay).
	Allow []netip.Prefix
}

// Permits reports whether addr may be dialed.
func (p EgressPolicy) Permits(addr netip.Addr) bool {
	addr = addr.Unmap() // IPv4-mapped IPv6 is checked as IPv4
	for _, a := range p.Allow {
		if a.Contains(addr) {
			return true
		}
	}
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	for _, b := range blockedPrefixes {
		if b.Contains(addr) {
			return false
		}
	}
	return true
}

// Dialer returns a net.Dialer that refuses connections the policy doesn't permit.
func (p EgressPolicy) Dialer() *net.Dialer {
	return policyDialer(func() EgressPolicy { return p })
}

func policyDialer(current func() EgressPolicy) *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || !current().Permits(addr) {
				return fmt.Errorf("egress to %s is not allowed on this host", address)
			}
			return nil
		},
	}
}

var (
	activeEgress atomic.Pointer[EgressPolicy]
	wireEgress   sync.Once
)

// InstallEgressPolicy routes Go's default HTTP transport and PocketBase's SMTP client through the policy, and caps
// $http.send. It applies to the whole process; calling it again replaces the policy (e.g. on a config reload).
func InstallEgressPolicy(p EgressPolicy, httpMaxTimeoutSec int, httpMaxBody int64) {
	activeEgress.Store(&p)
	jsvm.HTTPSendMaxTimeout = httpMaxTimeoutSec
	jsvm.HTTPSendMaxBodySize = httpMaxBody
	wireEgress.Do(func() {
		d := policyDialer(func() EgressPolicy { return *activeEgress.Load() })
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			t.DialContext = d.DialContext
			t.Proxy = nil // a proxy from the environment would bypass the per-address check
		}
		mailer.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return d.DialContext(ctx, network, address)
		}
	})
}
