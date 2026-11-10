package httppolicy

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
)

// TestProjectSet is C03-policy-and-approvals, "The project set and
// learn mode", the unit part (SEC06-repo-writes).
func TestProjectSet(t *testing.T) {
	plain := policy.Policy{Projects: []policy.Project{
		project(projectA, repoA, repoB, repoC, "https://git.example.net/team/tool.git"),
	}}
	broad := broadPolicy()
	broad.Endpoints = append(broad.Endpoints,
		rest(projectBSrc, "uploads", "uploads.github.com", []string{"* /**"}))
	broad.Projects[0].Repositories = plain.Projects[0].Repositories
	for name, p := range map[string]policy.Policy{"no user rule": plain, "a user rule allows every write": broad} {
		e := quietEngine(t, p)
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				req   string
				allow bool
				rule  string
			}{
				// Push and API writes to a project's repository.
				{"GET github.com/acme/app.git/info/refs?service=git-receive-pack", true, RuleAllowed},
				{"POST github.com/acme/app.git/git-receive-pack", true, RuleAllowed},
				{"POST github.com/acme/app/git-receive-pack", true, RuleAllowed},
				{"POST api.github.com/repos/acme/app/issues", true, RuleAllowed},
				{"PATCH api.github.com/repos/acme/app/pulls/1", true, RuleAllowed},
				{"POST uploads.github.com/repos/acme/app/releases/1/assets", true, RuleAllowed},
				{"POST gitlab.com/group/sub/proj.git/git-receive-pack", true, RuleAllowed},
				{"PUT gitlab.com/api/v4/projects/group%2Fsub%2Fproj/repository/files/a", true, RuleAllowed},
				{"POST codeberg.org/owner/lib.git/git-receive-pack", true, RuleAllowed},
				{"POST codeberg.org/api/v1/repos/owner/lib/issues", true, RuleAllowed},
				// GitHub compares names without regard to case.
				{"POST github.com/ACME/App.git/git-receive-pack", true, RuleAllowed},
				{"POST api.github.com/repos/Acme/APP/issues", true, RuleAllowed},
				// Fetches aren't writes.
				{"GET github.com/other/repo.git/info/refs?service=git-upload-pack", true, RuleAllowed},
				{"POST github.com/other/repo.git/git-upload-pack", true, RuleAllowed},
				// The same writes to another repository.
				{"GET github.com/other/repo.git/info/refs?service=git-receive-pack", false, RuleWriteOutside},
				{"GET github.com/other/repo.git/info/refs?service=git-upload-pack&service=git-receive-pack", false, RuleWriteOutside},
				{"GET github.com/other/repo.git/info/refs?SERVICE=git-receive-pack", false, RuleWriteOutside},
				{"GET github.com/other/repo.git/info/refs?service=git%2Dreceive-pack", false, RuleWriteOutside},
				{"GET github.com/other/repo.git/info/refs?service=%zz", false, RuleWriteOutside},
				{"POST github.com/other/repo.git/git-receive-pack", false, RuleWriteOutside},
				{"POST github.com/acme/app2.git/git-receive-pack", false, RuleWriteOutside},
				{"POST github.com/acme/app/x/git-receive-pack", false, RuleWriteOutside},
				{"POST github.com/acme%2Fapp/git-receive-pack", false, RuleWriteOutside},
				{"POST api.github.com/repos/other/repo/issues", false, RuleWriteOutside},
				{"POST api.github.com/repos/acme/app.git/issues", false, RuleWriteOutside},
				{"POST uploads.github.com/repos/other/repo/releases/1/assets", false, RuleWriteOutside},
				{"POST gitlab.com/group/proj.git/git-receive-pack", false, RuleWriteOutside},
				// GitLab compares with letter case.
				{"POST gitlab.com/Group/sub/proj.git/git-receive-pack", false, RuleWriteOutside},
				{"POST codeberg.org/Owner/lib.git/git-receive-pack", false, RuleWriteOutside},
				// A repository that can't be read from the path.
				{"PUT gitlab.com/api/v4/projects/42/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/group%2Fsub%2Fproj%3F/x", false, RuleWriteOutside},
				// A GitLab id with an empty segment is malformed.
				{"POST gitlab.com/api/v4/projects/group%2Fsub%2Fproj%2F", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/group%2Fsub%2Fproj%2F/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/%2Fgroup%2Fsub%2Fproj/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/group%2F%2Fsub%2Fproj/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/group%2Fsub%2F%2Fproj/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/%2Fgroup%2Fsub%2Fproj%2F/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/%2F/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/%2F%2F/repository/files/a", false, RuleWriteOutside},
				{"PUT gitlab.com/api/v4/projects/group%2Fsub%2Fproj%2F%2F/repository/files/a", false, RuleWriteOutside},
				{"POST api.github.com/user/keys", false, RuleWriteOutside},
				{"POST github.com/acme/app/issues", false, RuleWriteOutside},
				{"POST github.com/repos/acme/app/issues", false, RuleWriteOutside},
				{"POST objects.githubusercontent.com/x", false, RuleWriteOutside},
				{"PROPFIND github.com/acme/app", false, RuleWriteOutside},
				// An API path that ends like git's transport is no fetch.
				{"POST codeberg.org/api/v1/repos/victim/repo/contents/git-upload-pack", false, RuleWriteOutside},
				{"POST gitlab.com/api/v4/projects/123/repository/files/git-upload-pack", false, RuleWriteOutside},
				{"POST gitlab.com/api/v4/projects/other%2Frepo/repository/files/git-upload-pack", false, RuleWriteOutside},
				{"POST gitlab.com/api/v4/projects/group%2Fsub%2Fproj/repository/files/git-receive-pack", true, RuleAllowed},
				{"POST git.example.net/api/v1/repos/victim/repo/contents/git-upload-pack", false, RuleWriteOutside},
				{"POST api.github.com/repos/other/repo/x/git-upload-pack", false, RuleWriteOutside},
				{"POST github.com/other/repo/x/git-upload-pack", false, RuleWriteOutside},
				{"GET git.example.net/api/x/info/refs", false, RuleNoRule},
				// GitLab's `-` segment is a web route, never a repository.
				{"POST gitlab.com/other/proj/-/create/main/git-upload-pack", false, RuleWriteOutside},
				// Known gap until M4.10: a remote host's API path that
				// doesn't start with `api` still reads as git's transport.
				{"POST git.example.net/rest/api/1.0/x/git-upload-pack", true, RuleAllowed},
				{"POST codeberg.org/other/repo/x/git-upload-pack", false, RuleWriteOutside},
				// Gists, creation and forks, even of a project's repository.
				{"POST gist.github.com/acme/app.git/git-receive-pack", false, builtin.RuleCreate},
				{"POST api.github.com/user/repos", false, builtin.RuleCreate},
				{"POST api.github.com/repos/acme/app/forks", false, builtin.RuleCreate},
				{"POST api.github.com/repos/acme/app/generate", false, builtin.RuleCreate},
				// A host a remote names: transport only, project set
				// enforced, on the port of its https form.
				{"POST git.example.net/team/tool.git/git-receive-pack", true, RuleAllowed},
				{"POST git.example.net/team/other.git/git-receive-pack", false, RuleWriteOutside},
				{"POST git.example.net/api/v1/repos/team/tool/issues", false, RuleWriteOutside},
				{"GET git.example.net/team/tool/archive/main.zip", false, builtin.RuleArchive},
			} {
				r := req(tc.req)
				want(t, e, r, tc.allow, tc.rule)
			}
		})
	}
}

