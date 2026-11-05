package policy

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

const (
	projectA = "0123456789abcdef0123456789abcdef"
	projectB = "fedcba9876543210fedcba9876543210"
)

func global() *policyv1.Source {
	return &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_GLOBAL}
}

// valid returns a message that FromProto accepts, with one of each
// list.
func valid() *policyv1.EffectivePolicy {
	return &policyv1.EffectivePolicy{
		Generation: 7,
		Endpoints: []*policyv1.Endpoint{
			{
				Source:   &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_BUILTIN},
				RuleName: "github",
				Host:     "api.github.com",
				Ports:    []uint32{443},
				Protocol: policyv1.Protocol_PROTOCOL_REST,
				Rules:    []*policyv1.HttpRule{{Method: "GET", Path: "/repos/**"}},
				DenyRules: []*policyv1.HttpRule{
					{Method: "POST", Path: "/user/repos"},
				},
			},
			{
				Source:   &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_REPOSITORY, ProjectId: projectA, Repository: "https://github.com/o/r"},
				RuleName: "docs",
				Host:     "*.example.org",
				Ports:    []uint32{443, 80},
				Path:     "/docs/**",
				Protocol: policyv1.Protocol_PROTOCOL_REST,
				Access:   policyv1.Access_ACCESS_READ_ONLY,
			},
			{
				Source:      &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_PROJECT, ProjectId: projectA},
				RuleName:    "trial",
				Host:        "beta.example.net",
				Ports:       []uint32{8443},
				Protocol:    policyv1.Protocol_PROTOCOL_L4,
				Enforcement: policyv1.Enforcement_ENFORCEMENT_AUDIT,
			},
		},
		PassHosts:      []*policyv1.PassHost{{Host: "pinned.example.com", Source: global(), Reason: "certificate pinning"}},
		RawTcp:         []*policyv1.RawTcpEntry{{Host: "db.example.com", Port: 5432, Source: global(), Reason: "postgres"}},
		WildcardBudget: 100,
		PartsOff: []*policyv1.PartOff{
			{Profile: "git-hosting", Kind: "github", Part: "graphql", Source: global()},
			{Profile: "model-api", Part: "model", Source: &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_PROJECT, ProjectId: projectB}},
		},
		Projects: []*policyv1.Project{
			{Id: projectA, Repositories: []string{"https://github.com/o/r", "sha256:00"}},
			{Id: projectB},
		},
	}
}

func TestFromProtoValid(t *testing.T) {
	p, r := FromProto(context.Background(), valid())
	if r != nil {
		t.Fatalf("FromProto: %v", r)
	}
	if p.Generation != 7 || len(p.Endpoints) != 3 || p.WildcardBudget != 100 {
		t.Errorf("FromProto = %+v", p)
	}
	again, r := FromProto(context.Background(), ToProto(p))
	if r != nil {
		t.Fatalf("FromProto(ToProto): %v", r)
	}
	if !proto.Equal(ToProto(again), valid()) {
		t.Errorf("round trip differs:\n%v\n%v", ToProto(again), valid())
	}
}

