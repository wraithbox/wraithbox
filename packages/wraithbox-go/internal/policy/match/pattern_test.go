package match

import (
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

func mustPattern(t testing.TB, s string) Pattern {
	t.Helper()
	p, r := Compile(s)
	if r != nil {
		t.Fatalf("Compile(%q): %v", s, r)
	}
	return p
}

func mustHost(t testing.TB, s string) Host {
	t.Helper()
	h, r := CompileHost(s)
	if r != nil {
		t.Fatalf("CompileHost(%q): %v", s, r)
	}
	return h
}

func mustName(t testing.TB, s string) dnsname.Name {
	t.Helper()
	n, r := dnsname.FromHost(s)
	if r != nil {
		t.Fatalf("FromHost(%q): %v", s, r)
	}
	return n
}

// TestPatternMatch covers the glob semantics of OpenShell v0.1.2's
// path_matches (`crates/openshell-supervisor-network/data/sandbox-policy.rego`,
// commit 6648bd0c): `**` alone matches everything, otherwise
// glob.match with "/" as the separator, so `*` stays within a segment
// and `**` crosses them. OpenShell has no Rego test file for it at that
// commit, so the cases are this package's own.
func TestPatternMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"**", "/", true},
		{"**", "/a/b/c", true},
		{"/**", "/", true},
		{"/**", "/a/b/c", true},
		{"/api/**", "/api/", true},
		{"/api/**", "/api/v1/x", true},
		// gobwas glob: `/api/**` needs the slash after api.
		{"/api/**", "/api", false},
		{"/api/*", "/api/x", true},
		{"/api/*", "/api/", true},
		{"/api/*", "/api/x/y", false},
		{"/repos/*/*/pulls", "/repos/o/r/pulls", true},
		{"/repos/*/*/pulls", "/repos/o/r/x/pulls", false},
		{"/a*b", "/ab", true},
		{"/a*b", "/a-to-b", true},
		{"/a*b", "/ax/b", false},
		{"/a**b", "/ax/b", true},
		// A trailing slash is part of the path.
		{"/graphql", "/graphql/", false},
		{"/graphql/", "/graphql", false},
		{"/graphql", "/graphql", true},
		// `%2F` is data inside its segment (S07-08, "Encoded slashes"):
		// `*` matches across it, a literal `/` doesn't.
		{"/*", "/@scope%2Fname", true},
		{"/@scope%2fname", "/@scope%2Fname", true},
		{"/api/v4/projects/*", "/api/v4/projects/group%2Fproject", true},
		{"/v2/homebrew/core/**", "/v2/homebrew%2Fcore/x", false},
		// Patterns are put in canonical form (S07-08 over OpenShell, where
		// such a pattern matches no canonical path).
		{"/%67raphql", "/graphql", true},
		{"/users/%40*", "/users/@alice", true},
		{"/a/caf%c3%a9", "/a/caf%C3%A9", true},
		// An escaped star is a literal star.
		{"/%2A", "/*", true},
		{"/%2A", "/x", false},
	} {
		p := mustPattern(t, tc.pattern)
		if got := p.Match(tc.path); got != tc.want {
			t.Errorf("Compile(%q).Match(%q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
	if (Pattern{}).Match("/") || (Pattern{}).CanMatchPrefix("/") {
		t.Error("the zero Pattern matches")
	}
}

func TestCompileRefused(t *testing.T) {
	for _, src := range []string{
		"",
		"*",
		"api/**",
		"/a?b",
		"/a#b",
		"/a[bc]",
		"/{a,b}",
		"/a\\b",
		"/a b",
		"/a\x00",
		"/caf\xc3\xa9",
		"/a%zz",
		"/a%2*",
		"/a%00",
		"/a%5C",
		"/a//b",
		"/a/./b",
		"/a/../b",
		"/a/%2e%2e/b",
		"/a/..;/b",
		"/" + strings.Repeat("a", MaxPattern),
	} {
		if _, r := Compile(src); r == nil || r.Rule != RulePathPattern {
			t.Errorf("Compile(%q) = %v, want a %s refusal", src, r, RulePathPattern)
		}
	}
}

// TestCompileRefusedOffset checks that a refusal names the offset of the
// escape in the pattern, not in its segment (NFR06-explained-refusals).
func TestCompileRefusedOffset(t *testing.T) {
	for _, tc := range []struct {
		src    string
		offset string
	}{
		{"/api/v4/a%zz", "offset 9"},
		{"/api/v4/a*b%zz", "offset 11"},
		{"/api/**/x%00", "offset 9"},
	} {
		_, r := Compile(tc.src)
		if r == nil || !strings.Contains(r.Error(), tc.offset) {
			t.Errorf("Compile(%q) = %v, want it to name %s", tc.src, r, tc.offset)
		}
	}
}

func TestMethod(t *testing.T) {
	for _, tc := range []struct {
		rule, req string
		want      bool
	}{
		{"*", "DELETE", true},
		{"*", "x", true},
		{"POST", "POST", true},
		{"post", "POST", true},
		{"POST", "post", true},
		{"POST", "PUT", false},
		// RFC 9110, section 9.3.2, as OpenShell's method_matches.
		{"GET", "HEAD", true},
		{"get", "head", true},
		{"HEAD", "GET", false},
	} {
		m, r := CompileMethod(tc.rule)
		if r != nil {
			t.Fatalf("CompileMethod(%q): %v", tc.rule, r)
		}
		if got := m.Match(tc.req); got != tc.want {
			t.Errorf("Method(%q).Match(%q) = %v, want %v", tc.rule, tc.req, got, tc.want)
		}
	}
	for _, s := range []string{"", "G ET", "GET\r\n", "GÉT", "**", strings.Repeat("A", MaxMethod+1)} {
		if _, r := CompileMethod(s); r == nil || r.Rule != RuleMethod {
			t.Errorf("CompileMethod(%q) = %v, want a %s refusal", s, r, RuleMethod)
		}
	}
	if Method("").Match("GET") {
		t.Error("the zero Method matches")
	}
}

func TestHost(t *testing.T) {
	for _, tc := range []struct {
		pattern, host string
		want          bool
	}{
		{"api.github.com", "api.github.com", true},
		{"API.GitHub.com.", "api.github.com", true},
		{"api.github.com", "github.com", false},
		// OpenShell's glob with "." as the separator: `*.` is one label,
		// never none and never two.
		{"*.github.com", "api.github.com", true},
		{"*.GitHub.com", "api.github.com", true},
		{"*.github.com", "github.com", false},
		{"*.github.com", "a.b.github.com", false},
		{"*.anthropic.com", "api.anthropic.com", true},
		{"*.github.com", "evil-github.com", false},
	} {
		h := mustHost(t, tc.pattern)
		if got := h.Match(mustName(t, tc.host)); got != tc.want {
			t.Errorf("CompileHost(%q).Match(%q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
	if h := mustHost(t, "*.Example.COM."); h.String() != "*.example.com" || !h.Wildcard() || h.Name().String() != "example.com" {
		t.Errorf("CompileHost(*.Example.COM.) = %q", h)
	}
	if (Host{}).Match(mustName(t, "example.com")) {
		t.Error("the zero Host matches")
	}
}

// TestHostRefused checks the name form rule on host patterns
// (S07-03-dns, "Name form" and "IP literals"). The rule's own tests are
// in internal/dnsname. A wildcard host is checked after its `*.`.
func TestHostRefused(t *testing.T) {
	for _, tc := range []struct{ host, rule string }{
		{"under_score.example", dnsname.RuleForm},
		{"caf\xc3\xa9.example", dnsname.RuleForm},
		{"example.123", dnsname.RuleForm},
		{"1.2.3.4", dnsname.RuleForm},
		{"::1", dnsname.RuleForm},
		{"[::1]", dnsname.RuleForm},
		{"2001:db8::1", dnsname.RuleForm},
		{"", dnsname.RuleForm},
		{"*.under_score.example", dnsname.RuleForm},
		{"*.example.123", dnsname.RuleForm},
		{"*.1.2.3", dnsname.RuleForm},
		{"*.", dnsname.RuleForm},
		{"*", RuleHostPattern},
		{"**", RuleHostPattern},
		{"**.example.com", RuleHostPattern},
		{"api.*.example.com", RuleHostPattern},
		{"a*.example.com", RuleHostPattern},
		{"*.*.example.com", RuleHostPattern},
		{"*.com", RuleHostPattern},
	} {
		if _, r := CompileHost(tc.host); r == nil || r.Rule != tc.rule {
			t.Errorf("CompileHost(%q) = %v, want a %s refusal", tc.host, r, tc.rule)
		}
	}
}

func TestRuleMatch(t *testing.T) {
	ep := mustPattern(t, "/api/**")
	r := Rule{
		Host:         mustHost(t, "*.example.com"),
		EndpointPath: &ep,
		Method:       "GET",
		Path:         mustPattern(t, "/**"),
	}
	for _, tc := range []struct {
		host, method, path string
		want               bool
	}{
		{"api.example.com", "GET", "/api/x", true},
		{"api.example.com", "HEAD", "/api/x", true},
		{"api.example.com", "POST", "/api/x", false},
		{"api.example.com", "GET", "/other", false},
		{"example.com", "GET", "/api/x", false},
	} {
		if got := r.Match(mustName(t, tc.host), tc.method, tc.path); got != tc.want {
			t.Errorf("Match(%s %s %s) = %v, want %v", tc.host, tc.method, tc.path, got, tc.want)
		}
	}
}

// TestCanMatchGraphQLLock covers the forms of the GraphQL lock
// (S09-02-precedence, "Network rules merge as a union";
// C03-policy-and-approvals, "Profile parts"): a rule on api.github.com
// with each of these paths and any method can reach `/graphql`.
func TestCanMatchGraphQLLock(t *testing.T) {
	host := mustName(t, "api.github.com")
	for _, tc := range []struct {
		host, method, path string
		want               bool
	}{
		{"api.github.com", "*", "/graphql", true},
		{"api.github.com", "*", "/graphql/", true},
		{"api.github.com", "*", "/graph*", true},
		{"api.github.com", "*", "/%67raphql", true},
		{"api.github.com", "*", "/**", true},
		{"api.github.com", "*", "**", true},
		{"api.github.com", "POST", "/graphql", true},
		{"api.github.com", "post", "/graphql", true},
		{"*.github.com", "POST", "/graphql", true},
		{"api.github.com", "POST", "/*", true},
		{"api.github.com", "POST", "/*/", true},
		{"api.github.com", "POST", "/graph**", true},
		// `/graphql/x` starts with `/graphql`. The answer is about the
		// prefix, so the caller's prefix decides how wide the check is.
		{"api.github.com", "POST", "/*/x", true},
		// These can't reach it.
		{"api.github.com", "POST", "/repos/**", false},
		{"api.github.com", "POST", "/graph", false},
		{"api.github.com", "POST", "/x/*", false},
		{"github.com", "POST", "/graphql", false},
		{"*.example.com", "POST", "/graphql", false},
	} {
		r := Rule{
			Host:   mustHost(t, tc.host),
			Method: Method(strings.ToUpper(tc.method)),
			Path:   mustPattern(t, tc.path),
		}
		if got := CanMatch(r, host, []string{"GET", "POST"}, "/graphql"); got != tc.want {
			t.Errorf("CanMatch(%s %s %s, /graphql) = %v, want %v", tc.host, tc.method, tc.path, got, tc.want)
		}
	}
}

// TestCanMatchTokenPath covers the forms of `model-token-path`
// (S09-02-precedence, "The OAuth token paths"): a rule that can let a
// canonical POST reach a path that starts with `/v1/oauth/token` on a
// model API host. Access presets and endpoints without rules are
// expanded by the policy package and tested there. The port is not in
// a Rule, so any port counts.
func TestCanMatchTokenPath(t *testing.T) {
	const prefix = "/v1/oauth/token"
	for _, tc := range []struct {
		ruleHost, host, method, path string
		want                         bool
	}{
		{"api.anthropic.com", "api.anthropic.com", "POST", "/v1/oauth/token", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/token", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/token/revoke", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/token/", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/tok*", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/*", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/**", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/token*", true},
		{"platform.claude.com", "platform.claude.com", "*", "/v1/oauth/token", true},
		{"platform.claude.com", "platform.claude.com", "post", "/v1/oauth/token", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/%74oken", true},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/%6Fauth/token", true},
		{"*.claude.com", "platform.claude.com", "POST", "/v1/oauth/token", true},
		{"*.anthropic.com", "api.anthropic.com", "POST", "/**", true},
		{"PLATFORM.claude.com.", "platform.claude.com", "POST", "/v1/oauth/token", true},
		// These still load (S09-02-precedence).
		{"platform.claude.com", "platform.claude.com", "GET", "/v1/oauth/token", false},
		{"platform.claude.com", "platform.claude.com", "POST", "/v1/oauth/hello", false},
		{"claude.ai", "platform.claude.com", "POST", "/v1/oauth/token", false},
		{"claude.ai", "api.anthropic.com", "POST", "/v1/oauth/token", false},
		// `%2F` is data. The model API hosts refuse any percent-escape
		// at request time (S07-11-http-policy, `model-path-escape`).
		{"platform.claude.com", "platform.claude.com", "POST", "/v1%2Foauth%2Ftoken", false},
	} {
		r := Rule{
			Host:   mustHost(t, tc.ruleHost),
			Method: Method(strings.ToUpper(tc.method)),
			Path:   mustPattern(t, tc.path),
		}
		if got := CanMatch(r, mustName(t, tc.host), []string{"POST"}, prefix); got != tc.want {
			t.Errorf("CanMatch(%s %s %s on %s) = %v, want %v", tc.ruleHost, tc.method, tc.path, tc.host, got, tc.want)
		}
	}
}

func TestCanMatchEndpointPath(t *testing.T) {
	host := mustName(t, "api.github.com")
	for _, tc := range []struct {
		endpoint, path string
		want           bool
	}{
		{"/**", "/graphql", true},
		{"/graphql", "/**", true},
		{"/repos/**", "/**", false},
		{"/**", "/repos/**", false},
	} {
		ep := mustPattern(t, tc.endpoint)
		r := Rule{Host: ExactHost(host), EndpointPath: &ep, Method: AnyMethod, Path: mustPattern(t, tc.path)}
		if got := CanMatch(r, host, []string{"POST"}, "/graphql"); got != tc.want {
			t.Errorf("CanMatch(endpoint %s, rule %s) = %v, want %v", tc.endpoint, tc.path, got, tc.want)
		}
	}
}

// TestMatchLinear checks that a pattern with many stars against a long
// path that almost matches finishes, so a policy can't make a request
// cost exponential time (SEC13-bounded-resources).
func TestMatchLinear(t *testing.T) {
	p := mustPattern(t, "/"+strings.Repeat("a*", 200)+"b")
	path := "/" + strings.Repeat("a", MaxPath-1)
	if p.Match(path) {
		t.Fatal("matched a path without b")
	}
	if !p.CanMatchPrefix(path) {
		t.Fatal("a path of a can be continued to match")
	}
}