// TestRemoteHost covers a host a remote names that the catalog
// doesn't: git's transport on the port of its URL, nothing else, and
// its part off when any kind's `git` part is.
func TestRemoteHost(t *testing.T) {
	p := policy.Policy{Projects: []policy.Project{project(projectA,
		"https://git.example.net:8443/team/tool.git", "http://plain.example.net/a/b", "git://other.example.net/x/y")}}
	e := quietEngine(t, p)
	want(t, e, onPort(req("POST git.example.net/team/tool.git/git-receive-pack"), 8443), true, RuleAllowed)
	want(t, e, req("POST git.example.net/team/tool.git/git-receive-pack"), false, RuleNoRule)
	want(t, e, onPort(req("GET git.example.net/team/tool"), 8443), false, RuleNoRule)
	want(t, e, onPort(req("GET plain.example.net/a/b/info/refs?service=git-upload-pack"), 80), true, RuleAllowed)
	want(t, e, req("POST other.example.net/x/z/git-receive-pack"), false, RuleWriteOutside)
	want(t, e, req("POST other.example.net/x/y/git-receive-pack"), false, RuleNoRule)

	p.PartsOff = []policy.PartOff{{Profile: "git-hosting", Kind: "gitlab", Part: "git", Source: globalSrc}}
	e = quietEngine(t, p)
	d := want(t, e, onPort(req("POST git.example.net/team/tool.git/git-upload-pack"), 8443), false, RulePartOff)
	if len(d.OffBy) != 1 || d.OffBy[0] != globalSrc {
		t.Errorf("off by %v, want the global policy", d.OffBy)
	}
}

