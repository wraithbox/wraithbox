package termfilter

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Mark names one row of S16-02-drops-and-links, "Marks". The filter
// marks a dropped link with one of these names, or with none, and no
// other name exists, so a guest can't make one up. `wb-hostd` checks
// the marks `wb` reports against this list (S09-12-terminal-filter,
// "Form"). The zero Mark means the link has no mark.
type Mark string

// The marks in the order of the table in S16-02-drops-and-links,
// "Marks", which is also the order they are tried in. A link gets the
// first that applies.
const (
	// MarkGuestPath marks a `file:` URL, a path in the guest.
	MarkGuestPath Mark = "guest-path"
	// MarkIPLiteral marks a URL whose host is an IP address.
	MarkIPLiteral Mark = "ip-literal"
	// MarkLocalName marks a URL whose host is `localhost` or a name
	// under `.localhost`, `.local`, `.internal`, `.home.arpa` or `.lan`.
	MarkLocalName Mark = "local-name"
	// MarkDotlessName marks a URL whose host has no dot.
	MarkDotlessName Mark = "dotless-name"
	// MarkUserName marks a URL with a user name.
	MarkUserName Mark = "user-name"
	// MarkHTTP marks an `http:` URL.
	MarkHTTP Mark = "http"
	// MarkOtherScheme marks a URL with a scheme other than `https`.
	MarkOtherScheme Mark = "other-scheme"
)

// MarkUnmarked is the key under which LinkOverflow counts the links
// past the kept ones that have no mark (S16-02-drops-and-links, "What
// `wb` keeps of a link"). A Link never carries it: a link without a
// mark has the zero Mark.
const MarkUnmarked Mark = "unmarked"

var marks = [...]Mark{
	MarkGuestPath,
	MarkIPLiteral,
	MarkLocalName,
	MarkDotlessName,
	MarkUserName,
	MarkHTTP,
	MarkOtherScheme,
}

// Marks returns every mark, in the order of the table in
// S16-02-drops-and-links. MarkUnmarked isn't one.
func Marks() []Mark {
	return slices.Clone(marks[:])
}

// Valid reports whether m is one of the marks Marks returns.
func (m Mark) Valid() bool {
	return slices.Contains(marks[:], m)
}

// localSuffixes are the names under which a host is a local name
// (S16-02-drops-and-links, "Marks").
var localSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa", ".lan"}

// markURL returns the mark of a link's URL, or the zero Mark.
//
// A click on a link goes to the host's URL handler, so markURL reads
// the URL the way the WHATWG URL Standard does, and not the way
// net/url does (S16-02-drops-and-links, "Marks"). A
// URL is guest bytes, so markURL accepts any bytes, and reads a URL a
// browser would refuse as far as it can.
func markURL(raw string) Mark {
	scheme, rest, ok := readScheme(trimURL(raw))
	if !ok {
		return MarkOtherScheme
	}
	if scheme == "file" {
		return MarkGuestPath
	}
	userInfo, host, hasAuthority := readAuthority(scheme, rest)
	if hasAuthority {
		if m := markHost(scheme, host); m != "" {
			return m
		}
		if userInfo {
			return MarkUserName
		}
	}
	switch scheme {
	case "https":
		return ""
	case "http":
		return MarkHTTP
	}
	return MarkOtherScheme
}

// trimURL removes what the WHATWG URL parser removes before it reads a
// URL: the C0 controls and spaces at either end, and every tab and
// newline. It works on bytes, so an invalid byte stays as it came.
func trimURL(s string) string {
	s = strings.TrimLeftFunc(s, func(r rune) bool { return r <= ' ' })
	s = strings.TrimRightFunc(s, func(r rune) bool { return r <= ' ' })
	if !strings.ContainsAny(s, "\t\n\r") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '\t' && c != '\n' && c != '\r' {
			b = append(b, c)
		}
	}
	return string(b)
}

