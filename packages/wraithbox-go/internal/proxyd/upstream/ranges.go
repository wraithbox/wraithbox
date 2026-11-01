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
// NAT64 forms aren't entries: embeddedIPv4 takes the IPv4 address out
// of them, and that address is checked against the IPv4 entries. 6to4
// and Teredo are entries, so they are refused whole, whatever address
// they embed.
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
}

// The IPv6 prefixes that embed an IPv4 address (S07-07-hosts-and-modes,
// "Upstream address"). IPv4-mapped addresses are found with Is4In6.
var (
	compatPrefix   = netip.MustParsePrefix("::/96")
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse  = netip.MustParsePrefix("64:ff9b:1::/48")
)

// nat64Positions are the byte offsets of the IPv4 address inside an
// IPv6 address for each NAT64 prefix length from /48 to /96 (RFC 6052,
// section 2.2). Byte 8 is never part of it. The local-use prefix
// 64:ff9b:1::/48 can be split into any of these lengths, and the
// dialer can't tell which one the network uses, so it checks all of
// them.
var nat64Positions = [][4]int{
	{6, 7, 9, 10},    // /48
	{7, 9, 10, 11},   // /56
	{9, 10, 11, 12},  // /64
	{12, 13, 14, 15}, // /96
}

// embeddedIPv4 returns the IPv4 addresses an IPv6 address carries in
// its IPv4-mapped, IPv4-compatible or NAT64 form, or nil.
func embeddedIPv4(a netip.Addr) []netip.Addr {
	if !a.Is6() {
		return nil
	}
	b := a.As16()
	at := func(p [4]int) netip.Addr {
		return netip.AddrFrom4([4]byte{b[p[0]], b[p[1]], b[p[2]], b[p[3]]})
	}
	last := [4]int{12, 13, 14, 15}
	switch {
	case a.Is4In6(), compatPrefix.Contains(a), nat64WellKnown.Contains(a):
		return []netip.Addr{at(last)}
	case nat64LocalUse.Contains(a):
		out := make([]netip.Addr, 0, len(nat64Positions))
		for _, p := range nat64Positions {
			out = append(out, at(p))
		}
		return out
	}
	return nil
}

// forms returns the address, without its zone, followed by every IPv4
// address it embeds. Each is checked: a refusal of any refuses the
// address.
func forms(a netip.Addr) []netip.Addr {
	a = a.WithZone("")
	return append([]netip.Addr{a}, embeddedIPv4(a)...)
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

// Refused reports whether S07-07-hosts-and-modes, "Upstream address",
// refuses the address by its ranges alone, and the name of the range
// that does. The host's own networks aren't part of it: those are read
// at each connect, in Dialer.Dial.
func Refused(a netip.Addr) (rangeName string, refused bool) {
	r, ok := match(defaultRanges, a)
	return r.name, ok
}
