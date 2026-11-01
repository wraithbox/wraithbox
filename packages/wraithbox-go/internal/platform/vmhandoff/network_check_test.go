package vmhandoff

import (
	"slices"
	"testing"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// goodNetwork is the packet transport record of generation 3.
func goodNetwork() *vmdv1.HandoffMessage {
	return &vmdv1.HandoffMessage{Generation: 3, Kind: vmdv1.Kind_KIND_NETWORK}
}

// networkTable has the packet transport of generation 3 expected.
func networkTable(t testing.TB) (*table, *request) {
	t.Helper()
	tb := newTable()
	r, _, ref := tb.expectNetwork(3)
	if ref != nil {
		t.Fatal(ref)
	}
	return tb, r
}

// TestCheckNetwork is the KIND_NETWORK checks of C11-host-daemons,
// "Hand-off receiver": a record of the current generation with no
// request ID, direction or port is accepted, and anything else is
// refused with handoff-network-refused (S07-02-packet-path, "Packet
// transport").
func TestCheckNetwork(t *testing.T) {
	cases := []struct {
		name        string
		change      func(*vmdv1.HandoffMessage)
		rule        Rule
		failsWaiter bool
	}{
		{"good", nil, "", false},
		{"generation 0", func(m *vmdv1.HandoffMessage) { m.Generation = 0 }, RuleNetwork, false},
		{"older generation", func(m *vmdv1.HandoffMessage) { m.Generation = 2 }, RuleNetwork, false},
		{"newer generation", func(m *vmdv1.HandoffMessage) { m.Generation = 4 }, RuleNetwork, false},
		{"request ID", func(m *vmdv1.HandoffMessage) { m.RequestId = 7 }, RuleNetwork, true},
		{"direction", func(m *vmdv1.HandoffMessage) { m.Direction = vmdv1.Direction_DIRECTION_HOST_TO_GUEST }, RuleNetwork, true},
		{"source port", func(m *vmdv1.HandoffMessage) { m.SourcePort = 49152 }, RuleNetwork, true},
		{"destination port", func(m *vmdv1.HandoffMessage) { m.DestinationPort = 1023 }, RuleNetwork, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb, want := networkTable(t)
			m := goodNetwork()
			if c.change != nil {
				c.change(m)
			}
			msg, got, ref := tb.check(marshal(t, m))
			requireNetworkOutcome(t, c.rule, msg, got, want, ref)
			if c.failsWaiter != (got != nil && ref != nil) {
				t.Errorf("request returned with the refusal = %v, want %v", got != nil, c.failsWaiter)
			}
			if stillOpen, wantOpen := tb.network != nil, c.rule != "" && !c.failsWaiter; stillOpen != wantOpen {
				t.Errorf("transport request open = %v, want %v", stillOpen, wantOpen)
			}
		})
	}
}

func requireNetworkOutcome(t *testing.T, rule Rule, msg *vmdv1.HandoffMessage, got, want *request, ref *Refusal) {
	t.Helper()
	if rule == "" {
		if ref != nil || got != want || msg.GetKind() != vmdv1.Kind_KIND_NETWORK {
			t.Fatalf("check = %v, %v, %v; want the open transport request", msg, got, ref)
		}
		return
	}
	if ref == nil || ref.Rule != rule {
		t.Fatalf("check = %v, want rule %s", ref, rule)
	}
}

// TestNetworkOncePerGeneration: a record nobody expected is refused, and
// so is a generation's second record, also when the caller tries to
// expect that generation again.
func TestNetworkOncePerGeneration(t *testing.T) {
	tb := newTable()
	tb.generation = 3
	if _, _, ref := tb.check(marshal(t, goodNetwork())); ref == nil || ref.Rule != RuleNetwork {
		t.Fatalf("a record nobody expected = %v, want %s", ref, RuleNetwork)
	}
	tb, _ = networkTable(t)
	if _, _, ref := tb.check(marshal(t, goodNetwork())); ref != nil {
		t.Fatal(ref)
	}
	if _, _, ref := tb.check(marshal(t, goodNetwork())); ref == nil || ref.Rule != RuleNetwork {
		t.Fatalf("a second record = %v, want %s", ref, RuleNetwork)
	}
	if _, _, ref := tb.expectNetwork(3); ref == nil || ref.Rule != RuleRequest {
		t.Fatalf("expecting generation 3 again = %v, want %s", ref, RuleRequest)
	}
}

// TestExpectNetwork checks the transport requests a caller may open,
// and that a newer generation closes the older one's requests of both
// kinds.
func TestExpectNetwork(t *testing.T) {
	tb, conn := openTable(t)
	for _, c := range []struct {
		name       string
		gen        uint64
		wantRefuse bool
	}{
		{"generation 0", 0, true},
		{"older generation", 2, true},
		{"current generation", 3, false},
		{"already expected", 3, true},
	} {
		_, _, ref := tb.expectNetwork(c.gen)
		if (ref != nil) != c.wantRefuse {
			t.Errorf("%s: expectNetwork = %v, want refused %v", c.name, ref, c.wantRefuse)
		}
		if ref != nil && ref.Rule != RuleRequest {
			t.Errorf("%s: rule %s, want %s", c.name, ref.Rule, RuleRequest)
		}
	}
	network := tb.network
	next, stale, ref := tb.expectNetwork(4)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(stale) != 2 || !slices.Contains(stale, conn) || !slices.Contains(stale, network) {
		t.Fatalf("stale = %v, want request 7 and the transport of generation 3", stale)
	}
	if tb.generation != 4 || len(tb.open) != 0 || tb.network != next {
		t.Fatalf("generation %d, %d open, transport %v; want 4, 0, generation 4's", tb.generation, len(tb.open), tb.network)
	}
}