// TestFromProtoRefused changes one field of a valid message per case,
// past a bound or against an invariant, and checks FromProto refuses
// the message with the rule (fail closed).
func TestFromProtoRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule string
		edit func(m *policyv1.EffectivePolicy)
	}{
		// Bounds, one per bound in policy.proto.
		{"endpoints", RuleBound, func(m *policyv1.EffectivePolicy) { m.Endpoints = past(m.Endpoints, MaxEndpoints) }},
		{"rules", RuleBound, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules = rules(MaxRulesPerEndpoint + 1) }},
		{"deny_rules", RuleBound, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].DenyRules = rules(MaxRulesPerEndpoint + 1) }},
		{"rules in all", RuleBound, func(m *policyv1.EffectivePolicy) {
			m.Endpoints[0].Rules = rules(MaxRulesPerEndpoint)
			m.Endpoints = past(m.Endpoints[:1], MaxRules/MaxRulesPerEndpoint)
		}},
		{"ports", RuleBound, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Ports = ports(MaxPortsPerEnd + 1) }},
		{"pass_hosts", RuleBound, func(m *policyv1.EffectivePolicy) { m.PassHosts = past(m.PassHosts, MaxPassHosts) }},
		{"raw_tcp", RuleBound, func(m *policyv1.EffectivePolicy) { m.RawTcp = past(m.RawTcp, MaxRawTCP) }},
		{"parts_off", RuleBound, func(m *policyv1.EffectivePolicy) { m.PartsOff = past(m.PartsOff, MaxPartsOff) }},
		{"projects", RuleBound, func(m *policyv1.EffectivePolicy) { m.Projects = past(m.Projects, MaxProjects) }},
		{"repositories", RuleBound, func(m *policyv1.EffectivePolicy) {
			m.Projects[0].Repositories = past(m.Projects[0].Repositories, MaxRepositories)
		}},
		{"wildcard_budget", RuleBound, func(m *policyv1.EffectivePolicy) { m.WildcardBudget = MaxWildcardBudget + 1 }},
		{"rule_name long", RuleString, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].RuleName = long(MaxRuleName + 1) }},
		{"rule_name empty", RuleString, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].RuleName = "" }},
		{"rule_name control", RuleString, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].RuleName = "a\nb" }},
		{"rule_name non-ASCII", RuleString, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].RuleName = "caf\xc3\xa9" }},
		{"pass reason long", RuleString, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Reason = long(MaxReason + 1) }},
		{"pass reason empty", RuleString, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Reason = "" }},
		{"raw reason long", RuleString, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Reason = long(MaxReason + 1) }},
		{"repository long", RuleString, func(m *policyv1.EffectivePolicy) { m.Projects[0].Repositories[0] = long(MaxRepository + 1) }},
		{"source repository long", RuleString, func(m *policyv1.EffectivePolicy) {
			m.Endpoints[1].Source.Repository = long(MaxRepository + 1)
		}},
		{"part long", RuleString, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Part = long(MaxPartString + 1) }},
		{"profile empty", RuleString, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Profile = "" }},
		{"kind control", RuleString, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Kind = "git\x00hub" }},
		{"method long", match.RuleMethod, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules[0].Method = long(match.MaxMethod + 1) }},
		{"method form", match.RuleMethod, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules[0].Method = "G*T" }},
		{"rule path long", match.RulePathPattern, func(m *policyv1.EffectivePolicy) {
			m.Endpoints[0].Rules[0].Path = "/" + long(match.MaxPattern)
		}},
		{"rule path empty", match.RulePathPattern, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules[0].Path = "" }},
		{"deny path form", match.RulePathPattern, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].DenyRules[0].Path = "/a\tb" }},
		{"endpoint path form", match.RulePathPattern, func(m *policyv1.EffectivePolicy) { m.Endpoints[1].Path = "docs" }},
		{"host form", dnsname.RuleForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Host = "a_b.example.com" }},
		{"host IP literal", dnsname.RuleForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Host = "10.0.0.1" }},
		{"host empty", dnsname.RuleForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Host = "" }},
		{"host pattern", match.RuleHostPattern, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Host = "**.github.com" }},
		{"host not canonical", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Host = "API.github.com" }},
		{"pass host not canonical", RuleValue, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Host = "pinned.example.com." }},
		{"pass host wildcard", dnsname.RuleForm, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Host = "*.example.com" }},
		{"raw host form", dnsname.RuleForm, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Host = "db.example.123" }},
		{"port 0", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Ports = []uint32{0} }},
		{"port 65536", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Ports = []uint32{65536} }},
		{"no port", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Ports = nil }},
		{"port twice", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Ports = []uint32{443, 443} }},
		{"raw port 0", RuleValue, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Port = 0 }},
		{"raw port 65536", RuleValue, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Port = 65536 }},
		{"project id", RuleValue, func(m *policyv1.EffectivePolicy) { m.Projects[0].Id = strings.ToUpper(projectA) }},
		{"project twice", RuleValue, func(m *policyv1.EffectivePolicy) { m.Projects[1].Id = projectA }},
		{"source project id", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Source.ProjectId = "abc" }},
		// Enums.
		{"protocol unspecified", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Protocol = 0 }},
		{"protocol unknown", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Protocol = 9 }},
		{"enforcement unknown", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Enforcement = 9 }},
		{"access unknown", RuleValue, func(m *policyv1.EffectivePolicy) { m.Endpoints[1].Access = 9 }},
		{"source unspecified", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Source.Kind = 0 }},
		{"source missing", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Source = nil }},
		{"source unknown", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Source.Kind = 9 }},
		// Sources.
		{"project source without project", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Source.ProjectId = "" }},
		{"global source with project", RuleSource, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Source.ProjectId = projectA }},
		{"repository source without repository", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[1].Source.Repository = "" }},
		{"project source with repository", RuleSource, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Source.Repository = "https://x.example/r" }},
		{"pass from repository", RuleSource, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Source = m.Endpoints[1].Source }},
		{"pass from approval", RuleSource, func(m *policyv1.EffectivePolicy) {
			m.PassHosts[0].Source = &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_APPROVAL}
		}},
		{"raw from builtin", RuleSource, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Source = m.Endpoints[0].Source }},
		{"part off from repository", RuleSource, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Source = m.Endpoints[1].Source }},
		{"audit from repository", RuleSource, func(m *policyv1.EffectivePolicy) {
			m.Endpoints[1].Enforcement = policyv1.Enforcement_ENFORCEMENT_AUDIT
		}},
		{"audit from approval", RuleSource, func(m *policyv1.EffectivePolicy) {
			m.Endpoints[2].Source = &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_APPROVAL, ProjectId: projectA}
		}},
		// Endpoint form, as OpenShell v0.1.2.
		{"rules and access", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Access = policyv1.Access_ACCESS_FULL }},
		{"REST without rules or access", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules = nil }},
		{"deny rules alone", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[0].Rules = nil }},
		{"L4 with access", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Access = policyv1.Access_ACCESS_FULL }},
		{"L4 with rules", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Rules = rules(1) }},
		{"L4 with deny rules", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].DenyRules = rules(1) }},
		{"L4 with path", RuleEndpointForm, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Path = "/x" }},
		// Guarded hosts (S07-07-hosts-and-modes).
		{"pass on a covered host", RuleGuarded, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Host = "registry.npmjs.org" }},
		{"pass on a covered-only host", RuleGuarded, func(m *policyv1.EffectivePolicy) { m.PassHosts[0].Host = "ssh.github.com" }},
		{"pass on a project's host", RuleGuarded, func(m *policyv1.EffectivePolicy) {
			m.Projects[1].Repositories = []string{"https://git.example.com:8443/o/r"}
			m.PassHosts[0].Host = "git.example.com"
		}},
		{"raw TCP on a covered host", RuleGuarded, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Host = "altssh.gitlab.com" }},
		{"audit on a covered host", RuleGuarded, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Host = "api.anthropic.com" }},
		{"audit wildcard over a covered host", RuleGuarded, func(m *policyv1.EffectivePolicy) { m.Endpoints[2].Host = "*.crates.io" }},
		{"audit on a project's host", RuleGuarded, func(m *policyv1.EffectivePolicy) {
			m.Projects[0].Repositories = append(m.Projects[0].Repositories, "https://beta.example.net/o/r")
		}},
		// One protocol per host and port (S07-15-non-http-streams).
		{"raw TCP and an endpoint", RuleProtocolConflict, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Host, m.RawTcp[0].Port = "beta.example.net", 8443 }},
		{"raw TCP under a wildcard endpoint", RuleProtocolConflict, func(m *policyv1.EffectivePolicy) { m.RawTcp[0].Host, m.RawTcp[0].Port = "x.example.org", 80 }},
		// Parts.
		{"unknown part", RulePart, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Part = "wiki" }},
		{"part of another kind", RulePart, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Kind = "gitea"; m.PartsOff[0].Part = "graphql" }},
		{"part without its kind", RulePart, func(m *policyv1.EffectivePolicy) { m.PartsOff[0].Kind = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.edit(m)
			p, r := FromProto(context.Background(), m)
			if r == nil || p != nil {
				t.Fatalf("FromProto accepted it")
			}
			if r.Rule != tc.rule {
				t.Errorf("rule %q, want %q: %v", r.Rule, tc.rule, r)
			}
		})
	}
}

