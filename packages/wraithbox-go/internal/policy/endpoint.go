package policy

import (
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

func (p *Policy) checkEndpoints() *Refusal {
	for i := range p.Endpoints {
		if r := p.Endpoints[i].check(at("endpoints", i)); r != nil {
			return r
		}
	}
	return nil
}

// check checks one endpoint's fields, and that they go together.
func (e *Endpoint) check(f string) *Refusal {
	if r := checkSource(f+".source", e.Source, anySource...); r != nil {
		return r
	}
	if r := checkString(f+".rule_name", e.RuleName, MaxRuleName); r != nil {
		return r
	}
	if e.Host.Name().String() == "" {
		return refuse(RuleValue, f+".host", "is missing")
	}
	if r := checkPorts(f+".ports", e.Ports); r != nil {
		return r
	}
	if e.Path != nil && e.Path.String() == "" {
		return refuse(RuleValue, f+".path", "is set but empty")
	}
	if r := e.checkEnums(f); r != nil {
		return r
	}
	if r := e.checkForm(f); r != nil {
		return r
	}
	if r := checkRules(f+".rules", e.Rules); r != nil {
		return r
	}
	return checkRules(f+".deny_rules", e.DenyRules)
}

func checkPorts(f string, ports []uint16) *Refusal {
	if len(ports) == 0 {
		return refuse(RuleValue, f, "names no port")
	}
	if r := bound(f, len(ports), MaxPortsPerEnd); r != nil {
		return r
	}
	seen := map[uint16]bool{}
	for j, port := range ports {
		switch {
		case port == 0:
			return refuse(RuleValue, at(f, j), "is 0")
		case seen[port]:
			return refuse(RuleValue, at(f, j), "is a duplicate")
		}
		seen[port] = true
	}
	return nil
}

func (e *Endpoint) checkEnums(f string) *Refusal {
	switch {
	case e.Protocol != ProtocolL4 && e.Protocol != ProtocolREST:
		return refuse(RuleValue, f+".protocol", "is not L4 or REST")
	case e.Enforcement < EnforcementUnset || e.Enforcement > EnforcementAudit:
		return refuse(RuleValue, f+".enforcement", "is not unset, enforce or audit")
	case e.Access < AccessNone || e.Access > AccessFull:
		return refuse(RuleValue, f+".access", "is not a preset")
	}
	// Only the user sets `audit` (S07-07-hosts-and-modes, "Rules for
	// audit").
	if e.Enforcement == EnforcementAudit && e.Source.Kind != SourceGlobal && e.Source.Kind != SourceProject {
		return refuse(RuleSource, f+".enforcement", "a %s source may not set audit", e.Source.Kind)
	}
	return nil
}

// checkForm refuses the combinations OpenShell v0.1.2 refuses, and an
// L4 endpoint with a path, whose path no L4 stream could be held to. A
// field no check would apply is never accepted, so no rule is weaker
// than written (S09-01-policy-files, "Refused keys").
func (e *Endpoint) checkForm(f string) *Refusal {
	hasRules := len(e.Rules) > 0
	hasAccess := e.Access != AccessNone
	if e.Protocol == ProtocolL4 {
		if hasRules || hasAccess || len(e.DenyRules) > 0 || e.Path != nil {
			return refuse(RuleEndpointForm, f, "an L4 endpoint has no path, access, rules or deny_rules")
		}
		return nil
	}
	switch {
	case hasRules && hasAccess:
		return refuse(RuleEndpointForm, f, "rules and access are mutually exclusive")
	case !hasRules && !hasAccess:
		return refuse(RuleEndpointForm, f, "a REST endpoint needs rules or access")
	}
	return nil
}

func checkRules(f string, rules []HTTPRule) *Refusal {
	if r := bound(f, len(rules), MaxRulesPerEndpoint); r != nil {
		return r
	}
	for j, rule := range rules {
		if m, r := match.CompileMethod(string(rule.Method)); r != nil || m != rule.Method {
			return refuse(match.RuleMethod, at(f, j)+".method", "is not a method in canonical form")
		}
		if rule.Path.String() == "" {
			return refuse(RuleValue, at(f, j)+".path", "is missing")
		}
	}
	return nil
}
