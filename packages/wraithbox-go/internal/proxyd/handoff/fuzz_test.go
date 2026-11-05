package handoff

import (
	"encoding/binary"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// fuzzNames are the names the structured operations report: allowed by
// an exact entry, by a wildcard, by nothing, and not in canonical form.
var fuzzNames = []string{
	"a.example.com", "b.wild.example.com", "c.wild.example.com", "d.wild.example.com",
	"e.wild.example.com", "other.example.net", "A.example.com", "a.example.com.", "-bad-.example.com", "",
}

// fuzzState is one VM's copy and policy as the receiver holds them.
type fuzzState struct {
	m        *mapping
	policies [2]*policy.Policy
	current  int
}

// FuzzHandoffDecision drives the pure decisions of this package with
// sequences of name reports, releases, streams and policy changes, as
// structured operations and as raw records through
// egressmsg.DecodeNetdMessage (C02-streams-and-tls, "Fuzzing"). After
// each operation it checks that the copy holds no address outside the
// synthetic range, no name outside the canonical form, and no address
// for a second name without a conflict removing the first, that the
// wildcard count stays within the budget, and that a stream is accepted
// only to an address the copy holds, under that name, with a port the
// policy allows. It has no OS code, so it runs on Linux.
func FuzzHandoffDecision(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0, 1, 0, 1, 2, 0, 0, 1})
	f.Add([]byte{0, 1, 0, 1, 0, 2, 0, 2, 0, 3, 0, 3, 0, 4, 0, 4, 0, 1, 0, 2, 2, 0, 0, 2, 3, 0, 0, 0, 2, 0, 0, 2})
	f.Add([]byte{0, 6, 0, 1, 0, 7, 0, 2, 0, 0, 0xf5, 1, 1, 0, 0, 1, 1, 0, 0, 9})
	f.Add(rawOp(f, &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
		NameReport: &egressv1.NameReport{Name: "a.example.com", Address: wire(5)},
	}}))
	policies := [2]*policy.Policy{
		mustPolicy(f, policyProto(3, endpoint("a.example.com", 443), endpoint("*.wild.example.com", 443, 8443))),
		mustPolicy(f, policyProto(3, endpoint("*.wild.example.com", 8443))),
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s := &fuzzState{m: newMapping(), policies: policies}
		for len(data) >= 4 {
			op := data[:4]
			data = data[4:]
			switch op[0] % 5 {
			case 0:
				name, okName := fuzzName(op[1])
				addr, okAddr := fuzzAddr(op[2], op[3])
				if okName && okAddr {
					s.report(t, name, addr)
				}
			case 1:
				if addr, ok := fuzzAddr(op[2], op[3]); ok {
					s.m.release(egressmsg.AddrFromWire(addr))
				}
			case 2:
				s.stream(t, op)
			case 3:
				s.current = int(op[1] % 2)
			case 4:
				n := min(int(op[1]), len(data))
				s.raw(t, data[:n], op[2]%2 == 1, op)
				data = data[n:]
			}
			s.check(t)
		}
	})
}

// rawOp is a seed that feeds an encoded message as a raw record.
func rawOp(f *testing.F, m *egressv1.NetdMessage) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		f.Fatal(err)
	}
	return append([]byte{4, byte(len(b)), 0, 0}, b...)
}

// fuzzName returns a pool name that passes egressmsg's name check.
func fuzzName(b byte) (dnsname.Name, bool) {
	n, r := egressmsg.Name("name", fuzzNames[int(b)%len(fuzzNames)])
	return n, r == nil
}

// fuzzAddr returns a wire address that passes egressmsg's range check:
// mostly a few addresses of the range, so reports collide, and outside
// it past 0xf0.
func fuzzAddr(hi, lo byte) (uint32, bool) {
	w := wire(uint32(lo % 8))
	if hi >= 0xf0 {
		w = binary.BigEndian.Uint32([]byte{198, hi, 0, lo})
	}
	_, r := egressmsg.Addr("address", w)
	return w, r == nil
}

func (s *fuzzState) policy() *policy.Policy { return s.policies[s.current] }

