package httppolicy

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// fuzzHosts are the hosts FuzzHTTPDecision picks from: covered hosts of
// each profile, a host a remote names, a host in audit, and a host with
// no rule.
var fuzzHosts = []string{
	"github.com", "api.github.com", "uploads.github.com", "codeload.github.com", "gist.github.com",
	"gitlab.com", "codeberg.org", "api.anthropic.com", "platform.claude.com",
	"registry.npmjs.org", "ghcr.io", "sum.golang.org",
	"git.example.net", "trial.example.com", "docs.example.com", "other.example.com",
}

// fuzzPolicy is broadPolicy, with a remote host in the project set and
// GitHub's REST part and npm turned off.
func fuzzPolicy() policy.Policy {
	p := broadPolicy()
	p.Projects[0].Repositories = append(p.Projects[0].Repositories, "https://git.example.net/team/tool.git")
	p.Endpoints = append(p.Endpoints, audit(rest(globalSrc, "trial-all", "trial.example.com", []string{"GET /**"}, "* /**")))
	p.PartsOff = []policy.PartOff{
		{Profile: "git-hosting", Kind: "github", Part: "rest", Source: projectASrc},
		{Profile: "package-registries", Part: "npm", Source: globalSrc},
	}
	return p
}

// FuzzHTTPDecision decides requests from guest bytes: the method, the
// path and query as received, the port and the WebSocket flag. It
// checks that Decide never panics, and that:
//
//   - a request a built-in deny matches is never allowed, in any mode;
//   - a request in a part that is off is never allowed;
//   - a write to git hosting outside the project set is never allowed
//     (SEC06-repo-writes);
//   - a publish to a registry is never allowed;
//   - `audit-allowed` names only http-no-rule or http-deny-rule as the
//     rule it broke, and only on a host in audit, never a covered host.
func FuzzHTTPDecision(f *testing.F) {
	for _, s := range []struct {
		method, path, query string
		host                uint8
		port                uint16
		ws                  bool
	}{
		{"POST", "/acme/app.git/git-receive-pack", "", 0, 443, false},
		{"GET", "/other/repo.git/info/refs", "service=git-receive-pack", 0, 443, false},
		{"GET", "/acme/app/archive/refs%2Fheads%2Fmain.zip", "", 0, 443, false},
		{"POST", "/repos/acme/app/forks", "", 1, 443, false},
		{"POST", "/GraphQL;x", "", 1, 443, false},
		{"POST", "/api/v4/projects/group%2Fsub%2Fproj/fork", "", 5, 443, false},
		{"POST", "/api/v4/projects/group%2Fsub%2Fproj%2F", "", 5, 443, false},
		{"POST", "/api/event_logging/v2/batch", "", 7, 443, false},
		{"POST", "/v1/%6Dessages", "", 7, 443, false},
		{"PUT", "/left-pad", "", 9, 443, false},
		{"GET", "/token", "scope=x", 10, 443, false},
		{"POST", "/team/tool.git/git-receive-pack", "", 12, 443, false},
		{"DELETE", "/secret/x", "", 13, 443, false},
		{"GET", "/socket", "", 14, 443, true},
		{"POST", "/other/repo.git/git-receive-pack", "", 0, 443, true},
		{"POST", "/api/v4/projects/123/repository/files/git-upload-pack", "", 5, 443, false},
		{"POST", "/api/v1/repos/victim/repo/contents/git-upload-pack", "", 12, 443, false},
		{"POST", "/repos/acme/app/forks;x", "", 1, 443, false},
		{"get", "/a/../b", "", 15, 80, false},
	} {
		f.Add(s.method, s.path, s.query, s.host, s.port, s.ws)
	}
	e := quietEngine(f, fuzzPolicy())
	f.Fuzz(func(t *testing.T, method, path, query string, hostIdx uint8, port uint16, ws bool) {
		r := Request{
			Method: method, Host: hostName(fuzzHosts[int(hostIdx)%len(fuzzHosts)]),
			Port: port, Path: path, RawQuery: query, WebSocket: ws,
		}
		d := e.Decide(r)
		_ = d.RefusalBody()
		if !d.Allow {
			return
		}
		checkAllowed(t, e, r, d)
	})
}

// checkAllowed checks an allowed decision against the properties of
// FuzzHTTPDecision.
func checkAllowed(t *testing.T, e *Engine, r Request, d Decision) {
	t.Helper()
	switch d.Rule {
	case RuleAllowed:
	case RuleAuditAllowed:
		if d.Broke != RuleNoRule && d.Broke != RuleDenyRule || d.Mode != policy.ModeAudit {
			t.Fatalf("audit allowed %+v past %s in mode %v", r, d.Broke, d.Mode)
		}
	default:
		t.Fatalf("allowed %+v with rule %s", r, d.Rule)
	}
	if _, pr := match.CanonicalPath(r.Path); pr != nil {
		t.Fatalf("allowed %+v with a path that isn't canonical", r)
	}
	if r.WebSocket && r.Method != "GET" {
		t.Fatalf("allowed WebSocket upgrade %+v that isn't a GET", r)
	}
	h := e.hosts[r.Host]
	if h == nil {
		return
	}
	if d.Rule == RuleAuditAllowed {
		t.Fatalf("audit allowed %+v on a covered host", r)
	}
	c := call{req: r, method: d.Method, path: d.Path, host: h}
	if broken := e.brokenBuiltin(c); broken != "" {
		t.Fatalf("allowed %+v past %s", r, broken)
	}
}

