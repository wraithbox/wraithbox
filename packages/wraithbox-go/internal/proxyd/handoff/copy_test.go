package handoff

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
)

// TestDecideReport runs the checks of S07-06-name-mapping, "Name
// reports", in order on one copy: each refusal names its check and
// leaves the copy unchanged.
func TestDecideReport(t *testing.T) {
	p := mustPolicy(t, policyProto(2,
		endpoint("exact.example.com", 443),
		endpoint("*.wild.example.com", 443),
		endpoint("both.wild.example.com", 443),
	))
	m := newMapping()
	for _, tc := range []struct {
		name   string
		host   string
		addr   uint32
		action reportAction
		check  egressmsg.Check
		held   int
		count  int
	}{
		{"exact name", "exact.example.com", 1, reportAdd, "", 1, 0},
		{"name the policy doesn't allow", "other.example.net", 2, reportRefuse, CheckNotAllowed, 1, 0},
		{"first wildcard name", "a.wild.example.com", 3, reportAddWildcard, "", 2, 1},
		{"a counted name at a new address", "a.wild.example.com", 4, reportAdd, "", 3, 1},
		{"an exact entry under a wildcard isn't counted", "both.wild.example.com", 5, reportAdd, "", 4, 1},
		{"second wildcard name", "b.wild.example.com", 6, reportAddWildcard, "", 5, 2},
		{"past the budget", "c.wild.example.com", 7, reportRefuse, CheckWildcardBudget, 5, 2},
		{"repeat", "exact.example.com", 1, reportRepeat, "", 5, 2},
		{"conflict", "a.wild.example.com", 1, reportConflict, CheckAddressConflict, 4, 2},
		{"not allowed comes before the conflict", "other.example.net", 3, reportRefuse, CheckNotAllowed, 4, 2},
		{"after a conflict the address is free", "exact.example.com", 1, reportAdd, "", 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := mustName(t, tc.host)
			a, check := m.decideReport(p, name, synth(tc.addr))
			if a != tc.action || check != tc.check {
				t.Fatalf("decideReport = %d %q, want %d %q", a, check, tc.action, tc.check)
			}
			m.apply(a, name, synth(tc.addr))
			if len(m.byAddr) != tc.held || len(m.wildcard) != tc.count {
				t.Fatalf("copy holds %d entries and counts %d names, want %d and %d",
					len(m.byAddr), len(m.wildcard), tc.held, tc.count)
			}
		})
	}
}

// TestDecideReportNoPolicy: before the first PolicyChange the copy
// takes no name (SEC05-default-deny).
func TestDecideReportNoPolicy(t *testing.T) {
	m := newMapping()
	if a, check := m.decideReport(nil, mustName(t, "a.example.com"), synth(1)); a != reportRefuse || check != CheckNotAllowed {
		t.Fatalf("decideReport with no policy = %d %q", a, check)
	}
}

// TestReplayUnchanged replays every entry of a copy, and sees the copy
// and the wildcard count unchanged (S07-06-name-mapping, "Replay").
func TestReplayUnchanged(t *testing.T) {
	p := mustPolicy(t, policyProto(1, endpoint("*.wild.example.com", 443), endpoint("exact.example.com", 443)))
	m := newMapping()
	for i, h := range []string{"exact.example.com", "a.wild.example.com"} {
		name := mustName(t, h)
		a, _ := m.decideReport(p, name, synth(uint32(i)))
		m.apply(a, name, synth(uint32(i)))
	}
	before := len(m.byAddr)
	for addr, name := range m.byAddr {
		a, check := m.decideReport(p, name, addr)
		if a != reportRepeat || check != "" {
			t.Fatalf("replay of %s = %d %q, want a repeat", name, a, check)
		}
		m.apply(a, name, addr)
	}
	if len(m.byAddr) != before || len(m.wildcard) != 1 {
		t.Fatalf("replay changed the copy: %d entries, %d counted", len(m.byAddr), len(m.wildcard))
	}
}

// TestCountWildcard: names from ProxydStart count, and are not counted
// again when reported (S07-06-name-mapping, "Wildcard budget, second
// layer").
func TestCountWildcard(t *testing.T) {
	p := mustPolicy(t, policyProto(1, endpoint("*.wild.example.com", 443)))
	m := newMapping()
	m.countWildcard(nil)
	m.countWildcard(canonical(t, "a.wild.example.com"))
	if a, check := m.decideReport(p, mustName(t, "a.wild.example.com"), synth(1)); a != reportAdd || check != "" {
		t.Fatalf("a name ProxydStart counted = %d %q, want an add", a, check)
	}
	if a, check := m.decideReport(p, mustName(t, "b.wild.example.com"), synth(2)); a != reportRefuse || check != CheckWildcardBudget {
		t.Fatalf("a new name past the budget = %d %q", a, check)
	}
}

