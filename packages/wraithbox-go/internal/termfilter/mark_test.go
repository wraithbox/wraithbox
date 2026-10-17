package termfilter

import (
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// markCases has one row per mark of S16-02-drops-and-links, "Marks",
// and one per rule of how markURL reads a URL. They seed FuzzLinkMark.
var markCases = []struct {
	name string
	url  string
	want Mark
}{
	// One row per mark, and the first-match order.
	{"file", "file:///etc/hosts", MarkGuestPath},
	{"file with an ip host", "FILE://127.0.0.1/etc/hosts", MarkGuestPath},
	{"ipv4", "https://127.0.0.1/", MarkIPLiteral},
	{"http with ipv4 is ip-literal, not http", "http://127.0.0.1/", MarkIPLiteral},
	{"ipv6 in brackets", "https://[::1]/", MarkIPLiteral},
	{"ipv6 with a port", "https://[fe80::1%25en0]:8080/", MarkIPLiteral},
	{"short ipv4", "https://127.1/", MarkIPLiteral},
	{"ipv4 as one number", "https://2130706433/", MarkIPLiteral},
	{"hex ipv4", "https://0x7f.0.0.1/", MarkIPLiteral},
	{"octal ipv4", "https://0177.0.0.1/", MarkIPLiteral},
	{"name ending in a hex label", "https://example.0X1F/", MarkIPLiteral},
	{"name ending in 0x", "https://example.0x/", MarkIPLiteral},
	{"localhost", "https://localhost/", MarkLocalName},
	{"localhost in capitals with a trailing dot", "https://LOCALHOST./", MarkLocalName},
	{"under .localhost", "https://app.localhost:3000/", MarkLocalName},
	{"under .local", "https://printer.local/", MarkLocalName},
	{"under .internal", "https://db.corp.internal/", MarkLocalName},
	{"under .home.arpa", "https://nas.home.arpa/", MarkLocalName},
	{"under .lan", "https://printer.lan/", MarkLocalName},
	{"ideographic dot", "https://printer。lan/", MarkLocalName},
	{"fullwidth dot", "https://printer．lan/", MarkLocalName},
	{"halfwidth ideographic dot", "https://localhost｡/", MarkLocalName},
	{"percent-encoded name", "https://%6c%6f%63%61%6c%68%6f%73%74/", MarkLocalName},
	{"unicode capital that lowercases to ascii", "https://db.İnternal/", MarkLocalName},
	{"local name with a user", "https://me@printer.lan/", MarkLocalName},
	{"dotless name", "https://intranet/", MarkDotlessName},
	{"no host", "https://", MarkDotlessName},
	{"only slashes", "https:///", MarkDotlessName},
	{"empty host before a port", "https://:443/", MarkDotlessName},
	{"port that isn't digits", "https://example.com:x.local/", MarkDotlessName},
	{"host with a bad escape", "https://ex%zzample.com/", MarkDotlessName},
	{"host with a forbidden code point", "https://exa<mple.com/", MarkDotlessName},
	{"host with invalid utf-8", "https://ex\xffample.com/", MarkDotlessName},
	{"host with U+FFFD", "https://ex�ample.com/", MarkDotlessName},
	{"name that is only a dot", "https://./", MarkDotlessName},
	{"user name", "https://github.com@evil.example/", MarkUserName},
	{"user name and password", "https://user:pass@example.com/", MarkUserName},
	{"empty user name", "https://@example.com/", MarkUserName},
	{"host after the last @", "https://a@b@evil.example/", MarkUserName},
	{"http", "http://example.com/", MarkHTTP},
	{"http in capitals", "HTTP://EXAMPLE.COM/", MarkHTTP},
	{"other scheme", "ftp://example.com/", MarkOtherScheme},
	{"ssh", "ssh://git@example.com/repo", MarkUserName},
	{"ssh to an ip", "ssh://10.0.0.1/", MarkIPLiteral},
	{"non-special without an authority", "mailto:me@127.0.0.1", MarkOtherScheme},
	{"custom app scheme", "x-man-page://ls", MarkDotlessName},
	{"no scheme", "//example.com/", MarkOtherScheme},
	{"no colon", "example.com", MarkOtherScheme},
	{"empty", "", MarkOtherScheme},
	{"scheme with a bad character", "ht_tp://example.com/", MarkOtherScheme},
	{"scheme starting with a digit", "1http://example.com/", MarkOtherScheme},

	// How the authority is read: special schemes skip slashes and
	// backslashes, and end the authority at a backslash.
	{"https", "https://example.com/", ""},
	{"https in capitals", "HTTPS://Example.COM/", ""},
	{"https with a port", "https://example.com:8443/path", ""},
	{"percent-encoded dot", "https://evil%2Eexample/", ""},
	{"https without slashes", "https:example.com", ""},
	{"backslashes", `https:\\127.0.0.1\`, MarkIPLiteral},
	{"backslash ends the authority", `https://example.com\@127.0.0.1/`, ""},
	{"@ in the path", "https://example.com/@127.0.0.1", ""},
	{"@ in the query", "https://example.com?x@127.0.0.1", ""},
	{"@ in the fragment", "https://example.com#@127.0.0.1", ""},
	{"path with invalid utf-8", "https://example.com/\xff", ""},
	{"leading space and controls", " \x01https://127.0.0.1/", MarkIPLiteral},
	{"tab and newline inside", "ht\t" + "tps://local\n" + "host/", MarkLocalName},
	{"name ending in a dot and digits", "https://example.com1/", ""},
	{"two trailing dots", "https://localhost../", ""},
}

func TestMarkURL(t *testing.T) {
	for _, tt := range markCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := markURL(tt.url); got != tt.want {
				t.Errorf("markURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestMarks(t *testing.T) {
	want := []Mark{
		"guest-path", "ip-literal", "local-name", "dotless-name",
		"user-name", "http", "other-scheme",
	}
	got := Marks()
	if !slices.Equal(got, want) {
		t.Fatalf("Marks() = %v, want %v", got, want)
	}
	got[0] = "made-up"
	if Marks()[0] != MarkGuestPath {
		t.Error("Marks() returns its own list")
	}
	for _, m := range want {
		if !m.Valid() {
			t.Errorf("%s.Valid() = false", m)
		}
	}
	for _, m := range []Mark{"", MarkUnmarked, "made-up"} {
		if m.Valid() {
			t.Errorf("Mark(%q).Valid() = true", m)
		}
	}
}

// FuzzLinkMark checks the marker against the property of C10-terminal,
// "Fuzzing": it marks every URL that matches a mark rule. The oracle is
// mustMark, which reads the URL with net/url and not with the marker's
// code. It also checks that marking a URL twice gives the same mark.
func FuzzLinkMark(f *testing.F) {
	for _, tt := range markCases {
		f.Add(tt.url)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := markURL(raw)
		if got != "" && !got.Valid() {
			t.Fatalf("markURL(%q) = %q, not a mark", raw, got)
		}
		if again := markURL(raw); again != got {
			t.Fatalf("markURL(%q) = %q, then %q", raw, got, again)
		}
		if why := mustMark(raw); why != "" && got == "" {
			t.Fatalf("markURL(%q) has no mark, but %s", raw, why)
		}
	})
}

// mustMark says why raw matches a mark rule of S16-02-drops-and-links,
// "Marks", as net/url and net/netip read it, or returns "" when it
// matches none or net/url can't parse it. The reverse isn't checked:
// the marker reads more hosts as IP literals than net/url does.
//
// A host's dots are those the marker reads as dots, because IDNA reads
// U+3002, U+FF0E and U+FF61 as `.`, and S16-02-drops-and-links reads a
// name as the WHATWG URL Standard does.
func mustMark(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "https" {
		return "the scheme is " + u.Scheme + ", not https"
	}
	if u.User != nil {
		return "it has a user name"
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "the host is an IP address"
	}
	host = strings.NewReplacer("。", ".", "．", ".", "｡", ".").Replace(strings.ToLower(host))
	if host == "localhost" {
		return "the host is localhost"
	}
	for _, s := range []string{".localhost", ".local", ".internal", ".home.arpa", ".lan"} {
		if strings.HasSuffix(host, s) {
			return "the host is under " + s
		}
	}
	if !strings.Contains(host, ".") {
		return "the host has no dot"
	}
	return ""
}

// TestMustMarkRefuses feeds the oracle a URL for each rule, and sees it
// demand a mark. An oracle that demands nothing would make FuzzLinkMark
// pass too.
func TestMustMarkRefuses(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/",
		"//example.com/",
		"https://u@example.com/",
		"https://127.0.0.1/",
		"https://[::1]/",
		"https://LocalHost/",
		"https://printer.LAN/",
		"https://nas.home.arpa/",
		"https://intranet/",
	} {
		if mustMark(raw) == "" {
			t.Errorf("mustMark(%q) = \"\", want a reason", raw)
		}
	}
	if why := mustMark("https://example.com/"); why != "" {
		t.Errorf("mustMark of a public https URL = %q, want \"\"", why)
	}
}