// TestEncodedSlashes is S07-08-requests-and-credentials, "Encoded
// slashes": a `%2F` is data inside its segment.
func TestEncodedSlashes(t *testing.T) {
	p := policy.Policy{Endpoints: []policy.Endpoint{
		rest(globalSrc, "one-seg", "registry.npmjs.org", []string{"GET /**"}, "GET /*"),
	}}
	e := quietEngine(t, p)
	d := want(t, e, req("GET registry.npmjs.org/@scope%2fname"), false, RuleDenyRule)
	if d.Path != "/@scope%2Fname" {
		t.Errorf("canonical path %q", d.Path)
	}
	want(t, e, req("GET registry.npmjs.org/@scope/name"), true, RuleAllowed)

	e = quietEngine(t, broadPolicy())
	for _, r := range []string{
		"GET github.com/acme/app/archive/refs%2Fheads%2Fmain.tar.gz",
		"GET github.com/acme/app/archive/refs%2fheads%2fmain.tar.gz",
		"GET api.github.com/repos/acme/app/tarball/refs%2Fheads%2Fmain",
		"GET gitlab.com/group/sub/proj/-/archive/a%2Fb/proj.zip",
		"GET codeberg.org/owner/lib/archive/a%2Fb.zip",
	} {
		want(t, e, req(r), false, builtin.RuleArchive)
	}
}

// TestModelAPI covers S07-11-http-policy, "model API".
func TestModelAPI(t *testing.T) {
	e := quietEngine(t, policy.Policy{})
	for _, r := range []string{
		"POST api.anthropic.com/v1/messages",
		"GET api.anthropic.com/api/claude_code/policy_limits",
		"GET api.anthropic.com/api/claude_code/settings",
		"GET api.anthropic.com/api/claude_cli/bootstrap",
		"POST api.anthropic.com/api/eval/sdk-abc",
		"GET api.anthropic.com/mcp-registry/v0/servers",
		"GET api.anthropic.com/api/hello",
		"HEAD api.anthropic.com/api/hello",
		"GET platform.claude.com/v1/oauth/hello",
	} {
		d := want(t, e, req(r), true, RuleAllowed)
		if d.Source.String() != "builtin model-api/-/model" {
			t.Errorf("%s source %q", r, d.Source)
		}
	}
	for _, tc := range []struct{ req, rule string }{
		{"GET api.anthropic.com/v1/messages", RuleNoRule},
		{"POST api.anthropic.com/v1/oauth/token", RuleNoRule},
		{"POST platform.claude.com/v1/oauth/token", RuleNoRule},
		{"POST api.anthropic.com/api/event_logging/v2/batch", builtin.RuleEventLog},
		{"POST api.anthropic.com/api/event_logging", builtin.RuleEventLog},
		{"POST api.anthropic.com/v1/%6Dessages", RuleModelPathEscape},
		{"POST api.anthropic.com/api/event%5Flogging/v2/batch", RuleModelPathEscape},
		{"POST api.anthropic.com/api/event_logging%2Fv2/batch", RuleModelPathEscape},
		{"GET platform.claude.com/v1/oauth/%68ello", RuleModelPathEscape},
		{"GET downloads.claude.ai/claude-code-releases/latest", RuleNoRule},
		{"GET raw.githubusercontent.com/acme/app/refs/heads/main/CHANGELOG.md", RuleNoRule},
	} {
		want(t, e, req(tc.req), false, tc.rule)
	}
	// The event log deny holds against a user rule that allows it.
	e = quietEngine(t, broadPolicy())
	want(t, e, req("POST api.anthropic.com/api/event_logging/v2/batch"), false, builtin.RuleEventLog)
}

