package sandbox

import (
	"net/netip"
	"testing"
)

func TestEgressPolicyPermits(t *testing.T) {
	p := EgressPolicy{Allow: []netip.Prefix{netip.MustParsePrefix("10.99.0.5/32")}}
	denied := []string{
		"127.0.0.1", "10.88.0.10", "172.16.3.4", "192.168.1.1", "169.254.169.254", "100.64.1.1", "0.0.0.0",
		"::1", "::ffff:127.0.0.1", "::ffff:10.88.0.10", "fd00::1", "fe80::1", "64:ff9b::a58:a", "2002:7f00:1::",
		"224.0.0.1", "255.255.255.255",
	}
	for _, s := range denied {
		if p.Permits(netip.MustParseAddr(s)) {
			t.Errorf("%s should be denied", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "10.99.0.5"} {
		if !p.Permits(netip.MustParseAddr(s)) {
			t.Errorf("%s should be permitted", s)
		}
	}
}