// TestClearAndNewPeriod: a fresh start drops the copy, the wildcard
// names, the policy and its sequence, and keeps the generation. A new
// active period drops only the wildcard names (S07-06-name-mapping,
// "Lifetime", "Wildcard budget, second layer").
func TestClearAndNewPeriod(t *testing.T) {
	p := mustPolicy(t, policyProto(1, endpoint("*.wild.example.com", 443)))
	x, y := mustName(t, "x.wild.example.com"), mustName(t, "y.wild.example.com")
	fill := func() *vmState {
		st := &vmState{slot: "work", generation: 4, policy: p, sequence: 9, copy: newMapping()}
		st.copy.apply(reportAddWildcard, x, synth(1))
		return st
	}

	st := fill()
	st.copy.newPeriod()
	if _, ok := st.copy.lookup(synth(1)); !ok {
		t.Fatal("a new active period dropped an entry")
	}
	if a, check := st.copy.decideReport(p, y, synth(2)); a != reportAddWildcard {
		t.Fatalf("after a new active period: %d %q, want a counted name", a, check)
	}

	st = fill()
	st.clear()
	if _, ok := st.copy.lookup(synth(1)); ok || len(st.copy.wildcard) != 0 {
		t.Fatalf("a fresh start kept the copy: %+v", st.copy)
	}
	if st.policy != nil || st.sequence != 0 || st.generation != 4 {
		t.Fatalf("after a fresh start: policy %v, sequence %d, generation %d", st.policy, st.sequence, st.generation)
	}
	if a, check := st.copy.decideReport(st.policy, x, synth(1)); a != reportRefuse || check != CheckNotAllowed {
		t.Fatalf("a report with no policy after a fresh start: %d %q", a, check)
	}
}

// TestDecideStream feeds decideStream a violation of each check of
// S07-05-stream-hand-off, "Streams", and sees each refused with its
// check and no name.
func TestDecideStream(t *testing.T) {
	p := mustPolicy(t, policyProto(4, endpoint("a.example.com", 443)))
	m := newMapping()
	m.apply(reportAdd, mustName(t, "a.example.com"), synth(1))
	m.apply(reportAdd, mustName(t, "gone.example.com"), synth(2))
	ok := streamFacts{socketType: true, generation: 3, open: 0, maxStreams: 2}
	msg := func(addr uint32, gen uint64, port uint16) egressmsg.NetdMessage {
		return egressmsg.NetdMessage{Kind: egressmsg.KindStream, Addr: synth(addr), Generation: gen, Port: port, StreamID: 1}
	}
	for _, tc := range []struct {
		name  string
		msg   egressmsg.NetdMessage
		facts streamFacts
		check egressmsg.Check
	}{
		{"passes", msg(1, 3, 443), ok, ""},
		{"socket type", msg(1, 3, 443), streamFacts{generation: 3, maxStreams: 2}, CheckSocketType},
		{"another generation", msg(1, 2, 443), ok, CheckGeneration},
		{"no entry", msg(9, 3, 443), ok, CheckNoEntry},
		{"a name the policy no longer allows", msg(2, 3, 443), ok, CheckNotAllowed},
		{"a port the policy doesn't allow", msg(1, 3, 80), ok, CheckPortNotAllowed},
		{"at the cap", msg(1, 3, 443), streamFacts{socketType: true, generation: 3, open: 2, maxStreams: 2}, CheckStreamCap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, check := decideStream(m, p, tc.msg, tc.facts)
			if check != tc.check {
				t.Fatalf("decideStream = %q, want %q", check, tc.check)
			}
			if check != "" && name.String() != "" {
				t.Fatalf("a refused stream got the name %s", name)
			}
			if check == "" && name.String() != "a.example.com" {
				t.Fatalf("an accepted stream got the name %q", name)
			}
		})
	}
	if _, check := decideStream(m, nil, msg(1, 3, 443), ok); check != CheckNotAllowed {
		t.Fatalf("a stream with no policy = %q", check)
	}
}

func TestCaps(t *testing.T) {
	if err := DefaultCaps().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Caps{{Streams: 0, Pending: 1}, {Streams: 1, Pending: 0}, {Streams: -1, Pending: 5}} {
		if c.Validate() == nil {
			t.Errorf("Validate accepted %+v", c)
		}
	}
}
