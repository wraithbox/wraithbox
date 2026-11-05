package policyfile

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The values of the endpoint keys with a fixed set, as OpenShell v0.1.2
// spells them.
var (
	protocols = map[string]policy.EndpointProtocol{"rest": policy.ProtocolREST}
	modes     = map[string]policy.Enforcement{"enforce": policy.EnforcementEnforce, "audit": policy.EnforcementAudit}
	accesses  = map[string]policy.Access{
		"read-only": policy.AccessReadOnly, "read-write": policy.AccessReadWrite, "full": policy.AccessFull,
	}
)

// enums reads `protocol`, `enforcement` and `access`.
func (d *decoder) enums(n *yaml.Node, path string, e *policy.Endpoint) bool {
	ok := d.protocol(n, path, e)
	ok = d.enforcement(n, path, e) && ok
	e.Access = policy.AccessNone
	if v := lookup(n, "access"); v != nil {
		a, aok := oneOf(d, v, child(path, "access"), accesses)
		e.Access = a
		ok = aok && ok
	}
	return ok
}

// protocol reads `protocol`. An endpoint without one is L4: it allows
// every method and path on its host and port, still inspected. Any
// protocol but `rest` is refused, OpenShell's `tcp` included: user
// policy has no GraphQL, WebSocket, JSON-RPC or MCP rules in V1 (I489,
// maintainer decision 1), and an L4 endpoint is written without one.
func (d *decoder) protocol(n *yaml.Node, path string, e *policy.Endpoint) bool {
	e.Protocol = policy.ProtocolL4
	v := lookup(n, "protocol")
	if v == nil {
		return true
	}
	s, ok := d.str(v, child(path, "protocol"))
	if !ok {
		return false
	}
	p, known := protocols[s]
	if !known {
		d.c.add(RuleProtocolUnsupported, v.Line, child(path, "protocol"), s,
			"Wraith Box reads the protocol rest, or none for L4, so this endpoint is refused rather than enforced as another")
		return false
	}
	e.Protocol = p
	return true
}

// enforcement reads `enforcement`. Repository configuration can't set
// `audit` (S09-03-project-settings, "Repository-supplied
// configuration"; SEC09-host-policy).
func (d *decoder) enforcement(n *yaml.Node, path string, e *policy.Endpoint) bool {
	v := lookup(n, "enforcement")
	if v == nil {
		return true
	}
	mode, ok := oneOf(d, v, child(path, "enforcement"), modes)
	if !ok {
		return false
	}
	if mode == policy.EnforcementAudit && d.file.Source.Kind == policy.SourceRepository {
		d.c.add(RuleAuditSource, v.Line, child(path, "enforcement"), e.Host.String(),
			"repository configuration can't put the host %s in audit: only the global and project policy can", e.Host)
		return false
	}
	e.Enforcement = mode
	return true
}

// oneOf reads a string from a fixed set.
func oneOf[T any](d *decoder, n *yaml.Node, path string, set map[string]T) (T, bool) {
	var zero T
	s, ok := d.str(n, path)
	if !ok {
		return zero, false
	}
	v, known := set[s]
	if !known {
		d.c.add(RuleValue, n.Line, path, s, "isn't one of the values OpenShell v0.1.2 defines here")
		return zero, false
	}
	return v, true
}

// httpRules reads `rules` and `deny_rules`.
func (d *decoder) httpRules(n *yaml.Node, path string, e *policy.Endpoint) bool {
	ok := true
	if v := lookup(n, "rules"); v != nil && d.sequenceNode(v, child(path, "rules")) {
		for i, r := range v.Content {
			p := index(child(path, "rules"), i)
			allow := lookup(r, "allow")
			if !d.mappingNode(r, p) || allow == nil {
				d.c.add(RuleValue, r.Line, p, "", "a rule is a mapping with the key allow")
				ok = false
				continue
			}
			rule, rok := d.httpRule(allow, child(p, "allow"))
			e.Rules = append(e.Rules, rule)
			ok = ok && rok
		}
	}
	if v := lookup(n, "deny_rules"); v != nil && d.sequenceNode(v, child(path, "deny_rules")) {
		for i, r := range v.Content {
			rule, rok := d.httpRule(r, index(child(path, "deny_rules"), i))
			e.DenyRules = append(e.DenyRules, rule)
			ok = ok && rok
		}
	}
	return ok
}

// httpRule reads a method and a path. A rule that names CONNECT is
// refused: no rule allows a tunnel (S07-16-tls-and-tunnels, "`CONNECT`
// and other tunnels").
func (d *decoder) httpRule(n *yaml.Node, path string) (policy.HTTPRule, bool) {
	var rule policy.HTTPRule
	if !d.mappingNode(n, path) {
		return rule, false
	}
	mv, pv := lookup(n, "method"), lookup(n, "path")
	if mv == nil || pv == nil {
		d.c.add(RuleValue, n.Line, path, "", "a rule names a method and a path")
		return rule, false
	}
	s, ok := d.checkedStr(mv, child(path, "method"), match.MaxMethod)
	if !ok {
		return rule, false
	}
	m, r := match.CompileMethod(s)
	if r != nil {
		d.c.add(r.Rule, mv.Line, child(path, "method"), s, "%s", strings.TrimPrefix(r.Error(), r.Rule+": "))
		return rule, false
	}
	if m == "CONNECT" {
		d.c.add(RuleTunnel, mv.Line, child(path, "method"), s, "no rule can allow or deny CONNECT: Wraith Box doesn't forward tunnels")
		return rule, false
	}
	p, ok := d.pattern(pv, child(path, "path"))
	return policy.HTTPRule{Method: m, Path: p}, ok
}