func long(n int) string { return strings.Repeat("a", n) }

func ports(n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = uint32(i + 1)
	}
	return out
}

func rules(n int) []*policyv1.HttpRule {
	out := make([]*policyv1.HttpRule, n)
	for i := range out {
		out[i] = &policyv1.HttpRule{Method: "GET", Path: "/x"}
	}
	return out
}

// past repeats the last entry of a list until it has one more than
// maxN.
func past[T any](list []T, maxN int) []T {
	for len(list) <= maxN {
		list = append(list, list[len(list)-1])
	}
	return list
}

func TestFromProtoNil(t *testing.T) {
	if _, r := FromProto(context.Background(), nil); r == nil || r.Rule != RuleValue {
		t.Errorf("FromProto(nil) = %v", r)
	}
	var nilRefusal *Refusal
	if nilRefusal.Error() != "" {
		t.Error("a nil Refusal has a message")
	}
}

// TestCheckZeroValues checks that a Policy built in Go with fields left
// at their zero value is refused, not taken as a match-nothing rule.
func TestCheckZeroValues(t *testing.T) {
	p, r := FromProto(context.Background(), valid())
	if r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		name string
		edit func(p *Policy)
	}{
		{"endpoint host", func(p *Policy) { p.Endpoints[0].Host = match.Host{} }},
		{"rule path", func(p *Policy) { p.Endpoints[0].Rules[0].Path = match.Pattern{} }},
		{"rule method", func(p *Policy) { p.Endpoints[0].Rules[0].Method = "" }},
		{"rule method case", func(p *Policy) { p.Endpoints[0].Rules[0].Method = "get" }},
		{"endpoint path", func(p *Policy) { p.Endpoints[1].Path = &match.Pattern{} }},
		{"pass host", func(p *Policy) { p.PassHosts[0].Host = dnsname.Name{} }},
		{"raw host", func(p *Policy) { p.RawTCP[0].Host = dnsname.Name{} }},
		{"raw port", func(p *Policy) { p.RawTCP[0].Port = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := FromProto(context.Background(), ToProto(p))
			tc.edit(q)
			if r := q.Check(); r == nil {
				t.Errorf("Check accepted it")
			}
		})
	}
}

// FuzzFromProto feeds arbitrary bytes, as a daemon reads them, to
// FromProto. It checks nothing panics, and that a policy it accepts
// survives ToProto and FromProto unchanged.
func FuzzFromProto(f *testing.F) {
	seed, err := proto.Marshal(valid())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		var m policyv1.EffectivePolicy
		if proto.Unmarshal(b, &m) != nil {
			return
		}
		p, r := fromProto(&m)
		if r == nil {
			r = p.Check()
		}
		if r != nil {
			return
		}
		out := ToProto(p)
		again, r := fromProto(out)
		if r == nil {
			r = again.Check()
		}
		if r != nil || !proto.Equal(ToProto(again), out) {
			t.Fatalf("an accepted policy doesn't survive a round trip: %v", r)
		}
	})
}
