package main

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// TestPolicies checks the policy holder: before the first policy it
// allows no name and no port, and after it the name classes and ports
// are the policy's (SEC05-default-deny).
func TestPolicies(t *testing.T) {
	var h policies
	api := nt.Name("api.example.com")
	if c, _ := h.Class(api); c != netd.NameNotAllowed || h.PortAllowed(api, 443) || h.WildcardBudget() != 0 {
		t.Fatalf("before a policy: class %v, port %v, budget %d", c, h.PortAllowed(api, 443), h.WildcardBudget())
	}
	wild, ref := match.CompileHost("*.wild.example.com")
	if ref != nil {
		t.Fatal(ref)
	}
	h.ApplyPolicy(&policy.Policy{
		Generation: 2, WildcardBudget: 9,
		Endpoints: []policy.Endpoint{
			{Host: match.ExactHost(api), Ports: []uint16{443}},
			{Host: wild, Ports: []uint16{443}},
		},
	})
	for _, c := range []struct {
		name string
		want netd.NameClass
	}{
		{"api.example.com", netd.NameExact},
		{"a.wild.example.com", netd.NameWildcard},
		{"other.example.com", netd.NameNotAllowed},
	} {
		if got, from := h.Class(nt.Name(c.name)); got != c.want || from != "effective-policy-2" {
			t.Errorf("Class(%s) = %v, %s; want %v", c.name, got, from, c.want)
		}
	}
	if !h.PortAllowed(api, 443) || h.PortAllowed(api, 80) || h.WildcardBudget() != 9 {
		t.Fatalf("ports 443 %v, 80 %v, budget %d", h.PortAllowed(api, 443), h.PortAllowed(api, 80), h.WildcardBudget())
	}
}
