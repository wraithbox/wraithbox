package httppolicy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
)

const (
	repoA = "https://github.com/acme/app.git"
	repoB = "https://gitlab.com/group/sub/proj.git"
	repoC = "https://codeberg.org/owner/lib"
)

// broadPolicy has a project set, and user rules that allow every
// method on every built-in host, so each built-in deny is shown to hold
// against an allow (S09-02-precedence, "Network rules merge as a
// union").
func broadPolicy() policy.Policy {
	var eps []policy.Endpoint
	for _, h := range []string{
		"github.com", "api.github.com", "codeload.github.com", "gist.github.com",
		"gitlab.com", "codeberg.org", "api.anthropic.com", "registry.npmjs.org", "ghcr.io",
	} {
		eps = append(eps, rest(projectBSrc, "all-"+h, h, []string{"* /**"}))
	}
	eps = append(eps,
		rest(globalSrc, "docs", "docs.example.com", []string{"GET /**"}, "GET /private/**"),
		audit(rest(globalSrc, "trial", "trial.example.com", []string{"GET /**"}, "GET /secret/**")),
	)
	return policy.Policy{
		Generation: 3,
		Endpoints:  eps,
		Projects:   []policy.Project{project(projectA, repoA, repoB, repoC), project(projectB)},
	}
}

// TestDecideSteps runs each step of the order of evaluation, with each
// rule name.
func TestDecideSteps(t *testing.T) {
	e := quietEngine(t, broadPolicy())
	for _, tc := range []struct {
		req   string
		allow bool
		rule  string
	}{
		// 1. The form of the method and the path.
		{"get docs.example.com/a", false, RuleMethodForm},
		{"* docs.example.com/a", false, RuleMethodForm},
		{"G@T docs.example.com/a", false, RuleMethodForm},
		{" docs.example.com/a", false, RuleMethodForm},
		{"GET docs.example.com/a/../../b", false, RulePathForm},
		{"GET docs.example.com/a%zz", false, RulePathForm},
		// 2. The built-in denies, each against a user rule that allows
		// every method and path on the host.
		{"POST api.anthropic.com/v1/%6Dessages", false, RuleModelPathEscape},
		{"POST api.anthropic.com/api/event_logging/v2/batch", false, builtin.RuleEventLog},
		{"POST api.anthropic.com/API/Event_Logging/v2/batch", false, builtin.RuleEventLog},
		{"POST api.anthropic.com/api/event_logging;x/v2/batch", false, builtin.RuleEventLog},
		{"GET github.com/acme/app/archive/refs/heads/main.zip", false, builtin.RuleArchive},
		{"GET codeload.github.com/acme/app/tar.gz/main", false, builtin.RuleArchive},
		{"GET api.github.com/repos/acme/app/tarball/main", false, builtin.RuleArchive},
		{"GET api.github.com/repos/acme/app/zipball", false, builtin.RuleArchive},
		{"GET gitlab.com/group/sub/proj/-/archive/main/proj-main.zip", false, builtin.RuleArchive},
		{"GET gitlab.com/api/v4/projects/group%2Fsub%2Fproj/repository/archive.zip", false, builtin.RuleArchive},
		{"GET codeberg.org/owner/lib/archive/main.tar.gz", false, builtin.RuleArchive},
		{"GET gist.github.com/someone/abc", false, builtin.RuleCreate},
		{"POST api.github.com/gists", false, builtin.RuleCreate},
		{"POST api.github.com/user/repos", false, builtin.RuleCreate},
		{"POST api.github.com/orgs/acme/repos", false, builtin.RuleCreate},
		{"POST api.github.com/repos/acme/app/forks", false, builtin.RuleCreate},
		{"POST gitlab.com/api/v4/projects", false, builtin.RuleCreate},
		{"POST gitlab.com/api/v4/projects/group%2Fsub%2Fproj/fork", false, builtin.RuleCreate},
		{"POST codeberg.org/api/v1/repos/owner/lib/forks", false, builtin.RuleCreate},
		{"GET ghcr.io/token?scope=repository:homebrew/core/wget:pull", false, builtin.RuleRegistryToken},
		{"PUT registry.npmjs.org/left-pad", false, RuleRegistryPublish},
		{"DELETE registry.npmjs.org/left-pad/-rev/1", false, RuleRegistryPublish},
		{"POST github.com/other/repo.git/git-receive-pack", false, RuleWriteOutside},
		{"DELETE api.github.com/repos/other/repo", false, RuleWriteOutside},
		// 4. The GraphQL path of a built-in host, in each form.
		{"POST api.github.com/graphql", false, RuleGraphQL},
		{"POST api.github.com/graphql/", false, RuleGraphQL},
		{"POST api.github.com/GraphQL", false, RuleGraphQL},
		{"POST api.github.com/graphql;x", false, RuleGraphQL},
		{"GET api.github.com/graphql", false, RuleGraphQL},
		{"POST gitlab.com/api/graphql", false, RuleGraphQL},
		{"POST gitlab.com/api/graphql.json", false, RuleGraphQL},
		// 5. A source's deny_rules.
		{"GET docs.example.com/private/x", false, RuleDenyRule},
		// 6. The allows.
		{"GET docs.example.com/public", true, RuleAllowed},
		{"GET github.com/acme/app", true, RuleAllowed},
		{"POST docs.example.com/public", false, RuleNoRule},
		{"GET other.example.com/", false, RuleNoRule},
		// Not GraphQL: a longer name.
		{"GET api.github.com/graphqlx", true, RuleAllowed},
	} {
		want(t, e, req(tc.req), tc.allow, tc.rule)
	}
}

