package gitremote

import (
	"strings"
	"testing"
)

func noDrive(string) bool { return false }

// windowsDrive is git for Windows' has_dos_drive_prefix.
func windowsDrive(s string) bool {
	return len(s) >= 2 && s[1] == ':' && ('a' <= s[0] && s[0] <= 'z' || 'A' <= s[0] && s[0] <= 'Z')
}

// TestClean covers every rule of S08-01-into-the-guest, "Remote URLs",
// and the URL cases of C08-git: a token in the user info, an `ext::`
// URL, `git+ssh://`, an IP-literal host, a bracketed IPv6 host, a
// drive-letter path, and a URL that doesn't parse. The compare forms
// follow S05-04-naming, "Recorded identity".
func TestClean(t *testing.T) {
	const gh = "https://github.com/org/repo.git"
	tests := []struct {
		name     string
		raw      string
		windows  bool
		guest    string
		withheld Reason
		compare  string // "hash" for sha256 of raw
	}{
		{"https", gh, false, gh, "", gh},
		{"https token in user info", "https://x-access-token:ghp_secret@github.com/org/repo.git", false, gh, "", gh},
		{"https user only", "https://me@github.com/org/repo.git", false, gh, "", gh},
		{"https query and fragment", "https://github.com/org/repo.git?token=ghp_secret#frag", false, gh, "", gh},
		{"https host case", "https://GitHub.COM/org/repo.git", false, gh, "", gh},
		{"https keeps port", "https://git.example.com:8443/a/b", false, "https://git.example.com:8443/a/b", "", "https://git.example.com:8443/a/b"},
		{"https keeps path escapes", "https://h.example/a%2Fb", false, "https://h.example/a%2Fb", "", "https://h.example/a%2Fb"},
		{"https no path", "https://h.example", false, "https://h.example", "", "https://h.example"},
		{"ssh url", "ssh://git@github.com/org/repo.git", false, gh, "", gh},
		{"ssh url drops port and password", "ssh://git:pw@github.com:2222/org/repo.git", false, gh, "", gh},
		{"ssh short form", "git@github.com:org/repo.git", false, gh, "", gh},
		{"ssh short form absolute path", "github.com:/org/repo.git", false, gh, "", gh},
		{"ssh short form bracketed host and port", "git@[github.com:22]:org/repo.git", false, gh, "", gh},
		{"ssh short form escapes path", "h.example:a?b#c", false, "https://h.example/a%3Fb%23c", "", "https://h.example/a%3Fb%23c"},
		{"http", "http://u:pw@h.example/x?q#f", false, "", WithheldScheme, "http://h.example/x"},
		{"git", "git://h.example/x", false, "", WithheldScheme, "git://h.example/x"},
		{"file", "file:///Users/me/repo", false, "", WithheldScheme, "file:///Users/me/repo"},
		{"git+ssh", "git+ssh://git:pw@h.example/x", false, "", WithheldScheme, "git+ssh://h.example/x"},
		{"ssh+git", "ssh+git://h.example/x", false, "", WithheldScheme, "ssh+git://h.example/x"},
		{"scheme case", "HTTPS://h.example/x", false, "", WithheldScheme, "https://h.example/x"},
		{"other scheme", "foo://u:pw@H.example/x", false, "", WithheldScheme, "foo://h.example/x"},
		{"absolute path", "/Users/me/repo", false, "", WithheldLocal, "/Users/me/repo"},
		{"relative path", "../repo", false, "", WithheldLocal, "../repo"},
		{"no colon", "repo", false, "", WithheldLocal, "repo"},
		{"slash before colon", "./a:b", false, "", WithheldLocal, "./a:b"},
		{"drive letter on windows", "C:/x/repo", true, "", WithheldLocal, "C:/x/repo"},
		{"drive letter elsewhere is ssh", "C:/x/repo", false, "https://c/x/repo", "", "https://c/x/repo"},
		{"ext helper", "ext::ssh -i key h.example %S 'repo'", false, "", WithheldHelper, "hash"},
		{"helper with url", "persistent-https::https://u:ghp_secret@h.example/x", false, "", WithheldHelper, "hash"},
		{"ip host https", "https://10.0.0.1/x", false, "", WithheldIPHost, "https://10.0.0.1/x"},
		{"ip host short", "git@192.168.1.1:x", false, "", WithheldIPHost, "https://192.168.1.1/x"},
		{"ip host shortened", "https://127.1/x", false, "", WithheldIPHost, "https://127.1/x"},
		{"ipv6 https", "https://[::1]:8443/x", false, "", WithheldIPHost, "https://[::1]:8443/x"},
		{"ipv6 ssh url", "ssh://git@[fe80::1]:22/x", false, "", WithheldIPHost, "https://[fe80::1]/x"},
		{"ipv6 short form", "git@[::1]:x", false, "", WithheldIPHost, "https://[::1]/x"},
		{"host not a dns name", "https://u:pw@my_host/x", false, "", WithheldUnparsable, "https://my_host/x"},
		{"empty", "", false, "", WithheldUnparsable, "hash"},
		{"space", "https://h.example/a b", false, "", WithheldUnparsable, "hash"},
		{"line end", "https://h.example/x\nhttps://u:pw@h.example/y", false, "", WithheldUnparsable, "hash"},
		{"non-ascii", "https://h.example/é", false, "", WithheldUnparsable, "hash"},
		{"bad escape", "https://u:ghp_secret@h.example/%zz", false, "", WithheldUnparsable, "hash"},
		{"no host", "https:///x", false, "", WithheldUnparsable, "hash"},
		{"short form without path", "git@h.example:", false, "", WithheldUnparsable, "hash"},
		{"short form unclosed bracket", "git@[::1:x", false, "", WithheldUnparsable, "hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drive := noDrive
			if tt.windows {
				drive = windowsDrive
			}
			got := clean(tt.raw, drive)
			want := Result{Guest: tt.guest, Withheld: tt.withheld, Compare: tt.compare}
			if tt.compare == "hash" {
				want.Compare = hashOf(tt.raw)
			}
			if got != want {
				t.Errorf("clean(%q) = %+v, want %+v", tt.raw, got, want)
			}
			if strings.Contains(got.Compare, "ghp_secret") || strings.Contains(got.Guest, "ghp_secret") {
				t.Errorf("clean(%q) = %+v keeps the token", tt.raw, got)
			}
		})
	}
}
