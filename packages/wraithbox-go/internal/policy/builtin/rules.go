package builtin

import (
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The rules of the built-in denies in the table below
// (S07-11-http-policy, "git hosting", "package registries").
const (
	// RuleArchive denies an archive download from a git host
	// (S07-12-dependency-gate, "Scope").
	RuleArchive = "git-archive"
	// RuleCreate denies gists, repository creation and forks
	// (S07-11-http-policy, "git hosting", SEC06-repo-writes).
	RuleCreate = "git-create-denied"
	// RuleRegistryToken denies `ghcr.io/token` to the guest
	// (S07-10-anonymous-credentials).
	RuleRegistryToken = "registry-token"
	// RuleEventLog denies Anthropic's event log on the model API
	// (S07-11-http-policy, "model API").
	RuleEventLog = "model-event-log"
)

// HTTPSPort is the port the built-in allow rules apply on.
const HTTPSPort = 443

// Allow is one built-in allow rule, in the part it belongs to
// (S07-11-http-policy, "Profile parts").
type Allow struct {
	Profile Profile
	// Kind is the kind of a git hosting host, and NoKind for the other
	// profiles and for a host a remote names whose kind isn't known.
	Kind Kind
	Part string
	Rule match.Rule
}

// Deny is one built-in deny. It belongs to no part, applies on every
// port, and its path pattern is in lowercase: the caller matches it
// against the request's path in lowercase, so a host that routes paths
// without regard to case can't be reached past it.
type Deny struct {
	Name string
	Rule match.Rule
}

// ruleSpec is a table line before its method and path are compiled.
// methods is a space-separated list.
type ruleSpec struct {
	host    string
	part    string
	methods string
	path    string
}

// writeMethods are the methods of the REST writes the git hosting
// profile allows inside the project set. The project set check limits
// them, and any other write method (S07-11-http-policy, "git hosting").
const writeMethods = "POST PUT PATCH DELETE"

// allowTable holds the built-in allow rules of the covered hosts, in
// the order a request is put in a part: the first rule that matches a
// request names its part. So git's transport comes before `web`, whose
// reads cover every path, and the GraphQL path before `rest`.
var allowTable = []ruleSpec{
	// GitHub (S07-11-http-policy, "GitHub parts"). Push is limited to
	// the project set by the deny of SEC06-repo-writes.
	{"github.com", "git", "GET", "/*/*/info/refs"},
	{"github.com", "git", "POST", "/*/*/git-upload-pack"},
	{"github.com", "git", "POST", "/*/*/git-receive-pack"},
	// Refused by the GraphQL filter until M3.7b (graphql-operation).
	{"api.github.com", "graphql", "POST", "/graphql"},
	{"api.github.com", "rest", "GET OPTIONS", "/**"},
	{"api.github.com", "rest", writeMethods, "/repos/*/*"},
	{"api.github.com", "rest", writeMethods, "/repos/*/*/**"},
	// Release asset uploads.
	{"uploads.github.com", "rest", "POST", "/repos/*/*/releases/*/assets"},
	{"github.com", "web", "GET OPTIONS", "/**"},
	{"objects.githubusercontent.com", "web", "GET", "/**"},

	// GitLab (S07-11-http-policy, "Other parts"). A project path can
	// have nested groups, so git's transport takes `**`.
	{"gitlab.com", "git", "GET", "/**/info/refs"},
	{"gitlab.com", "git", "POST", "/**/git-upload-pack"},
	{"gitlab.com", "git", "POST", "/**/git-receive-pack"},
	{"gitlab.com", "graphql", "POST", "/api/graphql"},
	{"gitlab.com", "rest", "GET OPTIONS", "/api/v4/**"},
	{"gitlab.com", "rest", writeMethods, "/api/v4/projects/*"},
	{"gitlab.com", "rest", writeMethods, "/api/v4/projects/*/**"},
	{"gitlab.com", "web", "GET OPTIONS", "/**"},

	// Gitea and Forgejo (S07-11-http-policy, "Other parts").
	{"codeberg.org", "git", "GET", "/*/*/info/refs"},
	{"codeberg.org", "git", "POST", "/*/*/git-upload-pack"},
	{"codeberg.org", "git", "POST", "/*/*/git-receive-pack"},
	{"codeberg.org", "rest", "GET OPTIONS", "/api/v1/**"},
	{"codeberg.org", "rest", writeMethods, "/api/v1/repos/*/*"},
	{"codeberg.org", "rest", writeMethods, "/api/v1/repos/*/*/**"},
	{"codeberg.org", "web", "GET OPTIONS", "/**"},

	// model API (S07-11-http-policy, "model API"). X01-model-credential
	// recorded paths, not methods: the methods are those of the
	// Anthropic API (`/v1/messages`) and of a read for the others,
	// except the feature flag evaluation, which posts its attributes.
	{"api.anthropic.com", "model", "POST", "/v1/messages"},
	{"api.anthropic.com", "model", "GET", "/api/claude_code/policy_limits"},
	{"api.anthropic.com", "model", "GET", "/api/claude_code/settings"},
	{"api.anthropic.com", "model", "GET", "/api/claude_cli/bootstrap"},
	{"api.anthropic.com", "model", "POST", "/api/eval/*"},
	{"api.anthropic.com", "model", "GET", "/mcp-registry/v0/servers"},
	{"api.anthropic.com", "model", "GET", "/api/hello"},
	{"platform.claude.com", "model", "GET", "/v1/oauth/hello"},

	// package registries: reads only (S07-11-http-policy, "package
	// registries"). A GET rule matches HEAD too (match.Method).
	{"registry.npmjs.org", "npm", "GET", "/**"},
	{"pypi.org", "pypi", "GET", "/**"},
	{"files.pythonhosted.org", "pypi", "GET", "/**"},
	{"crates.io", "crates", "GET", "/**"},
	{"static.crates.io", "crates", "GET", "/**"},
	{"index.crates.io", "crates", "GET", "/**"},
	{"proxy.golang.org", "go", "GET", "/**"},
	// The checksum database, read-only (S07-14-go-lookups).
	{"sum.golang.org", "go", "GET", "/lookup/**"},
	{"sum.golang.org", "go", "GET", "/latest"},
	{"sum.golang.org", "go", "GET", "/tile/**"},
	// Homebrew bottles (S07-10-anonymous-credentials,
	// X22-no-guest-credentials).
	{"ghcr.io", "homebrew", "GET", "/v2/homebrew/core/**"},
	{"pkg-containers.githubusercontent.com", "homebrew", "GET", "/**"},
	{"formulae.brew.sh", "homebrew", "GET", "/**"},
}

// transportTable is git's smart HTTP transport, which a host a remote
// names gets while its kind isn't known
// (S07-08-requests-and-credentials, "Named places"). The host is filled
// in per host. Its part is `git`.
var transportTable = []ruleSpec{
	{"", "git", "GET", "/**/info/refs"},
	{"", "git", "POST", "/**/git-upload-pack"},
	{"", "git", "POST", "/**/git-receive-pack"},
}

// denySpec is a deny before it is compiled. part holds the rule.
type denySpec = ruleSpec

// The archive downloads of each kind (S07-12-dependency-gate, "Scope").
// A `%2F` stays inside its segment, so `*` matches a ref name with an
// encoded slash and the deny holds (S07-08-requests-and-credentials,
// "Encoded slashes").
var (
	githubArchives = []string{"/*/*/archive/**", "/*/*/tarball/**", "/*/*/zipball/**"}
	gitlabArchives = []string{
		"/**/-/archive/**",
		"/api/v4/projects/*/repository/archive*",
		"/api/v4/projects/*/repository/archive*/**",
	}
	giteaArchives = []string{"/*/*/archive/**", "/api/v1/repos/*/*/archive/**"}
)

// denyTable holds the built-in denies of the covered hosts. The writes
// outside the project set and the registry writes are decided in code
// (wb-proxyd's httppolicy), because they depend on the method and the
// project set, not on one method and path.
var denyTable = concat(
	// codeload.github.com serves archives and nothing else.
	[]denySpec{{"codeload.github.com", RuleArchive, "*", "/**"}},
	denies("github.com", RuleArchive, "*", githubArchives...),
	denies("api.github.com", RuleArchive, "*",
		"/repos/*/*/tarball", "/repos/*/*/tarball/**", "/repos/*/*/zipball", "/repos/*/*/zipball/**"),
	// Gists, with every method: a gist is a public place to write to
	// (SEC06-repo-writes).
	[]denySpec{{"gist.github.com", RuleCreate, "*", "/**"}},
	denies("api.github.com", RuleCreate, "*", "/gists", "/gists/**"),
	denies("api.github.com", RuleCreate, "POST",
		"/user/repos", "/orgs/*/repos", "/repos/*/*/forks", "/repos/*/*/generate"),
	denies("gitlab.com", RuleArchive, "*", gitlabArchives...),
	denies("gitlab.com", RuleCreate, "*", "/api/v4/snippets", "/api/v4/snippets/**"),
	denies("gitlab.com", RuleCreate, "POST",
		"/api/v4/projects", "/api/v4/projects/user/*", "/api/v4/projects/import",
		"/api/v4/projects/*/fork", "/api/v4/projects/*/fork/**"),
	denies("codeberg.org", RuleArchive, "*", giteaArchives...),
	denies("codeberg.org", RuleCreate, "POST",
		"/api/v1/user/repos", "/api/v1/orgs/*/repos", "/api/v1/repos/migrate",
		"/api/v1/repos/*/*/forks", "/api/v1/repos/*/*/generate"),
	// The event log, with `;` parameters a server may strip
	// (S07-11-http-policy, "model API", "Event log denied").
	denies("api.anthropic.com", RuleEventLog, "*",
		"/api/event_logging", "/api/event_logging/**", "/api/event_logging;**"),
	denies("ghcr.io", RuleRegistryToken, "*", "/token", "/token/**", "/token;**"),
)

// remoteDenyPaths are the archive denies of a host a remote names
// whose kind isn't known: the union of every kind's, as its credential
// names are (S07-08-requests-and-credentials, "Named places"). Its
// writes outside git's transport can't name a repository, so the
// project set check refuses them, creation and forks included.
var remoteDenyPaths = concat(githubArchives, gitlabArchives, giteaArchives)

func concat[T any](lists ...[]T) []T {
	var out []T
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func denies(host, rule, methods string, paths ...string) []denySpec {
	out := make([]denySpec, 0, len(paths))
	for _, p := range paths {
		out = append(out, denySpec{host, rule, methods, p})
	}
	return out
}

// The tables, compiled once.
var allowsByHost, deniesByHost = compileTables()

func compileTables() (map[dnsname.Name][]Allow, map[dnsname.Name][]Deny) {
	allowsByHost := map[dnsname.Name][]Allow{}
	deniesByHost := map[dnsname.Name][]Deny{}
	for _, s := range allowTable {
		e, ok := Lookup(mustName(s.host))
		if !ok || !e.Allowed {
			panic("builtin: allow rule on " + s.host + ", which isn't an allowed covered host")
		}
		if !KnownPart(e.Profile, e.Kind, s.part) {
			panic("builtin: allow rule on " + s.host + " in unknown part " + s.part)
		}
		allowsByHost[e.Host] = append(allowsByHost[e.Host], compileAllows(e.Host, e.Profile, e.Kind, s)...)
	}
	for _, s := range denyTable {
		n := mustName(s.host)
		if _, ok := Lookup(n); !ok {
			panic("builtin: deny on " + s.host + ", which isn't a covered host")
		}
		deniesByHost[n] = append(deniesByHost[n], compileDenies(n, s)...)
	}
	return allowsByHost, deniesByHost
}

func mustName(s string) dnsname.Name {
	n, r := dnsname.FromHost(s)
	if r != nil || n.String() != s {
		panic("builtin: rule host " + s + " is not in canonical form")
	}
	return n
}

func compileRules(host dnsname.Name, s ruleSpec) []match.Rule {
	p, r := match.Compile(s.path)
	if r != nil {
		panic("builtin: rule path " + s.path + ": " + r.Error())
	}
	var out []match.Rule
	for m := range strings.FieldsSeq(s.methods) {
		cm, r := match.CompileMethod(m)
		if r != nil || string(cm) != m {
			panic("builtin: rule method " + m + " is not in canonical form")
		}
		out = append(out, match.Rule{Host: match.ExactHost(host), Method: cm, Path: p})
	}
	return out
}

func compileAllows(host dnsname.Name, p Profile, k Kind, s ruleSpec) []Allow {
	var out []Allow
	for _, r := range compileRules(host, s) {
		out = append(out, Allow{Profile: p, Kind: k, Part: s.part, Rule: r})
	}
	return out
}

func compileDenies(host dnsname.Name, s denySpec) []Deny {
	if strings.ToLower(s.path) != s.path {
		panic("builtin: deny path " + s.path + " is not in lowercase")
	}
	var out []Deny
	for _, r := range compileRules(host, s) {
		out = append(out, Deny{Name: s.part, Rule: r})
	}
	return out
}

// Allows returns the built-in allow rules of a covered host, in the
// order that puts a request in a part (allowTable), and nil for any
// other host.
func Allows(n dnsname.Name) []Allow { return slices.Clone(allowsByHost[n]) }

// Denies returns the built-in denies of a covered host, and nil for any
// other host.
func Denies(n dnsname.Name) []Deny { return slices.Clone(deniesByHost[n]) }

// RemoteRules returns the rules of a host a project's remote names that
// the catalog doesn't cover: git's smart HTTP transport, in the part
// `git` with no kind, and the archive denies of every kind.
func RemoteRules(n dnsname.Name) ([]Allow, []Deny) {
	var allows []Allow
	for _, s := range transportTable {
		allows = append(allows, compileAllows(n, GitHosting, NoKind, s)...)
	}
	var out []Deny
	for _, p := range remoteDenyPaths {
		out = append(out, compileDenies(n, denySpec{"", RuleArchive, "*", p})...)
	}
	return allows, out
}
