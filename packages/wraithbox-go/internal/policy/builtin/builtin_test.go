package builtin

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

func name(t *testing.T, s string) dnsname.Name {
	t.Helper()
	n, r := dnsname.FromHost(s)
	if r != nil {
		t.Fatalf("FromHost(%q): %v", s, r)
	}
	return n
}

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		host    string
		profile Profile
		kind    Kind
		allowed bool
		graphQL string
	}{
		{"github.com", GitHosting, GitHub, true, ""},
		{"api.github.com", GitHosting, GitHub, true, "/graphql"},
		{"ssh.github.com", GitHosting, GitHub, false, ""},
		{"gitlab.com", GitHosting, GitLab, true, "/api/graphql"},
		{"altssh.gitlab.com", GitHosting, GitLab, false, ""},
		{"codeberg.org", GitHosting, Gitea, true, ""},
		{"api.anthropic.com", ModelAPI, NoKind, true, ""},
		{"platform.claude.com", ModelAPI, NoKind, true, ""},
		{"sum.golang.org", PackageRegistries, NoKind, true, ""},
		{"API.GitHub.com.", GitHosting, GitHub, true, "/graphql"},
	} {
		e, ok := Lookup(name(t, tc.host))
		if !ok || e.Profile != tc.profile || e.Kind != tc.kind || e.Allowed != tc.allowed || e.GraphQLPath != tc.graphQL {
			t.Errorf("Lookup(%q) = %+v, %v", tc.host, e, ok)
		}
	}
}

// TestLookupNotCovered names hosts the catalog leaves out: subdomains
// of covered hosts, and the hosts S07-11-http-policy, "model API",
// keeps outside every profile.
func TestLookupNotCovered(t *testing.T) {
	for _, host := range []string{
		"x.github.com", "githubusercontent.com", "claude.ai",
		"console.anthropic.com", "downloads.claude.ai",
		"raw.githubusercontent.com", "example.com",
	} {
		if e, ok := Lookup(name(t, host)); ok {
			t.Errorf("Lookup(%q) = %+v, want not covered", host, e)
		}
	}
	if _, ok := Lookup(dnsname.Name{}); ok {
		t.Error("the zero Name is covered")
	}
}

// TestEntries checks the catalog has each host once, parts for each
// profile and kind, and GraphQL paths in canonical form, so the lock
// compares with what a request canonicalizes to.
func TestEntries(t *testing.T) {
	seen := map[dnsname.Name]bool{}
	for _, e := range Entries() {
		if seen[e.Host] {
			t.Errorf("%s is in the catalog twice", e.Host)
		}
		seen[e.Host] = true
		if Parts(e.Profile, e.Kind) == nil {
			t.Errorf("%s: profile %s kind %q has no parts", e.Host, e.Profile, e.Kind)
		}
		if e.GraphQLPath != "" && (e.Kind == NoKind || !KnownPart(e.Profile, e.Kind, "graphql")) {
			t.Errorf("%s has a GraphQL path but its kind has no graphql part", e.Host)
		}
		if c, r := match.CanonicalPath(e.GraphQLPath); e.GraphQLPath != "" && (r != nil || c != e.GraphQLPath) {
			t.Errorf("%s: GraphQL path %q is not canonical", e.Host, e.GraphQLPath)
		}
	}
	if len(seen) != len(catalog) {
		t.Errorf("Entries has %d hosts, the catalog %d", len(seen), len(catalog))
	}
}

func TestParts(t *testing.T) {
	for _, tc := range []struct {
		p    Profile
		k    Kind
		part string
		want bool
	}{
		{GitHosting, GitHub, "git", true},
		{GitHosting, GitHub, "graphql", true},
		{GitHosting, Gitea, "graphql", false},
		{GitHosting, NoKind, "git", false},
		{ModelAPI, NoKind, "model", true},
		{ModelAPI, GitHub, "model", false},
		{PackageRegistries, NoKind, "homebrew", true},
		{PackageRegistries, NoKind, "Homebrew", false},
		{"other", NoKind, "model", false},
	} {
		if got := KnownPart(tc.p, tc.k, tc.part); got != tc.want {
			t.Errorf("KnownPart(%s, %q, %q) = %v, want %v", tc.p, tc.k, tc.part, got, tc.want)
		}
	}
	p := Parts(ModelAPI, NoKind)
	p[0] = "changed"
	if !KnownPart(ModelAPI, NoKind, "model") {
		t.Error("Parts returned the catalog's own slice")
	}
}
