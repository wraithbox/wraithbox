package upstream

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

// v4Refused is an oracle for the IPv4 ranges of S07-07-hosts-and-modes,
// "Upstream address", written as masks over the address as a number
// and not over the table, so the fuzz target checks the table against
// a second reading of the spec.
func v4Refused(b [4]byte) bool {
	v := binary.BigEndian.Uint32(b[:])
	for _, r := range []struct{ mask, value uint32 }{
		{0xff000000, 0x00000000}, // 0.0.0.0/8
		{0xff000000, 0x7f000000}, // 127.0.0.0/8
		{0xff000000, 0x0a000000}, // 10.0.0.0/8
		{0xfff00000, 0xac100000}, // 172.16.0.0/12
		{0xffff0000, 0xc0a80000}, // 192.168.0.0/16
		{0xffc00000, 0x64400000}, // 100.64.0.0/10
		{0xffff0000, 0xa9fe0000}, // 169.254.0.0/16
		{0xffffff00, 0xc0000000}, // 192.0.0.0/24
		{0xf0000000, 0xe0000000}, // 224.0.0.0/4
		{0xf0000000, 0xf0000000}, // 240.0.0.0/4
		{0xfffe0000, 0xc6120000}, // 198.18.0.0/15
	} {
		if v&r.mask == r.value {
			return true
		}
	}
	return false
}

// v6RangeRefused is the oracle for the IPv6 ranges, with 6to4 and
// Teredo whole.
func v6RangeRefused(b [16]byte) bool {
	return b[0] == 0xfe && b[1]&0xc0 == 0x80 || // fe80::/10
		b[0]&0xfe == 0xfc || // fc00::/7
		b[0] == 0xff || // ff00::/8
		bytes.HasPrefix(b[:], []byte{0x20, 0x02}) || // 2002::/16
		bytes.HasPrefix(b[:], []byte{0x20, 0x01, 0, 0}) // 2001::/32
}

// embeddedRefused is the oracle for the IPv4 address at each embedded
// position: IPv4-compatible (with :: and ::1 among them), IPv4-mapped,
// and NAT64.
func embeddedRefused(b [16]byte) bool {
	at := func(i, j, k, l int) bool { return v4Refused([4]byte{b[i], b[j], b[k], b[l]}) }
	compat := make([]byte, 12)
	mapped := append(make([]byte, 10), 0xff, 0xff)
	wellKnown := append([]byte{0, 0x64, 0xff, 0x9b}, make([]byte, 8)...)
	localUse := []byte{0, 0x64, 0xff, 0x9b, 0, 1}
	switch {
	case bytes.HasPrefix(b[:], compat), bytes.HasPrefix(b[:], mapped), bytes.HasPrefix(b[:], wellKnown):
		return at(12, 13, 14, 15)
	case bytes.HasPrefix(b[:], localUse):
		return at(6, 7, 9, 10) || at(7, 9, 10, 11) || at(9, 10, 11, 12) || at(12, 13, 14, 15)
	}
	return false
}

// Arbitrary 16-byte addresses: every one whose IPv4 form, mapped,
// compatible or NAT64-embedded, is in an IPv4 range is refused, and so
// is every address in 6to4 and Teredo (C02-streams-and-tls, "Fuzzing").
// The check and the oracle agree both ways.
func FuzzUpstreamAddress(f *testing.F) {
	for _, s := range []string{
		"::ffff:127.0.0.1", "::127.0.0.1", "64:ff9b::a00:1", "64:ff9b:1:808:8:808:808:808",
		"2002:808:808::1", "2001:0:4136:e378:8000:63bf:f7f7:f7f7", "2606:4700:4700::1111", "fe80::1",
	} {
		a := netip.MustParseAddr(s).As16()
		f.Add(a[:])
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) != 16 {
			return
		}
		b := [16]byte(raw)
		a := netip.AddrFrom16(b)
		name, refused := Refused(a)
		if want := v6RangeRefused(b) || embeddedRefused(b); refused != want {
			t.Fatalf("Refused(%s) = %q, %v; the oracle says %v", a, name, refused, want)
		}
		if a.Is4In6() {
			if _, refused4 := Refused(a.Unmap()); refused4 != refused {
				t.Fatalf("Refused(%s) = %v, but its IPv4 form %s gives %v", a, refused, a.Unmap(), refused4)
			}
		}
	})
}
