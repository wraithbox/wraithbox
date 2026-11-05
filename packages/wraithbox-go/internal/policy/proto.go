package policy

import (
	"context"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// FromProto checks an EffectivePolicy and returns it as a Policy. It
// refuses a message past a bound in policy.proto, or one that breaks an
// invariant (Check), and never repairs one. Each decision is logged
// with its rule. The message is untrusted input: the counts are
// checked before any list is read.
func FromProto(ctx context.Context, m *policyv1.EffectivePolicy) (*Policy, *Refusal) {
	p, r := fromProto(m)
	if r == nil {
		r = p.Check()
	}
	if r != nil {
		slog.Default().LogAttrs(ctx, slog.LevelError, "effective policy refused",
			slog.String("rule", r.Rule), slog.String("decision", "refuse"),
			slog.String("field", r.Field), slog.String("reason", r.msg))
		return nil, r
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "effective policy accepted",
		slog.String("decision", "accept"), slog.Uint64("generation", p.Generation),
		slog.Int("endpoints", len(p.Endpoints)), slog.Int("pass_hosts", len(p.PassHosts)),
		slog.Int("raw_tcp", len(p.RawTCP)), slog.Int("parts_off", len(p.PartsOff)),
		slog.Int("projects", len(p.Projects)))
	return p, nil
}

func fromProto(m *policyv1.EffectivePolicy) (*Policy, *Refusal) {
	if m == nil {
		return nil, refuse(RuleValue, "", "the message is missing")
	}
	if r := checkProtoCounts(m); r != nil {
		return nil, r
	}
	p := &Policy{Generation: m.GetGeneration(), WildcardBudget: m.GetWildcardBudget()}
	for i, e := range m.GetEndpoints() {
		ep, r := endpointFromProto(at("endpoints", i), e)
		if r != nil {
			return nil, r
		}
		p.Endpoints = append(p.Endpoints, ep)
	}
	if r := p.listsFromProto(m); r != nil {
		return nil, r
	}
	return p, nil
}

// checkProtoCounts checks the bounds of every list before FromProto
// reads one, so a message past a bound costs no more work.
func checkProtoCounts(m *policyv1.EffectivePolicy) *Refusal {
	total := 0
	for i, e := range m.GetEndpoints() {
		f := at("endpoints", i)
		for _, r := range []*Refusal{
			bound(f+".rules", len(e.GetRules()), MaxRulesPerEndpoint),
			bound(f+".deny_rules", len(e.GetDenyRules()), MaxRulesPerEndpoint),
			bound(f+".ports", len(e.GetPorts()), MaxPortsPerEnd),
		} {
			if r != nil {
				return r
			}
		}
		total += len(e.GetRules()) + len(e.GetDenyRules())
	}
	for i, pr := range m.GetProjects() {
		if r := bound(at("projects", i)+".repositories", len(pr.GetRepositories()), MaxRepositories); r != nil {
			return r
		}
	}
	shell := Policy{
		Endpoints: make([]Endpoint, len(m.GetEndpoints())),
		PassHosts: make([]PassHost, len(m.GetPassHosts())),
		RawTCP:    make([]RawTCP, len(m.GetRawTcp())),
		PartsOff:  make([]PartOff, len(m.GetPartsOff())),
		Projects:  make([]Project, len(m.GetProjects())),
	}
	if r := bound("endpoints[*].rules and deny_rules", total, MaxRules); r != nil {
		return r
	}
	shell.WildcardBudget = m.GetWildcardBudget()
	return shell.checkCounts()
}

func (p *Policy) listsFromProto(m *policyv1.EffectivePolicy) *Refusal {
	for i, h := range m.GetPassHosts() {
		n, r := exactHostFromProto(at("pass_hosts", i)+".host", h.GetHost())
		if r != nil {
			return r
		}
		p.PassHosts = append(p.PassHosts, PassHost{Host: n, Source: sourceFromProto(h.GetSource()), Reason: h.GetReason()})
	}
	for i, e := range m.GetRawTcp() {
		f := at("raw_tcp", i)
		n, r := exactHostFromProto(f+".host", e.GetHost())
		if r != nil {
			return r
		}
		port, r := portFromProto(f+".port", e.GetPort())
		if r != nil {
			return r
		}
		p.RawTCP = append(p.RawTCP, RawTCP{Host: n, Port: port, Source: sourceFromProto(e.GetSource()), Reason: e.GetReason()})
	}
	for _, po := range m.GetPartsOff() {
		p.PartsOff = append(p.PartsOff, PartOff{Profile: po.GetProfile(), Kind: po.GetKind(), Part: po.GetPart(), Source: sourceFromProto(po.GetSource())})
	}
	for _, pr := range m.GetProjects() {
		p.Projects = append(p.Projects, Project{ID: pr.GetId(), Repositories: append([]string(nil), pr.GetRepositories()...)})
	}
	return nil
}

// sourceFromProto copies a source. Check refuses a kind out of range,
// so it is copied as it is.
func sourceFromProto(s *policyv1.Source) Source {
	return Source{Kind: SourceKind(s.GetKind()), Project: s.GetProjectId(), Repository: s.GetRepository()}
}

func portFromProto(f string, port uint32) (uint16, *Refusal) {
	if port == 0 || port > 65535 {
		return 0, refuse(RuleValue, f, "is %d, want 1 to 65535", port)
	}
	return uint16(port), nil
}

// exactHostFromProto checks an exact host, which must already be in
// canonical form.
func exactHostFromProto(f, s string) (dnsname.Name, *Refusal) {
	n, r := dnsname.FromHost(s)
	if r != nil {
		return dnsname.Name{}, &Refusal{Rule: r.Rule, Field: f, msg: strings.TrimPrefix(r.Error(), r.Rule+": ")}
	}
	if n.String() != s {
		return dnsname.Name{}, refuse(RuleValue, f, "is not in canonical form")
	}
	return n, nil
}

func endpointFromProto(f string, m *policyv1.Endpoint) (Endpoint, *Refusal) {
	h, mr := match.CompileHost(m.GetHost())
	if mr != nil {
		return Endpoint{}, fromMatch(f+".host", mr)
	}
	if h.String() != m.GetHost() {
		return Endpoint{}, refuse(RuleValue, f+".host", "is not in canonical form")
	}
	e := Endpoint{
		Source:      sourceFromProto(m.GetSource()),
		RuleName:    m.GetRuleName(),
		Host:        h,
		Protocol:    EndpointProtocol(m.GetProtocol()),
		Enforcement: Enforcement(m.GetEnforcement()),
		Access:      Access(m.GetAccess()),
	}
	for j, port := range m.GetPorts() {
		p, r := portFromProto(at(f+".ports", j), port)
		if r != nil {
			return Endpoint{}, r
		}
		e.Ports = append(e.Ports, p)
	}
	if m.GetPath() != "" {
		pat, mr := match.Compile(m.GetPath())
		if mr != nil {
			return Endpoint{}, fromMatch(f+".path", mr)
		}
		e.Path = &pat
	}
	var r *Refusal
	if e.Rules, r = rulesFromProto(f+".rules", m.GetRules()); r != nil {
		return Endpoint{}, r
	}
	if e.DenyRules, r = rulesFromProto(f+".deny_rules", m.GetDenyRules()); r != nil {
		return Endpoint{}, r
	}
	return e, nil
}

func rulesFromProto(f string, rules []*policyv1.HttpRule) ([]HTTPRule, *Refusal) {
	var out []HTTPRule
	for j, rule := range rules {
		m, mr := match.CompileMethod(rule.GetMethod())
		if mr != nil {
			return nil, fromMatch(at(f, j)+".method", mr)
		}
		pat, mr := match.Compile(rule.GetPath())
		if mr != nil {
			return nil, fromMatch(at(f, j)+".path", mr)
		}
		out = append(out, HTTPRule{Method: m, Path: pat})
	}
	return out, nil
}

// ToProto returns the policy as an EffectivePolicy. Patterns go out as
// they were written, so FromProto of the result gives the same policy.
func ToProto(p *Policy) *policyv1.EffectivePolicy {
	m := &policyv1.EffectivePolicy{Generation: p.Generation, WildcardBudget: p.WildcardBudget}
	for i := range p.Endpoints {
		m.Endpoints = append(m.Endpoints, endpointToProto(&p.Endpoints[i]))
	}
	for _, h := range p.PassHosts {
		m.PassHosts = append(m.PassHosts, &policyv1.PassHost{Host: h.Host.String(), Source: sourceToProto(h.Source), Reason: h.Reason})
	}
	for _, e := range p.RawTCP {
		m.RawTcp = append(m.RawTcp, &policyv1.RawTcpEntry{Host: e.Host.String(), Port: uint32(e.Port), Source: sourceToProto(e.Source), Reason: e.Reason})
	}
	for _, po := range p.PartsOff {
		m.PartsOff = append(m.PartsOff, &policyv1.PartOff{Profile: po.Profile, Kind: po.Kind, Part: po.Part, Source: sourceToProto(po.Source)})
	}
	for _, pr := range p.Projects {
		m.Projects = append(m.Projects, &policyv1.Project{Id: pr.ID, Repositories: append([]string(nil), pr.Repositories...)})
	}
	return m
}

func sourceToProto(s Source) *policyv1.Source {
	return &policyv1.Source{Kind: policyv1.SourceKind(s.Kind), ProjectId: s.Project, Repository: s.Repository}
}

func endpointToProto(e *Endpoint) *policyv1.Endpoint {
	m := &policyv1.Endpoint{
		Source:      sourceToProto(e.Source),
		RuleName:    e.RuleName,
		Host:        e.Host.String(),
		Protocol:    policyv1.Protocol(e.Protocol),
		Enforcement: policyv1.Enforcement(e.Enforcement),
		Access:      policyv1.Access(e.Access),
		Rules:       rulesToProto(e.Rules),
		DenyRules:   rulesToProto(e.DenyRules),
	}
	for _, port := range e.Ports {
		m.Ports = append(m.Ports, uint32(port))
	}
	if e.Path != nil {
		m.Path = e.Path.String()
	}
	return m
}

func rulesToProto(rules []HTTPRule) []*policyv1.HttpRule {
	var out []*policyv1.HttpRule
	for _, r := range rules {
		out = append(out, &policyv1.HttpRule{Method: string(r.Method), Path: r.Path.String()})
	}
	return out
}
