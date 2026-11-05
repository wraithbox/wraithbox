package builtin

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

// TestRulesCoverEveryPart checks that each part of S07-11-http-policy,
// "Profile parts", has a rule, that each allowed covered host has rules,
// that a host that allows no request has none, and that an allowed
// host without rules is denied whole (codeload and gist).
func TestRulesCoverEveryPart(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Entries() {
		allows := Allows(e.Host)
		if e.Allowed != (len(allows) > 0 || deniedWhole(e.Host)) {
			t.Errorf("%s: allowed %v but %d allow rules", e.Host, e.Allowed, len(allows))
		}
		for _, a := range allows {
			if a.Profile != e.Profile || a.Kind != e.Kind || !KnownPart(a.Profile, a.Kind, a.Part) {
				t.Errorf("%s: rule in %s/%s/%s", e.Host, a.Profile, a.Kind, a.Part)
			}
			seen[string(a.Profile)+"/"+string(a.Kind)+"/"+a.Part] = true
		}
	}
	for _, part := range allParts() {
		if !seen[part] {
			t.Errorf("part %s has no rule", part)
		}
	}
}

// deniedWhole reports whether a built-in deny covers every request to
// the host.
func deniedWhole(n dnsname.Name) bool {
	for _, d := range Denies(n) {
		if d.Rule.Match(n, "PATCH", "/any/path") {
			return true
		}
	}
	return false
}

// allParts names every part as profile/kind/part.
func allParts() []string {
	var out []string
	for _, p := range []Profile{GitHosting, ModelAPI, PackageRegistries} {
		for _, k := range []Kind{NoKind, GitHub, GitLab, Gitea} {
			for _, part := range Parts(p, k) {
				out = append(out, string(p)+"/"+string(k)+"/"+part)
			}
		}
	}
	return out
}

func TestAllowParts(t *testing.T) {
	gh := name(t, "github.com")
	first := func(method, path string) string {
		for _, a := range Allows(gh) {
			if a.Rule.Match(gh, method, path) {
				return a.Part
			}
		}
		return ""
	}
	for _, tc := range []struct{ method, path, part string }{
		{"GET", "/acme/app.git/info/refs", "git"},
		{"POST", "/acme/app.git/git-receive-pack", "git"},
		{"GET", "/acme/app", "web"},
		{"POST", "/acme/app", ""},
	} {
		if got := first(tc.method, tc.path); got != tc.part {
			t.Errorf("%s %s in part %q, want %q", tc.method, tc.path, got, tc.part)
		}
	}
}

func TestDenies(t *testing.T) {
	denied := func(n string, method, path string) string {
		h := name(t, n)
		for _, d := range Denies(h) {
			if d.Rule.Match(h, method, path) {
				return d.Name
			}
		}
		return ""
	}
	for _, tc := range []struct{ host, method, path, rule string }{
		{"codeload.github.com", "GET", "/acme/app/zip/main", RuleArchive},
		{"api.github.com", "POST", "/user/repos", RuleCreate},
		{"api.github.com", "GET", "/user/repos", ""},
		{"api.anthropic.com", "POST", "/api/event_logging/v2/batch", RuleEventLog},
		{"ghcr.io", "GET", "/token", RuleRegistryToken},
		{"registry.npmjs.org", "GET", "/left-pad", ""},
	} {
		if got := denied(tc.host, tc.method, tc.path); got != tc.rule {
			t.Errorf("%s %s %s denied by %q, want %q", tc.method, tc.host, tc.path, got, tc.rule)
		}
	}
}

func TestRemoteRules(t *testing.T) {
	n := name(t, "git.example.net")
	allows, denies := RemoteRules(n)
	if len(allows) == 0 || len(denies) == 0 {
		t.Fatalf("%d allows, %d denies", len(allows), len(denies))
	}
	for _, a := range allows {
		if a.Profile != GitHosting || a.Kind != NoKind || a.Part != "git" {
			t.Errorf("remote rule in %s/%s/%s", a.Profile, a.Kind, a.Part)
		}
	}
	match := func(method, path string) bool {
		for _, a := range allows {
			if a.Rule.Match(n, method, path) {
				return true
			}
		}
		return false
	}
	if !match("POST", "/team/tool.git/git-receive-pack") || match("GET", "/team/tool") {
		t.Error("remote rules don't hold git's transport alone")
	}
	if Allows(n) != nil || Denies(n) != nil {
		t.Error("a host the catalog doesn't cover has catalog rules")
	}
}