// readScheme splits s at the first `:` into the scheme, lowercased, and
// the rest. A scheme is an ASCII letter followed by letters, digits,
// `+`, `-` and `.`. It reports false when s has no such scheme.
func readScheme(s string) (string, string, bool) {
	scheme, rest, found := strings.Cut(s, ":")
	if !found || scheme == "" || !isASCIILetter(scheme[0]) {
		return "", "", false
	}
	for i := 1; i < len(scheme); i++ {
		c := scheme[i]
		if !isASCIILetter(c) && !isDigit(c) && c != '+' && c != '-' && c != '.' {
			return "", "", false
		}
	}
	return strings.ToLower(scheme), rest, true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// specialSchemes are the WHATWG URL Standard's special schemes other
// than `file`, which markURL handles first.
var specialSchemes = []string{"http", "https", "ws", "wss", "ftp"}

// readAuthority finds the authority of a URL after its scheme. For a
// special scheme it skips every `/` and `\` and ends the authority at
// the next `/`, `\`, `?` or `#`. Any other scheme has an authority only
// after `//`, and `\` doesn't end it. It returns whether the authority
// has a user name, which is everything before its last `@`, and the
// host and port after that `@`.
func readAuthority(scheme, rest string) (bool, string, bool) {
	ends := "/?#"
	if slices.Contains(specialSchemes, scheme) {
		rest = strings.TrimLeft(rest, `/\`)
		ends = `/\?#`
	} else {
		var found bool
		if rest, found = strings.CutPrefix(rest, "//"); !found {
			return false, "", false
		}
	}
	if i := strings.IndexAny(rest, ends); i >= 0 {
		rest = rest[:i]
	}
	at := strings.LastIndexByte(rest, '@')
	return at >= 0, rest[at+1:], true
}

// markHost returns the mark of a host and port, or the zero Mark.
// An `http` or `https` URL whose host can't be read is marked as
// MarkDotlessName, because it names no host on the internet.
func markHost(scheme, hostPort string) Mark {
	if strings.HasPrefix(hostPort, "[") {
		return MarkIPLiteral
	}
	host, ok := readHost(hostPort)
	if !ok {
		if scheme == "http" || scheme == "https" {
			return MarkDotlessName
		}
		return ""
	}
	switch {
	case endsInNumber(host):
		return MarkIPLiteral
	case isLocalName(host):
		return MarkLocalName
	case !strings.Contains(host, "."):
		return MarkDotlessName
	}
	return ""
}

// forbiddenHost holds the ASCII code points the WHATWG URL Standard
// forbids in a domain, other than C0 controls and DEL, which readHost
// checks apart. A host that holds one after percent-decoding can't be
// read.
const forbiddenHost = " #%/:<>?@[\\]^|"

// idnaDots maps the dots that IDNA reads as `.` to `.`.
var idnaDots = strings.NewReplacer("。", ".", "．", ".", "｡", ".")

// readHost reads the host in hostPort the way the WHATWG URL Standard
// does: it cuts the port at the first `:`, percent-decodes the host,
// lowercases it, reads the dots U+3002, U+FF0E and U+FF61 as `.`, and
// strips one trailing dot. It reports false for a host it can't read:
// an empty one, one with a port that isn't digits, and one that, after
// percent-decoding, isn't valid UTF-8 or holds U+FFFD or a code point
// the standard forbids in a domain.
func readHost(hostPort string) (string, bool) {
	host, port, _ := strings.Cut(hostPort, ":")
	if strings.TrimFunc(port, func(r rune) bool { return r >= '0' && r <= '9' }) != "" {
		return "", false
	}
	host = percentDecode(host)
	if !utf8.ValidString(host) || strings.ContainsRune(host, utf8.RuneError) ||
		strings.ContainsAny(host, forbiddenHost) ||
		strings.ContainsFunc(host, func(r rune) bool { return r < ' ' || r == del }) {
		return "", false
	}
	host = idnaDots.Replace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host, host != ""
}

// percentDecode replaces each `%` and two hex digits with the byte they
// name, and leaves any other `%` as it is.
func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b = append(b, hexValue(s[i+1])<<4|hexValue(s[i+2]))
			i += 2
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) byte {
	switch {
	case isDigit(c):
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// endsInNumber reports whether the last label of host, which is
// lowercase, is a number as the WHATWG URL Standard's IPv4 parser reads
// one: digits, which also covers octal with a leading zero, or `0x`
// followed by hex digits, none included. A browser then reads the host
// as an IPv4 address, so `127.1`, `0x7f.0.0.1` and `2130706433` are all
// IP literals.
func endsInNumber(host string) bool {
	label := host[strings.LastIndexByte(host, '.')+1:]
	hex, ok := strings.CutPrefix(label, "0x")
	if !ok {
		return isDigits(label)
	}
	for i := 0; i < len(hex); i++ {
		if !isHex(hex[i]) {
			return false
		}
	}
	return true
}

// isLocalName reports whether host is `localhost` or a name under one
// of localSuffixes.
func isLocalName(host string) bool {
	if host == "localhost" {
		return true
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}
