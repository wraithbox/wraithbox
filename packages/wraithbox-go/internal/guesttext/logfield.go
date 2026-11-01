package guesttext

import (
	"strconv"
	"strings"
)

// LogField returns a string from the guest in the form a log record
// holds it (S07-02-packet-path, "Events and rate limits"): in double
// quotes, with every byte outside printable ASCII written as \xHH and a
// quote or backslash escaped with a backslash. Only the first maxBytes
// bytes of s are kept. When s is longer, the quoted text is followed by
// a truncation marker that gives the length of s, such as
// "abc"[cut from 300 bytes]. The result is printable ASCII, and at most
// 4*maxBytes + 2 bytes plus the marker long.
//
// It is for strings such as a DHCP option, a DNS name, and a name or
// label in a hand-off message.
func LogField(s string, maxBytes int) string {
	kept := s
	maxBytes = max(maxBytes, 0)
	if len(kept) > maxBytes {
		kept = kept[:maxBytes]
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(kept) + 2)
	b.WriteByte('"')
	for i := range len(kept) {
		c := kept[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	b.WriteByte('"')
	if len(kept) < len(s) {
		b.WriteString("[cut from " + strconv.Itoa(len(s)) + " bytes]")
	}
	return b.String()
}
