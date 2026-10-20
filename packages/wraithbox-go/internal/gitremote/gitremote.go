// Package gitremote cleans a git remote URL by the rules of
// S08-01-into-the-guest, "Remote URLs". It gives the URL the guest gets,
// or the reason the guest doesn't get it, and the form wb records and
// compares as a project's identity (S05-04-naming, "Recorded identity").
//
// A remote URL comes from the user's repository, which may come from a
// downloaded archive, so every byte of it is treated as an attacker's.
// No user name, password, query string or fragment is ever in the
// guest URL or the compare form, and a URL the cleaner can't parse is
// recorded only as a hash of its bytes.
package gitremote

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"net/url"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// Reason says why the guest doesn't get a remote URL. The session
// summary and the audit record name it (S08-01-into-the-guest, "Remote
// URLs").
type Reason string

// The reasons a URL is withheld from the guest.
const (
	// WithheldScheme: a URL with a scheme other than https:// and
	// ssh://, such as http://, git://, file://, git+ssh:// or
	// ssh+git://.
	WithheldScheme Reason = "remote-scheme"
	// WithheldLocal: a local path, which git reads as one when no colon
	// comes before the first slash, or on Windows after a drive letter.
	WithheldLocal Reason = "remote-local-path"
	// WithheldHelper: a `<helper>::<address>` URL, such as `ext::`.
	WithheldHelper Reason = "remote-helper"
	// WithheldIPHost: an SSH or HTTPS URL whose host is an IP address.
	WithheldIPHost Reason = "remote-ip-host"
	// WithheldUnparsable: a URL the cleaner can't parse, or whose host
	// isn't a DNS name, or whose port isn't 1 to 5 digits.
	WithheldUnparsable Reason = "remote-unparsable"
)

// hashPrefix starts the compare form of a URL the cleaner can't parse,
// and of a helper URL, whose address it doesn't parse.
const hashPrefix = "sha256:"

// Result is a cleaned remote URL.
type Result struct {
	// Guest is the URL the guest gets: `https://host[:port]/path` with no
	// user info, query or fragment. It is empty when Withheld is set.
	Guest string
	// Withheld is the reason the guest doesn't get the URL, or "".
	Withheld Reason
	// Compare is the form wb records and compares (S05-04-naming,
	// "Recorded identity"). It is Guest when the guest gets the URL.
	// Otherwise it is the URL without its user name, password, query
	// string and fragment, with an SSH URL in its `https://` form. It is
	// `sha256:<hex>` of the URL's bytes when the cleaner can't split the
	// URL into its parts, and for a helper URL, whose address it doesn't
	// parse.
	Compare string
}

// Clean cleans a remote URL as the host's git reads it.
func Clean(raw string) Result {
	return clean(raw, platform.HasDrivePrefix)
}

// clean is Clean with the drive-letter rule as a parameter, so the tests
// can check both readings on any host.
func clean(raw string, hasDrive func(string) bool) Result {
	// A helper's address is a command line, with spaces, for `ext::`.
	switch {
	case isHelper(raw):
		return hashed(raw, WithheldHelper)
	case !printable(raw):
		return hashed(raw, WithheldUnparsable)
	}
	if scheme, ok := urlScheme(raw); ok {
		switch scheme {
		case "https":
			return fromURL(raw, true)
		case "ssh":
			return fromURL(raw, false)
		default:
			return otherScheme(raw)
		}
	}
	if isLocal(raw, hasDrive) {
		return Result{Withheld: WithheldLocal, Compare: raw}
	}
	return fromShortForm(raw)
}

// printable accepts a non-empty URL of printable ASCII without spaces.
// Anything else is a URL the cleaner doesn't parse: a control byte or a
// line end can't reach the guest's configuration or a terminal.
func printable(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// isSchemeChar is the scheme character test of git's url.c: a letter
// first, then letters, digits, `+`, `-` and `.`.
func isSchemeChar(first bool, c byte) bool {
	alpha := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
	if first {
		return alpha
	}
	return alpha || '0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'
}

// isHelper is git's test for `<helper>::<address>` in transport_get: scheme
// characters, possibly none, then `::`.
func isHelper(s string) bool {
	i := 0
	for i < len(s) && isSchemeChar(i == 0, s[i]) {
		i++
	}
	return strings.HasPrefix(s[i:], "::")
}

// urlScheme is git's is_url: a scheme of scheme characters, then `://`.
// The scheme is returned as written: git matches it case-sensitively.
func urlScheme(s string) (string, bool) {
	scheme, _, ok := strings.Cut(s, "://")
	if !ok || scheme == "" {
		return "", false
	}
	for i := range len(scheme) {
		if !isSchemeChar(i == 0, scheme[i]) {
			return "", false
		}
	}
	return scheme, true
}

// isLocal is git's url_is_local_not_ssh: no colon, a slash before the
// first colon, or a drive letter where the host has drive letters.
func isLocal(s string, hasDrive func(string) bool) bool {
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	return colon < 0 || slash >= 0 && slash < colon || hasDrive(s)
}

// fromURL cleans an https:// or ssh:// URL. An https:// URL keeps its
// port. An ssh:// URL loses it, because the SSH port says nothing about
// the forge's HTTPS port (S08-01-into-the-guest, "Clean the remote URLs").
func fromURL(raw string, keepPort bool) Result {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" {
		return hashed(raw, WithheldUnparsable)
	}
	port := ""
	if keepPort {
		port = u.Port()
	}
	return httpsForm(u.Hostname(), port, u.EscapedPath())
}