// TestBuiltinDenyAllModes shows each built-in deny holding against a
// user rule that allows it, and audit relaxing nothing but
// http-no-rule and http-deny-rule.
func TestAuditRelaxesOnlyTwoRules(t *testing.T) {
	e := quietEngine(t, broadPolicy())
	d := want(t, e, req("DELETE trial.example.com/x"), true, RuleAuditAllowed)
	if d.Broke != RuleNoRule || d.Mode != policy.ModeAudit {
		t.Errorf("broke %q mode %v, want %s audit", d.Broke, d.Mode, RuleNoRule)
	}
	d = want(t, e, req("GET trial.example.com/secret/x"), true, RuleAuditAllowed)
	if d.Broke != RuleDenyRule {
		t.Errorf("broke %q, want %s", d.Broke, RuleDenyRule)
	}
	// The form checks hold on an audit host.
	want(t, e, req("get trial.example.com/x"), false, RuleMethodForm)
	want(t, e, req("GET trial.example.com/../x"), false, RulePathForm)
	// Another port of the host isn't in audit.
	want(t, e, onPort(req("DELETE trial.example.com/x"), 8443), false, RuleNoRule)
}

// TestAuditPerVMEnforcement is C03-policy-and-approvals, "Per-VM
// enforcement", the wb-proxyd side: an enforce endpoint on the same
// host and port in another source keeps it in enforce.
func TestAuditPerVMEnforcement(t *testing.T) {
	p := policy.Policy{Endpoints: []policy.Endpoint{
		audit(rest(projectASrc, "try", "svc.example.com", []string{"GET /**"})),
	}}
	e := quietEngine(t, p)
	d := want(t, e, req("DELETE svc.example.com/item/1"), true, RuleAuditAllowed)
	if d.Broke != RuleNoRule {
		t.Errorf("broke %q, want %s", d.Broke, RuleNoRule)
	}
	p.Endpoints = append(p.Endpoints, rest(projectBSrc, "keep", "svc.example.com", []string{"GET /**"}))
	e = quietEngine(t, p)
	want(t, e, req("DELETE svc.example.com/item/1"), false, RuleNoRule)
	want(t, e, req("GET svc.example.com/item/1"), true, RuleAllowed)
}

// TestGuardedNeverAudit builds an engine whose policy would put a
// guarded host in audit if Check let it, and sees the host held in
// enforce: the engine doesn't rely on the load check alone.
func TestGuardedNeverAudit(t *testing.T) {
	e := quietEngine(t, broadPolicy())
	// Swap in an audit endpoint on github.com after Check ran.
	ep := audit(rest(globalSrc, "sneak", "github.com", []string{"GET /**"}))
	e.policy.Endpoints = append(e.policy.Endpoints, ep)
	for _, r := range []string{
		"POST github.com/other/repo.git/git-receive-pack",
		"GET github.com/acme/app/archive/main.zip",
		"PUT github.com/acme/app",
	} {
		d := e.Decide(req(r))
		if d.Allow || d.Mode != policy.ModeEnforce {
			t.Errorf("%s = allow %v mode %v, want refused in enforce", r, d.Allow, d.Mode)
		}
	}
}

func TestNewRefusesBadPolicy(t *testing.T) {
	if _, err := New(nil, "vm1", nil); err == nil {
		t.Error("New(nil) succeeded")
	}
	p := policy.Policy{Endpoints: []policy.Endpoint{
		audit(rest(globalSrc, "x", "github.com", []string{"GET /**"})),
	}}
	if _, err := New(&p, "vm1", nil); err == nil || !strings.Contains(err.Error(), policy.RuleGuarded) {
		t.Errorf("New with audit on github.com: %v, want %s", err, policy.RuleGuarded)
	}
}

func TestLogAndBody(t *testing.T) {
	p := broadPolicy()
	p.PartsOff = []policy.PartOff{{Profile: "git-hosting", Kind: "github", Part: "rest", Source: projectASrc}}
	e, buf := engine(t, p)
	d := want(t, e, req("GET api.github.com/user"), false, RulePartOff)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line: %v: %s", err, buf)
	}
	for k, v := range map[string]any{
		"vm": "vm1", "generation": 3.0, "host": "api.github.com", "port": 443.0,
		"method": "GET", "path": "/user", "rule": RulePartOff, "mode": "enforce",
		"source": "builtin git-hosting/github/rest", "off_by": "project " + projectA,
	} {
		if rec[k] != v {
			t.Errorf("log %s = %v, want %v", k, rec[k], v)
		}
	}
	body := d.RefusalBody()
	for _, s := range []string{
		"rule: profile-part-off", "host: api.github.com:443", "method: GET", "path: /user",
		"part: builtin git-hosting/github/rest", "turned off by: project " + projectA,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
	buf.Reset()
	want(t, e, req("GET trial.example.com/secret/x"), true, RuleAuditAllowed)
	if !strings.Contains(buf.String(), `"broke":"http-deny-rule"`) || !strings.Contains(buf.String(), `"source":"global rule trial"`) {
		t.Errorf("audit log line lacks broke or source: %s", buf)
	}
	buf.Reset()
	e.Decide(Request{Method: "GET", Host: hostName("docs.example.com"), Port: 443, Path: "/a\x00b"})
	if strings.Contains(buf.String(), "a\\u0000b") || !strings.Contains(buf.String(), "control byte") {
		t.Errorf("path form log line: %s", buf)
	}
}
