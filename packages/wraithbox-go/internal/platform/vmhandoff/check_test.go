package vmhandoff

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// good is the record for request 7 of generation 3, to port 1023.
func good() *vmdv1.HandoffMessage {
	return &vmdv1.HandoffMessage{
		RequestId:       7,
		Generation:      3,
		Direction:       vmdv1.Direction_DIRECTION_HOST_TO_GUEST,
		SourcePort:      49152,
		DestinationPort: 1023,
	}
}

func marshal(t testing.TB, m *vmdv1.HandoffMessage) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// openTable has request 7 of generation 3 open, for port 1023.
func openTable(t testing.TB) (*table, *request) {
	t.Helper()
	tb := newTable()
	r, _, ref := tb.expect(7, 3, 1023)
	if ref != nil {
		t.Fatal(ref)
	}
	return tb, r
}

// TestCheck is the message checks of C11-host-daemons, "Hand-off
// receiver", without a socket: each refusal has its rule, and a record
// that names its request closes it with the refusal.
func TestCheck(t *testing.T) {
	unknownField := protowire.AppendVarint(protowire.AppendTag(marshal(t, good()), 9, protowire.VarintType), 1)
	cases := []struct {
		name        string
		record      func(*vmdv1.HandoffMessage) []byte
		rule        Rule
		failsWaiter bool
	}{
		{"good", nil, "", false},
		{"not protobuf", func(*vmdv1.HandoffMessage) []byte { return []byte{0xff, 0xff, 0xff} }, RuleMalformed, false},
		{"unknown field", func(*vmdv1.HandoffMessage) []byte { return unknownField }, RuleMalformed, false},
		{"empty", func(*vmdv1.HandoffMessage) []byte { return nil }, RuleGeneration, false},
		{"generation 0", func(m *vmdv1.HandoffMessage) []byte { m.Generation = 0; return marshal(t, m) }, RuleGeneration, false},
		{"older generation", func(m *vmdv1.HandoffMessage) []byte { m.Generation = 2; return marshal(t, m) }, RuleGeneration, false},
		{"newer generation", func(m *vmdv1.HandoffMessage) []byte { m.Generation = 4; return marshal(t, m) }, RuleGeneration, false},
		{"request ID 0", func(m *vmdv1.HandoffMessage) []byte { m.RequestId = 0; return marshal(t, m) }, RuleUnexpected, false},
		{"request not open", func(m *vmdv1.HandoffMessage) []byte { m.RequestId = 8; return marshal(t, m) }, RuleUnexpected, false},
		{"no direction", func(m *vmdv1.HandoffMessage) []byte { m.Direction = 0; return marshal(t, m) }, RuleDirection, true},
		{"unknown direction", func(m *vmdv1.HandoffMessage) []byte { m.Direction = 2; return marshal(t, m) }, RuleDirection, true},
		{"other port", func(m *vmdv1.HandoffMessage) []byte { m.DestinationPort = 1024; return marshal(t, m) }, RuleUnexpected, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb, want := openTable(t)
			record := marshal(t, good())
			if c.record != nil {
				record = c.record(good())
			}
			msg, got, ref := tb.check(record)
			checkOutcome(t, c.rule, msg, got, want, ref)
			if c.failsWaiter != (got != nil && ref != nil) {
				t.Errorf("request returned with the refusal = %v, want %v", got != nil, c.failsWaiter)
			}
			stillOpen := tb.open[7] != nil
			if wantOpen := c.rule != "" && !c.failsWaiter; stillOpen != wantOpen {
				t.Errorf("request 7 open = %v, want %v", stillOpen, wantOpen)
			}
		})
	}
}

// checkOutcome: with no rule, the record was accepted for the open
// request. Otherwise it was refused with rule.
func checkOutcome(t *testing.T, rule Rule, msg *vmdv1.HandoffMessage, got, want *request, ref *Refusal) {
	t.Helper()
	if rule == "" {
		if ref != nil || got != want || msg.GetRequestId() != 7 {
			t.Fatalf("check = %v, %v, %v; want the open request", msg, got, ref)
		}
		return
	}
	if ref == nil || ref.Rule != rule {
		t.Fatalf("check = %v, want rule %s", ref, rule)
	}
}

// TestExpect checks the requests a caller may open, and that a newer
// generation closes the older one's requests.
func TestExpect(t *testing.T) {
	tb, old := openTable(t)
	for _, c := range []struct {
		name       string
		id, gen    uint64
		wantRefuse bool
	}{
		{"request ID 0", 0, 3, true},
		{"generation 0", 8, 0, true},
		{"older generation", 8, 2, true},
		{"open request ID", 7, 3, true},
		{"next request", 8, 3, false},
	} {
		_, _, ref := tb.expect(c.id, c.gen, 1023)
		if (ref != nil) != c.wantRefuse {
			t.Errorf("%s: expect = %v, want refused %v", c.name, ref, c.wantRefuse)
		}
		if ref != nil && ref.Rule != RuleRequest {
			t.Errorf("%s: rule %s, want %s", c.name, ref.Rule, RuleRequest)
		}
	}
	_, stale, ref := tb.expect(1, 4, 1023)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(stale) != 2 || (stale[0] != old && stale[1] != old) {
		t.Fatalf("stale = %v, want requests 7 and 8 of generation 3", stale)
	}
	if tb.generation != 4 || len(tb.open) != 1 {
		t.Fatalf("generation %d with %d open, want 4 with 1", tb.generation, len(tb.open))
	}
	if _, _, ref := tb.check(marshal(t, good())); ref == nil || ref.Rule != RuleGeneration {
		t.Fatalf("a record of generation 3 after 4 = %v, want %s", ref, RuleGeneration)
	}
}