// fromShortForm cleans git's SSH short form `[user@]host:path`, where
// host may be in brackets: `[::1]`, or `[host:port]`. The split follows
// git's host_end: the path starts at the first colon after the host's
// closing bracket.
func fromShortForm(raw string) Result {
	end := 0
	start := 0
	if i := strings.Index(raw, "@["); i >= 0 {
		start = i + 1
	}
	if raw[start] == '[' {
		if j := strings.IndexByte(raw[start:], ']'); j >= 0 {
			end = start + j
		}
	}
	colon := strings.IndexByte(raw[end:], ':')
	if colon < 0 {
		return hashed(raw, WithheldUnparsable)
	}
	hostPart, path := raw[:end+colon], raw[end+colon+1:]
	if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	host, ok := shortFormHost(hostPart)
	if !ok || path == "" {
		return hashed(raw, WithheldUnparsable)
	}
	escaped := (&url.URL{Path: "/" + strings.TrimPrefix(path, "/")}).EscapedPath()
	return httpsForm(host, "", escaped)
}

// shortFormHost takes the host out of a short form's host part: a bare
// host, `[ip]`, or `[host:port]`, whose port is dropped.
func shortFormHost(part string) (string, bool) {
	inner, ok := strings.CutPrefix(part, "[")
	if !ok {
		return part, !strings.ContainsAny(part, "[]")
	}
	inner, ok = strings.CutSuffix(inner, "]")
	if !ok {
		return "", false
	}
	if _, err := netip.ParseAddr(inner); err == nil {
		return inner, true
	}
	if i := strings.LastIndexByte(inner, ':'); i >= 0 {
		inner = inner[:i]
	}
	return inner, true
}

// httpsForm builds `https://host[:port]path` with the host in lowercase,
// because host names don't depend on letter case. The guest gets it when
// the host is a DNS name. A host that is an IP address, or that isn't a
// DNS name, withholds it, and the form is still the compare form: it
// holds no user info, query or fragment.
func httpsForm(host, port, escapedPath string) Result {
	host = strings.ToLower(host)
	authority := host
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority += ":" + port
	}
	cleaned := "https://" + authority + escapedPath
	switch {
	case isIPHost(host):
		return Result{Withheld: WithheldIPHost, Compare: cleaned}
	case !isDNSName(host) || !validPort(port):
		return Result{Withheld: WithheldUnparsable, Compare: cleaned}
	}
	return Result{Guest: cleaned, Compare: cleaned}
}

// isIPHost treats a host as an IP address when it parses as one, or
// when its last label starts with a digit, as in `127.1` or `0x7f.1`,
// which some resolvers read as an address. No top-level domain starts
// with a digit.
func isIPHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	last := host[strings.LastIndexByte(host, '.')+1:]
	return last != "" && '0' <= last[0] && last[0] <= '9'
}

// isDNSName accepts a lowercase host name of letters, digits and
// hyphens in labels of 1 to 63 bytes, joined by dots, at most 253 bytes,
// with no label that starts or ends with a hyphen.
func isDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		if strings.ContainsFunc(label, notDNSChar) {
			return false
		}
	}
	return true
}

// notDNSChar is true for anything but a lowercase letter, a digit and
// `-`.
func notDNSChar(c rune) bool {
	return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-'
}

// validPort accepts no port, or 1 to 5 digits.
func validPort(port string) bool {
	if len(port) > 5 {
		return false
	}
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

// otherScheme withholds a URL with another scheme. Its compare form is
// the URL without user info, query string and fragment, with the host
// in lowercase, or a hash when it doesn't parse.
func otherScheme(raw string) Result {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return hashed(raw, WithheldUnparsable)
	}
	u.User = nil
	u.Host = strings.ToLower(u.Host)
	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	return Result{Withheld: WithheldScheme, Compare: u.String()}
}

// hashed withholds a URL for reason, with the hash of its bytes as its
// compare form, so a token inside it is never stored or printed.
func hashed(raw string, reason Reason) Result {
	return Result{Withheld: reason, Compare: hashOf(raw)}
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hashPrefix + hex.EncodeToString(sum[:])
}
