package policy

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

func name(t *testing.T, s string) dnsname.Name {
	t.Helper()
	n, r := dnsname.FromHost(s)
	if r != nil {
		t.Fatal(r)
	}
	return n
}

func host(t *testing.T, s string) match.Host {
	t.Helper()
	h, r := match.CompileHost(s)
	if r != nil {
		t.Fatal(r)
	}
	return h
}

func pattern(t *testing.T, s string) match.Pattern {
	t.Helper()
	p, r := match.Compile(s)
	if r != nil {
		t.Fatal(r)
	}
	return p
}

func l4(t *testing.T, h string, e Enforcement, ports ...uint16) Endpoint {
	t.Helper()
	return Endpoint{
		Source: Source{Kind: SourceGlobal}, RuleName: "r", Host: host(t, h),
		Ports: ports, Protocol: ProtocolL4, Enforcement: e,
	}
}

func TestMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints []Endpoint
		host      string
		port      uint16
		want      HostMode
	}{
		{"no endpoint", nil, "a.example.com", 443, ModeNone},
		{"another host", []Endpoint{l4(t, "b.example.com", EnforcementAudit, 443)}, "a.example.com", 443, ModeNone},
		{"another port", []Endpoint{l4(t, "a.example.com", EnforcementAudit, 443)}, "a.example.com", 8443, ModeNone},
		{"audit alone", []Endpoint{l4(t, "a.example.com", EnforcementAudit, 443)}, "a.example.com", 443, ModeAudit},
		{"enforce alone", []Endpoint{l4(t, "a.example.com", EnforcementEnforce, 443)}, "a.example.com", 443, ModeEnforce},
		{"no enforcement field", []Endpoint{l4(t, "a.example.com", EnforcementUnset, 443)}, "a.example.com", 443, ModeEnforce},
		{"no field over audit", []Endpoint{
			l4(t, "a.example.com", EnforcementAudit, 443),
			l4(t, "a.example.com", EnforcementUnset, 443),
		}, "a.example.com", 443, ModeEnforce},
		{"two audits", []Endpoint{
			l4(t, "a.example.com", EnforcementAudit, 443),
			l4(t, "*.example.com", EnforcementAudit, 443),
		}, "a.example.com", 443, ModeAudit},
		{"wildcard enforce over exact audit", []Endpoint{
			l4(t, "a.example.com", EnforcementAudit, 443),
			l4(t, "*.example.com", EnforcementEnforce, 443),
		}, "a.example.com", 443, ModeEnforce},
		{"exact enforce over wildcard audit", []Endpoint{
			l4(t, "*.example.com", EnforcementAudit, 443),
			l4(t, "a.example.com", EnforcementEnforce, 443),
		}, "a.example.com", 443, ModeEnforce},
		{"wildcard doesn't reach two labels", []Endpoint{
			l4(t, "*.example.com", EnforcementEnforce, 443),
		}, "a.b.example.com", 443, ModeNone},
		{"two ports, audit on one", []Endpoint{
			l4(t, "a.example.com", EnforcementAudit, 8443),
			l4(t, "a.example.com", EnforcementEnforce, 443),
		}, "a.example.com", 8443, ModeAudit},
		{"two ports, enforce on the other", []Endpoint{
			l4(t, "a.example.com", EnforcementAudit, 8443),
			l4(t, "a.example.com", EnforcementEnforce, 443),
		}, "a.example.com", 443, ModeEnforce},
	} {
		if got := Mode(tc.endpoints, name(t, tc.host), tc.port); got != tc.want {
			t.Errorf("%s: Mode = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestProtocol covers S07-15-non-http-streams, "One protocol per host
// and port".
func TestProtocol(t *testing.T) {
	p := &Policy{
		Endpoints: []Endpoint{
			l4(t, "a.example.com", EnforcementUnset, 443, 80),
			l4(t, "*.example.org", EnforcementUnset, 8443),
		},
		RawTCP: []RawTCP{{Host: name(t, "a.example.com"), Port: 22, Source: Source{Kind: SourceGlobal}, Reason: "ssh"}},
	}
	if r := p.Check(); r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		host string
		port uint16
		want StreamProtocol
	}{
		{"a.example.com", 443, StreamTLS},
		{"a.example.com", 80, StreamPlainHTTP},
		{"a.example.com", 22, StreamRawTCP},
		{"a.example.com", 8443, StreamNone},
		{"b.example.org", 8443, StreamTLS},
		{"b.example.org", 80, StreamNone},
		{"b.example.com", 443, StreamNone},
	} {
		if got := p.Protocol(name(t, tc.host), tc.port); got != tc.want {
			t.Errorf("Protocol(%s, %d) = %s, want %s", tc.host, tc.port, got, tc.want)
		}
	}
	// A raw TCP entry on a port an endpoint names gives the port two
	// protocols, which Check refuses.
	p.RawTCP[0].Port = 443
	if r := p.Check(); r == nil || r.Rule != RuleProtocolConflict {
		t.Errorf("Check = %v, want %s", r, RuleProtocolConflict)
	}
}

// TestInspectsTLS checks which host and port get a leaf: a TLS port of
// a host that isn't a pass host, and no pass host, plain HTTP or raw TCP
// port, unnamed port, or nil policy.
func TestInspectsTLS(t *testing.T) {
	p := &Policy{
		Endpoints: []Endpoint{
			l4(t, "a.example.com", EnforcementUnset, 443, 80),
			l4(t, "pass.example.com", EnforcementUnset, 443),
		},
		PassHosts: []PassHost{{Host: name(t, "pass.example.com"), Source: Source{Kind: SourceGlobal}, Reason: "pinned"}},
		RawTCP:    []RawTCP{{Host: name(t, "a.example.com"), Port: 22, Source: Source{Kind: SourceGlobal}, Reason: "ssh"}},
	}
	if r := p.Check(); r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		host string
		port uint16
		want bool
	}{
		{"a.example.com", 443, true},
		{"a.example.com", 80, false},
		{"a.example.com", 22, false},
		{"a.example.com", 8443, false},
		{"pass.example.com", 443, false},
		{"b.example.com", 443, false},
	} {
		if got := p.InspectsTLS(name(t, tc.host), tc.port); got != tc.want {
			t.Errorf("InspectsTLS(%s, %d) = %v, want %v", tc.host, tc.port, got, tc.want)
		}
	}
	var none *Policy
	if none.InspectsTLS(name(t, "a.example.com"), 443) {
		t.Error("a nil policy inspects a host")
	}
}

// TestAllowsTokenPath checks the expansion of access presets and L4
// endpoints against the model-token-path check of S09-02-precedence:
// a rule that can allow a POST under /v1/oauth/token on a model API
// host counts, and GET or another path doesn't.
func TestAllowsTokenPath(t *testing.T) {
	rest := func(h string, a Access, rules ...HTTPRule) Endpoint {
		return Endpoint{
			Source: Source{Kind: SourceGlobal}, RuleName: "r", Host: host(t, h),
			Ports: []uint16{443}, Protocol: ProtocolREST, Access: a, Rules: rules,
		}
	}
	for _, tc := range []struct {
		name string
		e    Endpoint
		want bool
	}{
		{"read-only", rest("api.anthropic.com", AccessReadOnly), false},
		{"read-write", rest("api.anthropic.com", AccessReadWrite), true},
		{"full", rest("api.anthropic.com", AccessFull), true},
		{"L4", l4(t, "platform.claude.com", EnforcementUnset, 443), true},
		{"wildcard host", rest("*.anthropic.com", AccessReadWrite), true},
		{"GET only", rest("api.anthropic.com", AccessNone, HTTPRule{"GET", pattern(t, "/v1/oauth/token")}), false},
		{"POST hello", rest("platform.claude.com", AccessNone, HTTPRule{"POST", pattern(t, "/v1/oauth/hello")}), false},
		{"lowercase post", rest("api.anthropic.com", AccessNone, HTTPRule{"POST", pattern(t, "/v1/oauth/%74oken/**")}), true},
		{"any method", rest("api.anthropic.com", AccessNone, HTTPRule{"*", pattern(t, "/v1/**")}), true},
		// A * stays in one segment, so /v1/* can't reach /v1/oauth/token.
		{"one segment", rest("api.anthropic.com", AccessNone, HTTPRule{"*", pattern(t, "/v1/*")}), false},
		{"endpoint path elsewhere", func() Endpoint {
			e := rest("api.anthropic.com", AccessFull)
			p := pattern(t, "/v1/messages")
			e.Path = &p
			return e
		}(), false},
		{"claude.ai is no model API host", rest("claude.ai", AccessFull), false},
	} {
		got := false
		for _, e := range builtin.Entries() {
			if e.Profile != builtin.ModelAPI {
				continue
			}
			for _, r := range tc.e.Allows() {
				got = got || match.CanMatch(r, e.Host, []string{"post"}, "/v1/oauth/token")
			}
		}
		if got != tc.want {
			t.Errorf("%s: can allow POST /v1/oauth/token = %v, want %v", tc.name, got, tc.want)
		}
	}
}
