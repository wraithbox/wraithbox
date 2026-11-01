package upstream

import "net/netip"

// addrRange is one refused range of S07-07-hosts-and-modes, "Upstream
// address", with the name the refusal log gives it.
type addrRange struct {
	name   string
	prefix netip.Prefix
}

// defaultRanges is the table of S07-07-hosts-and-modes, "Upstream
// address" (SEC05-default-deny). The IPv4-mapped, IPv4-compatible and
// well-known NAT64 forms aren't entries: embeddedIPv4 takes the IPv4
// address out of them, and that address is checked against the IPv4
// entries. 6to4, Teredo and the local-use NAT64 prefix are entries, so
// they are refused whole, whatever address they embed. Where the
// local-use prefix puts the IPv4 address depends on how the network
// splits it (RFC 6052, section 2.2), which the dialer can't know.
var defaultRanges = []addrRange{
	{"ipv4-this-network", netip.MustParsePrefix("0.0.0.0/8")},
	{"ipv4-loopback", netip.MustParsePrefix("127.0.0.0/8")},
	{"ipv4-private", netip.MustParsePrefix("10.0.0.0/8")},
	{"ipv4-private", netip.MustParsePrefix("172.16.0.0/12")},
	{"ipv4-private", netip.MustParsePrefix("192.168.0.0/16")},
	{"ipv4-cgnat", netip.MustParsePrefix("100.64.0.0/10")},
	{"ipv4-link-local", netip.MustParsePrefix("169.254.0.0/16")},
	{"ipv4-ietf-protocol", netip.MustParsePrefix("192.0.0.0/24")},
	{"ipv4-multicast", netip.MustParsePrefix("224.0.0.0/4")},
	{"ipv4-reserved", netip.MustParsePrefix("240.0.0.0/4")},
	{"ipv4-synthetic", netip.MustParsePrefix("198.18.0.0/15")},
	{"ipv6-unspecified", netip.MustParsePrefix("::/128")},
	{"ipv6-loopback", netip.MustParsePrefix("::1/128")},
	{"ipv6-link-local", netip.MustParsePrefix("fe80::/10")},
	{"ipv6-unique-local", netip.MustParsePrefix("fc00::/7")},
	{"ipv6-multicast", netip.MustParsePrefix("ff00::/8")},
	{"ipv6-6to4", netip.MustParsePrefix("2002::/16")},
	{"ipv6-teredo", netip.MustParsePrefix("2001::/32")},
	{"ipv6-nat64-local-use", netip.MustParsePrefix("64:ff9b:1::/48")},
}

// The IPv6 prefixes that carry an IPv4 address in their last four
// bytes (S07-07-hosts-and-modes, "Upstream address"). IPv4-mapped
// addresses are found with Is4In6.
var (
	compatPrefix   = netip.MustParsePrefix("::/96")
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
)

// embeddedIPv4 returns the IPv4 address an IPv6 address carries in its
// IPv4-mapped, IPv4-compatible or well-known NAT64 form.
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	if !a.Is4In6() && !compatPrefix.Contains(a) && !nat64WellKnown.Contains(a) {
		return netip.Addr{}, false
	}
	b := a.As16()
	return netip.AddrFrom4([4]byte(b[12:16])), true
}

// forms returns the address, without its zone, followed by the IPv4
// address it embeds, if any. Each is checked: a refusal of either
// refuses the address.
func forms(a netip.Addr) []netip.Addr {
	a = a.WithZone("")
	if v4, ok := embeddedIPv4(a); ok {
		return []netip.Addr{a, v4}
	}
	return []netip.Addr{a}
}

// match reports the first range of the table that holds the address or
// an IPv4 address it embeds. An address that isn't valid is refused,
// since there is nothing to check.
func match(ranges []addrRange, a netip.Addr) (addrRange, bool) {
	if !a.IsValid() {
		return addrRange{name: "invalid-address"}, true
	}
	for _, f := range forms(a) {
		for _, r := range ranges {
			if r.prefix.Contains(f) {
				return r, true
			}
		}
	}
	return addrRange{}, false
}

// rangeRefused reports whether S07-07-hosts-and-modes, "Upstream
// address", refuses the address by its ranges alone, and the name of
// the range that does. It is unexported so no caller takes it for the
// whole check: the host's own networks are read at each connect, in
// Dialer.Dial. The tests and FuzzUpstreamAddress use it.
func rangeRefused(a netip.Addr) (rangeName string, refused bool) {
	r, ok := match(defaultRanges, a)
	return r.name, ok
}
