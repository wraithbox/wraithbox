package match

import (
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

// RuleMethod refuses a method in policy that is neither `*` nor an HTTP
// token.
const RuleMethod = "policy-method"

// MaxMethod is the longest method Method accepts, in bytes
// (S09-01-policy-files, "Strings").
const MaxMethod = 32

// AnyMethod is the method that matches every method.
const AnyMethod = "*"

// Method is a method in policy: AnyMethod, or an HTTP token in upper
// case. The zero Method matches nothing.
type Method string

// CompileMethod checks a method in policy and puts it in upper case.
// It refuses, with RuleMethod, anything but `*` or 1 to MaxMethod bytes
// of RFC 9110 token characters other than `*`, so a method like `G*T`
// can't look like a wildcard it isn't.
func CompileMethod(s string) (Method, *Refusal) {
	if s == AnyMethod {
		return AnyMethod, nil
	}
	if s == "" || len(s) > MaxMethod {
		return "", refuse(RuleMethod, "a method is * or 1 to "+strconv.Itoa(MaxMethod)+" token characters")
	}
	for i := range len(s) {
		if !isTokenChar(s[i]) || s[i] == '*' {
			return "", refuse(RuleMethod, "the method has a byte that isn't a token character, or a * that isn't the whole method, at offset "+strconv.Itoa(i))
		}
	}
	return Method(strings.ToUpper(s)), nil
}

// isTokenChar reports whether a byte is an RFC 9110 tchar.
func isTokenChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// Match reports whether the policy method matches a request's method,
// as OpenShell's method_matches does: `*` matches every method, others
// compare without regard to case, and a GET rule also matches HEAD
// (RFC 9110, section 9.3.2).
func (m Method) Match(method string) bool {
	switch {
	case m == "":
		return false
	case m == AnyMethod, strings.EqualFold(string(m), method):
		return true
	}
	return m == "GET" && strings.EqualFold(method, "HEAD")
}

// Rule is one allow or deny rule as a request meets it: the host
// pattern and the endpoint-level path of its endpoint, and the method
// and path of the rule itself. The port is the endpoint's business.
type Rule struct {
	Host Host
	// EndpointPath, when set, is the endpoint-level `path`, which a
	// request must match too (endpoint_path_matches_request in
	// OpenShell's `sandbox-policy.rego`).
	EndpointPath *Pattern
	Method       Method
	Path         Pattern
}

// Match reports whether a request matches the rule. path must be in
// canonical form (CanonicalPath).
func (r Rule) Match(host dnsname.Name, method, path string) bool {
	return r.Host.Match(host) && r.Method.Match(method) && r.Path.Match(path) &&
		(r.EndpointPath == nil || r.EndpointPath.Match(path))
}

// CanMatch reports whether the rule can match any request on host with
// one of the methods and a canonical path that starts with pathPrefix,
// on any port. It decides on the patterns (Pattern.CanMatchPrefix). The
// rule's path and its endpoint's path are each intersected with the
// prefix, but not with each other: a rule whose two paths can each
// reach the prefix counts as a match, which fails closed.
//
// The load checks of S09-02-precedence ask it: the GraphQL lock with a
// built-in host's GraphQL path, and `model-token-path` with `POST` and
// `/v1/oauth/token` on each model API host. Methods compare as Method.Match
// does, so without regard to case, and `*` in the rule matches every
// one. host is in canonical form, so a wildcard rule host counts on it.
// An access preset, or an endpoint without method and path rules, is
// expanded into its rules by the caller before it asks.
func CanMatch(r Rule, host dnsname.Name, methods []string, pathPrefix string) bool {
	if !r.Host.Match(host) {
		return false
	}
	methodOK := false
	for _, m := range methods {
		methodOK = methodOK || r.Method.Match(m)
	}
	if !methodOK {
		return false
	}
	return r.Path.CanMatchPrefix(pathPrefix) &&
		(r.EndpointPath == nil || r.EndpointPath.CanMatchPrefix(pathPrefix))
}
