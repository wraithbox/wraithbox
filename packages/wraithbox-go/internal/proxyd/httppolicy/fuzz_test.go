package httppolicy

import (
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
		{"POST", "/api/event_logging/v2/batch", "", 7, 443, false},
		{"POST", "/v1/%6Dessages", "", 7, 443, false},
		{"PUT", "/left-pad", "", 9, 443, false},
		{"GET", "/token", "scope=x", 10, 443, false},
		{"POST", "/team/tool.git/git-receive-pack", "", 12, 443, false},
		{"DELETE", "/secret/x", "", 13, 443, false},
		{"GET", "/socket", "", 14, 443, true},
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
	h := e.hosts[r.Host]
	if h == nil {
		return
	}
	if d.Rule == RuleAuditAllowed {
		t.Fatalf("audit allowed %+v on a covered host", r)
	}
	method := d.Method
	if r.WebSocket {
		method = "GET"
	}
	c := call{req: r, method: method, path: d.Path, host: h}
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
		if dn.Rule.Match(c.req.Host, c.method, lower) {
			return "built-in deny " + dn.Name
		}
	}
	switch {
	case h.profile == builtin.ModelAPI && strings.Contains(c.req.Path, "%"):
		return "an escape on the model API"
	case h.profile == builtin.PackageRegistries && !isRead(c.method):
		return "a publish"
	case h.profile == builtin.GitHosting && h.isWrite(c.method, c.path, c.req.RawQuery) && !h.inProjectSet(c.path):
		return "a write outside the project set"
	case h.graphQLPath != "" && h.isGraphQL(c.path):
		return "the GraphQL path"
	}
	if a, ok := c.firstBuiltin(); ok && len(e.offBy(a)) > 0 {
		return "part " + a.Part + ", which is off"
	}
	return ""
}