// brokenBuiltin returns the property of FuzzHTTPDecision a request on a
// covered host breaks, or "". Where a check is short, it is its own
// rather than the engine's.
func (e *Engine) brokenBuiltin(c call) string {
	h := c.host
	lower := strings.ToLower(c.path)
	for _, dn := range h.denies {
		if dn.Rule.Match(c.req.Host, c.method, lower) || dn.Rule.Match(c.req.Host, c.method, denyStem(lower)) {
			return "built-in deny " + dn.Name
		}
	}
	switch {
	case h.profile == builtin.ModelAPI && strings.Contains(c.req.Path, "%"):
		return "an escape on the model API"
	case h.profile == builtin.PackageRegistries && !isRead(c.method):
		return "a publish"
	case h.profile == builtin.GitHosting && oracleWrite(c) && !oracleInSet(c):
		return "a write outside the project set"
	case h.graphQLPath != "" && h.isGraphQL(c.path):
		return "the GraphQL path"
	}
	if a, ok := c.firstBuiltin(); ok && len(e.offBy(a)) > 0 {
		return "part " + a.Part + ", which is off"
	}
	return ""
}

// oracleWrite is FuzzHTTPDecision's own reading of a git hosting write
// (S07-19-request-decisions, "Writes to git hosting"), apart from the
// engine's isWrite: a query that names a `service` other than
// `git-upload-pack`, or a method other than a read, unless it is a
// `git-upload-pack` POST on a transport path or a request to the
// GraphQL path, which the GraphQL refusal covers.
func oracleWrite(c call) bool {
	for pair := range strings.FieldsFuncSeq(c.req.RawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		k, v, _ := strings.Cut(pair, "=")
		key, keyErr := url.PathUnescape(strings.ReplaceAll(k, "+", " "))
		value, valueErr := url.PathUnescape(strings.ReplaceAll(v, "+", " "))
		if keyErr != nil || strings.EqualFold(strings.TrimSpace(key), "service") && (valueErr != nil || value != "git-upload-pack") {
			return true
		}
	}
	switch c.method {
	case "GET", "HEAD", "OPTIONS":
		return false
	case "POST":
		if oracleFetch(c.req.Host.String(), c.path) {
			return false
		}
	}
	return c.host.graphQLPath == "" || !strings.HasPrefix(strings.ToLower(c.path), c.host.graphQLPath)
}

// oracleFetch reports whether a POST is git's `git-upload-pack` on a
// transport path: `/{owner}/{repo}/git-upload-pack` on GitHub and
// Codeberg, and two segments or more, the first not `api`, on GitLab
// and the remote host.
func oracleFetch(host, path string) bool {
	segments := strings.Split(path, "/")[1:]
	if slices.Contains(segments, "") || segments[len(segments)-1] != "git-upload-pack" {
		return false
	}
	switch host {
	case "github.com", "codeberg.org":
		return len(segments) == 3
	case "gitlab.com", "git.example.net":
		return len(segments) >= 3 && !strings.EqualFold(segments[0], "api")
	}
	return false
}

// oracleSets are fuzzPolicy's project set, as the paths of a write to
// each host: git's transport and the API forms. GitHub's are matched in
// lowercase.
var oracleSets = map[string]*regexp.Regexp{
	"github.com":         regexp.MustCompile(`^/acme/app(\.git)?` + oracleTransport),
	"api.github.com":     regexp.MustCompile(`^/repos/acme/app(/|$)`),
	"uploads.github.com": regexp.MustCompile(`^/repos/acme/app(/|$)`),
	"gitlab.com": regexp.MustCompile(`^(/group/sub/proj(\.git)?` + oracleTransport +
		`|/api/v4/projects/group%2Fsub%2Fproj(/|$))`),
	"codeberg.org":    regexp.MustCompile(`^(/owner/lib(\.git)?` + oracleTransport + `|/api/v1/repos/owner/lib(/|$))`),
	"git.example.net": regexp.MustCompile(`^/team/tool(\.git)?` + oracleTransport),
}

const oracleTransport = `/(info/refs|git-upload-pack|git-receive-pack)$`

// oracleInSet reports whether a write names a repository of
// fuzzPolicy's project set.
func oracleInSet(c call) bool {
	host := c.req.Host.String()
	re, ok := oracleSets[host]
	if !ok {
		return false
	}
	path := c.path
	if host == "github.com" || host == "api.github.com" || host == "uploads.github.com" {
		path = strings.ToLower(path)
	}
	return re.MatchString(path)
}
