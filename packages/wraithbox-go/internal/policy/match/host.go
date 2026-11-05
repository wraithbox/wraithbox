package match

import (
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

// Host is a compiled host pattern: an exact host, or `*.` and a host
// (S07-03-dns, "Name form" and "IP literals"). The zero Host matches
// nothing.
type Host struct {
	name     dnsname.Name
	wildcard bool
}

// CompileHost checks a host pattern and puts it in canonical form,
// lowercase and without a trailing dot (S07-06-name-mapping).
//
// An exact host, and the part after `*.` of a wildcard host, must pass
// dnsname.FromHost, so no policy host is an IP literal; the refusal's
// rule is then dnsname.RuleForm. OpenShell v0.1.2 also accepts `**.` as
// a first label, and `*` as a middle label (validate_host_wildcard in
// `crates/openshell-supervisor-network/src/l7/mod.rs`). Wraith Box
// refuses both with RuleHostPattern. So does a `*` anywhere else, `*.`
// before a single label, which OpenShell refuses too, and `*` alone.
func CompileHost(s string) (Host, *Refusal) {
	rest, wildcard := strings.CutPrefix(s, "*.")
	if strings.Contains(rest, "*") || s == "*" {
		return Host{}, refuse(RuleHostPattern,
			"a host pattern is an exact host or *. followed by a host, with no other *")
	}
	n, r := dnsname.FromHost(rest)
	if r != nil {
		return Host{}, &Refusal{Rule: r.Rule, msg: strings.TrimPrefix(r.Error(), r.Rule+": ")}
	}
	if wildcard && !strings.Contains(n.String(), ".") {
		return Host{}, refuse(RuleHostPattern,
			"a wildcard host needs at least two labels after *., such as *.example.com")
	}
	return Host{name: n, wildcard: wildcard}, nil
}

// ExactHost returns the Host that matches one name.
func ExactHost(n dnsname.Name) Host { return Host{name: n} }

// Wildcard reports whether the pattern starts with `*.`.
func (h Host) Wildcard() bool { return h.wildcard }

// Name returns the host of an exact pattern, and the part after `*.`
// of a wildcard one.
func (h Host) Name() dnsname.Name { return h.name }

// String returns the pattern in canonical form, such as
// "*.example.com".
func (h Host) String() string {
	if h.wildcard {
		return "*." + h.name.String()
	}
	return h.name.String()
}

// Match reports whether the pattern matches a name. Names are in
// canonical form, so they compare as strings. `*.` matches exactly one
// label, never none and never two: OpenShell matches a host pattern with
// glob.match and "." as the separator (endpoint_matches_request in
// `sandbox-policy.rego`), so its `*` doesn't cross a dot.
func (h Host) Match(n dnsname.Name) bool {
	if h.name.String() == "" {
		return false
	}
	if !h.wildcard {
		return n == h.name
	}
	label, ok := strings.CutSuffix(n.String(), "."+h.name.String())
	return ok && label != "" && !strings.Contains(label, ".")
}