// report decides and applies a report as the receiver does, with a
// wildcard write that always succeeds.
func (s *fuzzState) report(t *testing.T, name dnsname.Name, wireAddr uint32) {
	addr := egressmsg.AddrFromWire(wireAddr)
	a, check := s.m.decideReport(s.policy(), name, addr)
	held, isHeld := s.m.lookup(addr)
	switch a {
	case reportAdd, reportAddWildcard:
		if isHeld {
			t.Fatalf("add of %s to %s, held for %s", name, addr, held)
		}
		if s.policy().NameAllowance(name) == policy.NameRefused {
			t.Fatalf("added %s, which the policy refuses", name)
		}
	case reportConflict:
		if !isHeld || held == name || check != CheckAddressConflict {
			t.Fatalf("conflict on %s for %s: held %q %v, check %q", addr, name, held, isHeld, check)
		}
	case reportRepeat:
		if held != name {
			t.Fatalf("repeat of %s at %s, held for %q", name, addr, held)
		}
	case reportRefuse:
		if check == "" {
			t.Fatal("a refusal without a check")
		}
	}
	s.m.apply(a, name, addr)
	if a == reportConflict {
		if _, still := s.m.lookup(addr); still {
			t.Fatalf("a conflict left %s in the copy", addr)
		}
	}
}

// stream decides a stream from the operation's bytes.
func (s *fuzzState) stream(t *testing.T, op []byte) {
	addr, ok := fuzzAddr(op[1], op[2])
	if !ok {
		return
	}
	ports := []uint16{443, 8443, 80, 22}
	msg := egressmsg.NetdMessage{
		Kind: egressmsg.KindStream, Addr: egressmsg.AddrFromWire(addr),
		Generation: uint64(op[3]%2) + 1, Port: ports[int(op[3]>>1)%len(ports)], StreamID: 1,
	}
	s.decideStream(t, msg, op[3]&0x10 == 0, int(op[3]>>5))
}

func (s *fuzzState) decideStream(t *testing.T, msg egressmsg.NetdMessage, socketType bool, open int) {
	facts := streamFacts{socketType: socketType, generation: 1, open: open, maxStreams: 4}
	name, check := decideStream(s.m, s.policy(), msg, facts)
	if check != "" {
		if name.String() != "" {
			t.Fatalf("a stream refused with %q got the name %s", check, name)
		}
		return
	}
	held, ok := s.m.lookup(msg.Addr)
	switch {
	case !ok || held != name:
		t.Fatalf("a stream to %s accepted as %s, copy holds %q %v", msg.Addr, name, held, ok)
	case !socketType || msg.Generation != 1 || open >= 4:
		t.Fatalf("a stream accepted with socket type %v, generation %d, %d open", socketType, msg.Generation, open)
	case !s.policy().PortAllowed(name, msg.Port):
		t.Fatalf("a stream to %s:%d accepted, which the policy refuses", name, msg.Port)
	}
}

// raw feeds a record through DecodeNetdMessage and decides what it
// accepts.
func (s *fuzzState) raw(t *testing.T, record []byte, hasFD bool, op []byte) {
	msg, ref := egressmsg.DecodeNetdMessage(record, hasFD)
	if ref != nil {
		return
	}
	w, ok := egressmsg.AddrToWire(msg.Addr)
	if !ok {
		t.Fatalf("DecodeNetdMessage accepted the address %s", msg.Addr)
	}
	switch msg.Kind {
	case egressmsg.KindNameReport:
		s.report(t, msg.Name, w)
	case egressmsg.KindRelease:
		s.m.release(msg.Addr)
	case egressmsg.KindStream:
		s.decideStream(t, msg, op[3]&1 == 0, int(op[3]>>5))
	}
}

// check checks the copy's invariants.
func (s *fuzzState) check(t *testing.T) {
	for addr, name := range s.m.byAddr {
		if !egressmsg.SyntheticRange.Contains(addr) {
			t.Fatalf("the copy holds %s, outside the synthetic range", addr)
		}
		if n, r := egressmsg.Name("name", name.String()); r != nil || n != name {
			t.Fatalf("the copy holds %q, outside the canonical form", name)
		}
	}
	if len(s.m.wildcard) > 3 {
		t.Fatalf("the copy counts %d wildcard names, the budget is 3", len(s.m.wildcard))
	}
}
