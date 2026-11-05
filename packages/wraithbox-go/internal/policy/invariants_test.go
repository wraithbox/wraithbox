package policy

import "testing"

// TestRepositoryHost covers the compare forms of gitremote.Clean: every
// `scheme://` form names its host or is refused, and only forms with no
// host to name, or an IP address, name none.
func TestRepositoryHost(t *testing.T) {
	for _, tc := range []struct {
		compare string
		host    string // "" when it names none
		refused bool
	}{
		{"https://github.com/o/r", "github.com", false},
		{"https://git.example.com:8443/o/r", "git.example.com", false},
		{"https://git.example.com:bad/o/r", "git.example.com", false},
		{"http://git.example.com/o/r.git", "git.example.com", false},
		{"git://git.example.com/o/r.git", "git.example.com", false},
		{"ftp://user@Git.Example.com:21/o/r", "git.example.com", false},
		{"https://git.example.com?x#y", "git.example.com", false},
		{"https://[git.example.com:22]/o/r", "git.example.com", false},
		{"https://10.0.0.1/o/r", "", false},
		{"https://[2001:db8::1]:8443/o/r", "", false},
		{"https://127.1/o/r", "", false},
		{"file:///srv/git/r.git", "", false},
		{"sha256:00", "", false},
		{"/srv/git/r.git", "", false},
		{"../r", "", false},
		{"https:///o/r", "", true},
		{"https://git_example.com/o/r", "", true},
		{"https://-git.example.com/o/r", "", true},
		{"git://:9418/r", "", true},
	} {
		n, ok, r := repositoryHost(tc.compare)
		if (r != nil) != tc.refused {
			t.Errorf("%s: refusal %v, want refused %v", tc.compare, r, tc.refused)
			continue
		}
		if r != nil && r.Rule != RuleValue {
			t.Errorf("%s: rule %s, want %s", tc.compare, r.Rule, RuleValue)
		}
		if ok != (tc.host != "") || n.String() != tc.host {
			t.Errorf("%s: host %q %v, want %q", tc.compare, n, ok, tc.host)
		}
	}
}
