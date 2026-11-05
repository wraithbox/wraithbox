package handoff

import (
	"net/netip"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// endpoint is an L4 endpoint of the global policy.
func endpoint(host string, ports ...uint32) *policyv1.Endpoint {
	return &policyv1.Endpoint{
		Source:      &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_GLOBAL},
		RuleName:    "r",
		Host:        host,
		Ports:       ports,
		Protocol:    policyv1.Protocol_PROTOCOL_L4,
		Enforcement: policyv1.Enforcement_ENFORCEMENT_ENFORCE,
	}
}

// policyProto is an effective policy with a wildcard budget and
// endpoints.
func policyProto(budget uint32, eps ...*policyv1.Endpoint) *policyv1.EffectivePolicy {
	return &policyv1.EffectivePolicy{Generation: 1, WildcardBudget: budget, Endpoints: eps}
}

func mustPolicy(t testing.TB, m *policyv1.EffectivePolicy) *policy.Policy {
	t.Helper()
	p, r := policy.FromProto(t.Context(), m)
	if r != nil {
		t.Fatal(r)
	}
	return p
}

func mustName(t testing.TB, s string) dnsname.Name {
	t.Helper()
	n, r := dnsname.FromHost(s)
	if r != nil {
		t.Fatal(r)
	}
	return n
}

// synth returns the address i above the start of the synthetic range.
func synth(i uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{198, 18, byte(i >> 8), byte(i)})
}

// canonical returns the names as a ProxydStart set holds them.
func canonical(t testing.TB, hosts ...string) []dnsname.Name {
	t.Helper()
	out := make([]dnsname.Name, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, mustName(t, h))
	}
	return out
}

// wire returns synth(i) as the field value of a message.
func wire(i uint32) uint32 { return 198<<24 | 18<<16 | i }