// TestRegistries covers S07-11-http-policy, "package registries".
func TestRegistries(t *testing.T) {
	e := quietEngine(t, policy.Policy{})
	for _, r := range []string{
		"GET registry.npmjs.org/left-pad",
		"GET files.pythonhosted.org/packages/a/b.whl",
		"HEAD static.crates.io/crates/x/x-1.0.crate",
		"GET proxy.golang.org/golang.org/x/net/@v/list",
		"GET sum.golang.org/lookup/golang.org/x/net@v0.1.0",
		"GET sum.golang.org/latest",
		"GET sum.golang.org/tile/8/0/001",
		"GET ghcr.io/v2/homebrew/core/wget/blobs/sha256:abc",
		"GET pkg-containers.githubusercontent.com/ghcr1/blobs/sha256:abc",
		"GET formulae.brew.sh/api/formula.jws.json",
	} {
		want(t, e, req(r), true, RuleAllowed)
	}
	for _, tc := range []struct{ req, rule string }{
		{"GET sum.golang.org/other", RuleNoRule},
		{"GET ghcr.io/v2/homebrew/cask/x/manifests/1", RuleNoRule},
		{"GET ghcr.io/v2/homebrew%2Fcore/wget/manifests/1", RuleNoRule},
		{"GET ghcr.io/token", builtin.RuleRegistryToken},
		{"PUT ghcr.io/v2/homebrew/core/wget/manifests/1", RuleRegistryPublish},
		{"POST pypi.org/legacy/", RuleRegistryPublish},
		{"PUT crates.io/api/v1/crates/new", RuleRegistryPublish},
	} {
		want(t, e, req(tc.req), false, tc.rule)
	}
}

// TestParts is C03-policy-and-approvals, "Profile parts", the
// wb-proxyd side: with only GitHub's `git` part on, fetch and push
// pass, and every REST and GraphQL request is refused with
// profile-part-off, naming the part and its source.
func TestParts(t *testing.T) {
	p := broadPolicy()
	for _, part := range []string{"rest", "graphql", "web"} {
		p.PartsOff = append(p.PartsOff, policy.PartOff{Profile: "git-hosting", Kind: "github", Part: part, Source: projectASrc})
	}
	p.PartsOff = append(p.PartsOff, policy.PartOff{Profile: "git-hosting", Kind: "github", Part: "rest", Source: globalSrc})
	e := quietEngine(t, p)
	for _, r := range []string{
		"GET github.com/acme/app.git/info/refs?service=git-upload-pack",
		"POST github.com/acme/app.git/git-upload-pack",
		"GET github.com/acme/app.git/info/refs?service=git-receive-pack",
		"POST github.com/acme/app.git/git-receive-pack",
	} {
		d := want(t, e, req(r), true, RuleAllowed)
		if d.Source.String() != "builtin git-hosting/github/git" {
			t.Errorf("%s source %q", r, d.Source)
		}
	}
	for _, tc := range []struct{ req, part string }{
		// Allowed by another project's rule too: the part wins.
		{"GET api.github.com/user", "rest"},
		{"POST api.github.com/repos/acme/app/issues", "rest"},
		{"GET api.github.com/graphql", "rest"},
		{"POST api.github.com/graphql", "graphql"},
		{"GET github.com/acme/app", "web"},
	} {
		d := want(t, e, req(tc.req), false, RulePartOff)
		if d.Source.Part != tc.part || d.Source.Kind != builtin.GitHub {
			t.Errorf("%s part %q, want %s", tc.req, d.Source.Part, tc.part)
		}
		if tc.part == "rest" && len(d.OffBy) != 2 {
			t.Errorf("%s off by %v, want project A and the global policy", tc.req, d.OffBy)
		}
	}
	// A built-in deny comes first, and holds whatever parts are on.
	want(t, e, req("GET github.com/acme/app/archive/main.zip"), false, builtin.RuleArchive)
	// The registry parts are separate: npm off leaves PyPI on.
	p = policy.Policy{PartsOff: []policy.PartOff{{Profile: "package-registries", Part: "npm", Source: globalSrc}}}
	e = quietEngine(t, p)
	want(t, e, req("GET registry.npmjs.org/left-pad"), false, RulePartOff)
	want(t, e, req("GET pypi.org/simple/x/"), true, RuleAllowed)
}

