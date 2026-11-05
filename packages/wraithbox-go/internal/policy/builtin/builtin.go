// Package builtin is the catalog of the hosts the built-in profiles
// cover (S07-11-http-policy, "Covered hosts"): for each host its
// profile, its kind in the git hosting profile, whether the profile
// allows requests to it or only covers it, and its GraphQL path. It
// also names the parts of each profile and kind (S07-11-http-policy,
// "Profile parts").
//
// A covered host is a guarded host (S07-07-hosts-and-modes, "Guarded
// hosts"): it can't be a pass host, have a raw TCP entry, or be in
// `audit`. The allow rules of each part and the built-in denies are in
// rules.go. wb-proxyd decides requests with them (S07-11-http-policy).
package builtin

import (
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

// Profile is a built-in profile (S07-11-http-policy).
type Profile string

// The built-in profiles.
const (
	GitHosting        Profile = "git-hosting"
	ModelAPI          Profile = "model-api"
	PackageRegistries Profile = "package-registries"
)

// Kind is the kind of a git hosting host (S07-08-requests-and-credentials,
// "Named places"). Hosts of the other profiles have no kind.
type Kind string

// The kinds of the git hosting profile.
const (
	NoKind Kind = ""
	GitHub Kind = "github"
	GitLab Kind = "gitlab"
	// Gitea is the kind of Gitea and Forgejo hosts.
	Gitea Kind = "gitea"
)

// Entry is one covered host.
type Entry struct {
	Host    dnsname.Name
	Profile Profile
	Kind    Kind
	// Allowed is false for a host the profile covers but allows no
	// request to: the hosts on which a forge serves SSH on port 443
	// (S07-11-http-policy, "git hosting").
	Allowed bool
	// GraphQLPath is the canonical path of the host's GraphQL
	// endpoint, which the GraphQL lock of S09-02-precedence protects,
	// or "" when the host has none.
	GraphQLPath string
}

// entry is a catalog line before its host is checked.
type entry struct {
	host        string
	profile     Profile
	kind        Kind
	allowed     bool
	graphQLPath string
}

// catalog is the table of S07-11-http-policy, "Covered hosts". Keep the
// two in step.
var catalog = []entry{
	{"github.com", GitHosting, GitHub, true, ""},
	{"api.github.com", GitHosting, GitHub, true, "/graphql"},
	{"codeload.github.com", GitHosting, GitHub, true, ""},
	{"ssh.github.com", GitHosting, GitHub, false, ""},
	{"gist.github.com", GitHosting, GitHub, true, ""},
	{"uploads.github.com", GitHosting, GitHub, true, ""},
	{"objects.githubusercontent.com", GitHosting, GitHub, true, ""},
	{"gitlab.com", GitHosting, GitLab, true, "/api/graphql"},
	{"altssh.gitlab.com", GitHosting, GitLab, false, ""},
	{"codeberg.org", GitHosting, Gitea, true, ""},
	{"api.anthropic.com", ModelAPI, NoKind, true, ""},
	{"platform.claude.com", ModelAPI, NoKind, true, ""},
	{"registry.npmjs.org", PackageRegistries, NoKind, true, ""},
	{"pypi.org", PackageRegistries, NoKind, true, ""},
	{"files.pythonhosted.org", PackageRegistries, NoKind, true, ""},
	{"crates.io", PackageRegistries, NoKind, true, ""},
	{"static.crates.io", PackageRegistries, NoKind, true, ""},
	{"index.crates.io", PackageRegistries, NoKind, true, ""},
	{"proxy.golang.org", PackageRegistries, NoKind, true, ""},
	{"sum.golang.org", PackageRegistries, NoKind, true, ""},
	{"ghcr.io", PackageRegistries, NoKind, true, ""},
	{"pkg-containers.githubusercontent.com", PackageRegistries, NoKind, true, ""},
	{"formulae.brew.sh", PackageRegistries, NoKind, true, ""},
}

// entries is the catalog with checked hosts, built once.
var entries = buildEntries()

func buildEntries() []Entry {
	out := make([]Entry, 0, len(catalog))
	for _, e := range catalog {
		n, r := dnsname.FromHost(e.host)
		if r != nil || n.String() != e.host {
			panic("builtin: catalog host " + e.host + " is not in canonical form")
		}
		out = append(out, Entry{Host: n, Profile: e.profile, Kind: e.kind, Allowed: e.allowed, GraphQLPath: e.graphQLPath})
	}
	return out
}

// Lookup returns the entry of a covered host, and false for any other
// host. Only exact names are covered: a subdomain of a covered host is
// not.
func Lookup(n dnsname.Name) (Entry, bool) {
	for _, e := range entries {
		if e.Host == n {
			return e, true
		}
	}
	return Entry{}, false
}

// Entries returns every covered host, in the order of the table.
func Entries() []Entry { return slices.Clone(entries) }

// The parts of each profile and kind (S07-11-http-policy, "Profile
// parts").
var parts = map[Profile]map[Kind][]string{
	GitHosting: {
		GitHub: {"git", "rest", "graphql", "web"},
		GitLab: {"git", "rest", "graphql", "web"},
		Gitea:  {"git", "rest", "web"},
	},
	ModelAPI:          {NoKind: {"model"}},
	PackageRegistries: {NoKind: {"npm", "pypi", "crates", "go", "homebrew"}},
}

// Parts returns the part names of a profile and kind, and nil for a
// pair that has none. The git hosting profile's parts are per kind, and
// the other profiles' parts are under NoKind.
func Parts(p Profile, k Kind) []string { return slices.Clone(parts[p][k]) }

// KnownPart reports whether a profile and kind have a part of that
// name.
func KnownPart(p Profile, k Kind, part string) bool {
	return slices.Contains(parts[p][k], part)
}
