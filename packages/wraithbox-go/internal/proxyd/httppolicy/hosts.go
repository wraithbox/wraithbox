package httppolicy

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// hostRules are the built-in rules of one host: a host the catalog
// covers, or a host a project's remote names (S07-08-requests-and-credentials,
// "Named places").
type hostRules struct {
	profile builtin.Profile
	kind    builtin.Kind
	// remote marks a host the catalog doesn't cover, which has the git
	// hosting profile because a remote names it. Its kind isn't known.
	remote      bool
	graphQLPath string
	allows      []builtin.Allow
	denies      []builtin.Deny
	// ports are the ports the allow rules apply on. The denies and the
	// project set check apply on every port.
	ports []uint16
	// repos is the project set on this host: the repository paths of
	// each project with a session in the VM, in the form repoKey gives
	// them.
	repos map[string]bool
	// forms are the path forms a write names its repository in.
	forms repoForms
}

// repoForms says which path forms name a repository on a host.
type repoForms struct {
	transport bool
	githubAPI bool
	gitlabAPI bool
	giteaAPI  bool
}

// apiForms are the API forms of the catalog's git hosts. git's
// transport is read on github.com, gitlab.com, codeberg.org and every
// host a remote names. A host of the catalog not listed here names no
// repository, so every write to it is refused.
var apiForms = map[string]repoForms{
	"github.com":         {transport: true},
	"api.github.com":     {githubAPI: true},
	"uploads.github.com": {githubAPI: true},
	"gitlab.com":         {transport: true, gitlabAPI: true},
	"codeberg.org":       {transport: true, giteaAPI: true},
}

// sharedRepos names the hosts whose API writes to the repositories of
// another host: GitHub's API is on hosts of its own, and `github.com`
// remotes name its repositories.
var sharedRepos = map[string]string{
	"api.github.com":     "github.com",
	"uploads.github.com": "github.com",
}

func mustName(s string) dnsname.Name {
	n, r := dnsname.FromHost(s)
	if r != nil {
		panic("httppolicy: " + s + " is not a host name")
	}
	return n
}

// buildHosts makes the rules of each covered host, and of each host
// that a project's repository names.
func buildHosts(projects []policy.Project) map[dnsname.Name]*hostRules {
	hosts := map[dnsname.Name]*hostRules{}
	for _, entry := range builtin.Entries() {
		hosts[entry.Host] = &hostRules{
			profile:     entry.Profile,
			kind:        entry.Kind,
			graphQLPath: entry.GraphQLPath,
			allows:      builtin.Allows(entry.Host),
			denies:      builtin.Denies(entry.Host),
			ports:       []uint16{builtin.HTTPSPort},
			repos:       map[string]bool{},
			forms:       apiForms[entry.Host.String()],
		}
	}
	for api, web := range sharedRepos {
		hosts[mustName(api)].repos = hosts[mustName(web)].repos
	}
	for _, pr := range projects {
		for _, compare := range pr.Repositories {
			addRepository(hosts, compare)
		}
	}
	return hosts
}

// addRepository adds a compare form (gitremote.Result.Compare) to the
// project set. A form that names no host, such as a local path or a
// hash, adds nothing. A host the catalog doesn't cover gets the rules
// of a remote host. An `https://` or `http://` form adds its port to
// the ports git's transport is allowed on.
func addRepository(hosts map[dnsname.Name]*hostRules, compare string) {
	u, err := url.Parse(compare)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return
	}
	n, r := dnsname.FromHost(u.Hostname())
	if r != nil {
		return
	}
	h, ok := hosts[n]
	if !ok {
		allows, denies := builtin.RemoteRules(n)
		h = &hostRules{
			profile: builtin.GitHosting,
			remote:  true,
			allows:  allows,
			denies:  denies,
			repos:   map[string]bool{},
			forms:   repoForms{transport: true},
		}
		hosts[n] = h
	}
	if h.remote {
		h.addPort(u)
	}
	path, pr := match.CanonicalPath(u.EscapedPath())
	if pr != nil {
		return
	}
	if key, ok := h.repoKey(path); ok {
		h.repos[key] = true
	}
}

// addPort adds the HTTP port of a remote's URL.
func (h *hostRules) addPort(u *url.URL) {
	var port uint16
	switch strings.ToLower(u.Scheme) {
	case "https":
		port = builtin.HTTPSPort
	case "http":
		port = policy.PlainHTTPPort
	default:
		return
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return
		}
		port = uint16(n)
	}
	if !slices.Contains(h.ports, port) {
		h.ports = append(h.ports, port)
	}
}

// repoKey puts a repository path in the form the project set compares:
// without a trailing `/` or `.git`, and in lowercase on a GitHub host,
// because GitHub compares owner and repository names without regard to
// case. Other forges are compared case-sensitively, which fails closed
// where a forge doesn't (SEC06-repo-writes). It reports false for a path
// that names no repository.
func (h *hostRules) repoKey(path string) (string, bool) {
	path = strings.TrimSuffix(path, "/")
	if h.kind == builtin.GitHub {
		path = strings.ToLower(path)
	}
	path = strings.TrimSuffix(path, ".git")
	if len(path) < 2 || path[0] != '/' {
		return "", false
	}
	return path, true
}
