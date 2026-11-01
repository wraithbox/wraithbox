package upstream

import (
	"net/netip"
	"testing"
)

// One address inside and one just outside each range of
// S07-07-hosts-and-modes, "Upstream address", the embedded IPv4 forms,
// and public addresses that pass (C02-streams-and-tls, "Upstream
// addresses").
func TestRefused(t *testing.T) {
	tests := []struct {
		addr string
		want string // range name, or "" when the address passes
	}{
		{"0.0.0.0", "ipv4-this-network"},
		{"0.255.255.255", "ipv4-this-network"},
		{"1.0.0.0", ""},
		{"127.0.0.1", "ipv4-loopback"},
		{"127.255.255.255", "ipv4-loopback"},
		{"126.255.255.255", ""},
		{"128.0.0.0", ""},
		{"10.0.0.0", "ipv4-private"},
		{"10.255.255.255", "ipv4-private"},
		{"9.255.255.255", ""},
		{"11.0.0.0", ""},
		{"172.16.0.0", "ipv4-private"},
		{"172.31.255.255", "ipv4-private"},
		{"172.15.255.255", ""},
		{"172.32.0.0", ""},
		{"192.168.0.1", "ipv4-private"},
		{"192.167.255.255", ""},
		{"192.169.0.0", ""},
		{"100.64.0.0", "ipv4-cgnat"},
		{"100.127.255.255", "ipv4-cgnat"},
		{"100.63.255.255", ""},
		{"100.128.0.0", ""},
		{"169.254.169.254", "ipv4-link-local"},
		{"169.253.255.255", ""},
		{"169.255.0.0", ""},
		{"192.0.0.8", "ipv4-ietf-protocol"},
		{"192.0.0.255", "ipv4-ietf-protocol"},
		{"191.255.255.255", ""},
		{"192.0.1.0", ""},
		{"224.0.0.1", "ipv4-multicast"},
		{"239.255.255.255", "ipv4-multicast"},
		{"223.255.255.255", ""},
		{"240.0.0.0", "ipv4-reserved"},
		{"255.255.255.255", "ipv4-reserved"},
		{"198.18.0.0", "ipv4-synthetic"},
		{"198.19.255.255", "ipv4-synthetic"},
		{"198.17.255.255", ""},
		{"198.20.0.0", ""},
		{"::", "ipv6-unspecified"},
		{"::1", "ipv6-loopback"},
		{"fe80::1", "ipv6-link-local"},
		{"fe80::1%en0", "ipv6-link-local"},
		{"feb0::1", "ipv6-link-local"},
		{"fe7f:ffff::", ""},
		{"fec0::1", ""},
		{"fc00::1", "ipv6-unique-local"},
		{"fdf0::1", "ipv6-unique-local"},
		{"fbf0::1", ""},
		{"ff02::1", "ipv6-multicast"},
		{"fef0::1", ""},
		{"::ffff:127.0.0.1", "ipv4-loopback"},
		{"::ffff:169.254.169.254", "ipv4-link-local"},
		{"::ffff:8.8.8.8", ""},
		{"::127.0.0.1", "ipv4-loopback"},
		{"::10.1.2.3", "ipv4-private"},
		{"::8.8.8.8", ""},
		{"64:ff9b::a00:1", "ipv4-private"},
		{"64:ff9b::808:808", ""},
		// Local-use NAT64 is refused whole: an RFC 6052 /96 address
		// embedding 8.8.8.8, and 8.8.8.8 at every position.
		{"64:ff9b:1::808:808", "ipv6-nat64-local-use"},
		{"64:ff9b:1:808:8:808:808:808", "ipv6-nat64-local-use"},
		{"64:ff9b:2::808:808", ""},
		// 6to4 and Teredo embedding 8.8.8.8 are refused whole.
		{"2002:808:808::1", "ipv6-6to4"},
		{"2001:0:4136:e378:8000:63bf:f7f7:f7f7", "ipv6-teredo"},
		{"2003::1", ""},
		{"2001:1::1", ""},
		{"8.8.8.8", ""},
		{"1.1.1.1", ""},
		{"2606:4700:4700::1111", ""},
		{"2a00:1450:4001::1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, refused := rangeRefused(netip.MustParseAddr(tt.addr))
			if refused != (tt.want != "") || got != tt.want {
				t.Fatalf("rangeRefused(%s) = %q, %v; want %q", tt.addr, got, refused, tt.want)
			}
		})
	}
}

// An address that isn't valid has nothing to check, so it is refused.
func TestRefusedInvalid(t *testing.T) {
	if _, refused := rangeRefused(netip.Addr{}); !refused {
		t.Fatal("the zero Addr passed")
	}
}