// TestWebSocket matches an upgrade as a GET on its path.
func TestWebSocket(t *testing.T) {
	p := policy.Policy{Endpoints: []policy.Endpoint{rest(globalSrc, "ws", "ws.example.com", []string{"GET /socket"})}}
	e := quietEngine(t, p)
	r := req("GET ws.example.com/socket")
	r.WebSocket = true
	want(t, e, r, true, RuleAllowed)
	// Another method would reach the server as itself, past rules that
	// matched a GET.
	for _, m := range []string{"CONNECT", "POST"} {
		r.Method = m
		want(t, e, r, false, RuleMethodForm)
	}
	e = quietEngine(t, broadPolicy())
	for _, s := range []string{
		"POST github.com/other/repo.git/git-receive-pack",
		"PUT registry.npmjs.org/left-pad",
	} {
		r := req(s)
		r.WebSocket = true
		want(t, e, r, false, RuleMethodForm)
	}
	r = req("GET ws.example.com/other")
	r.WebSocket = true
	want(t, e, r, false, RuleNoRule)
}

// TestDenySuffixForms is each built-in deny in the forms a server may
// route to its path: with a `/`, a `;` parameter, a `.` format suffix
// or an escape after the segment.
func TestDenySuffixForms(t *testing.T) {
	e := quietEngine(t, broadPolicy())
	for _, tc := range []struct{ req, rule string }{
		{"POST api.github.com/repos/acme/app/forks/", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/forks;x", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/forks.json", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/forks%2F", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/Forks%2fx", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/generate/", builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/generate;x", builtin.RuleCreate},
		{"POST api.github.com/user/repos.json", builtin.RuleCreate},
		{"POST api.github.com/orgs/acme/repos;x", builtin.RuleCreate},
		{"GET api.github.com/repos/acme/app/tarball;x", builtin.RuleArchive},
		{"GET api.github.com/repos/acme/app/tarball.json", builtin.RuleArchive},
		{"GET api.github.com/repos/acme/app/zipball%2Fmain", builtin.RuleArchive},
		{"GET github.com/acme/app/archive;x/main.zip", builtin.RuleArchive},
		{"GET github.com/acme/app/archive.x/main.zip", builtin.RuleArchive},
		{"GET gitlab.com/group/sub/proj/-/archive;x/main/proj.zip", builtin.RuleArchive},
		{"GET gitlab.com/api/v4/projects/group%2Fsub%2Fproj/repository/archive;x", builtin.RuleArchive},
		{"POST gitlab.com/api/v4/projects/group%2Fsub%2Fproj/fork;x", builtin.RuleCreate},
		{"POST gitlab.com/api/v4/projects.json", builtin.RuleCreate},
		{"GET codeberg.org/owner/lib/archive;x/main.zip", builtin.RuleArchive},
		{"GET codeberg.org/api/v1/repos/owner/lib/archive.x/main.zip", builtin.RuleArchive},
		{"POST codeberg.org/api/v1/repos/owner/lib/forks;x", builtin.RuleCreate},
		{"POST codeberg.org/api/v1/repos/owner/lib/generate.json", builtin.RuleCreate},
		{"POST codeberg.org/api/v1/repos/migrate/", builtin.RuleCreate},
		{"POST api.anthropic.com/api/event_logging.json", builtin.RuleEventLog},
		{"GET ghcr.io/token.json", builtin.RuleRegistryToken},
		{"GET ghcr.io/token/", builtin.RuleRegistryToken},
	} {
		want(t, e, req(tc.req), false, tc.rule)
	}
	// A repository whose name has a `.` isn't cut into a deny.
	want(t, e, req("GET github.com/acme/app.forks"), true, RuleAllowed)
}

func TestDenyStem(t *testing.T) {
	for in, out := range map[string]string{
		"/":              "/",
		"/a/forks/":      "/a/forks",
		"/a/forks;x/b":   "/a/forks/b",
		"/a/forks.json":  "/a/forks",
		"/a/forks%2fx":   "/a/forks",
		"/.well-known/x": "/.well-known/x",
		"/a/b.c;d%2f":    "/a/b",
	} {
		if got := denyStem(in); got != out {
			t.Errorf("denyStem(%q) = %q, want %q", in, got, out)
		}
	}
}
