package egress

import (
	"log/slog"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
)

// fuzzBound is the lowered bound on entries and names per VM, so the
// fuzzer reaches the count checks.
const fuzzBound = 8

// The ops of a FuzzAccountingRecords input. Each op is one byte; a
// record op is followed by a length byte and the record.
const (
	opNetdWork = iota
	opNetdIsolated
	opProxyd
	opServe
	opStop
	opRestore
	opCount
)

// FuzzAccountingRecords feeds a State sequences of records from wb-netd
// and wb-proxyd, with and without a descriptor, between starts, stops,
// restores and acknowledged hand-off sockets. After each record the kept
// state holds only checked entries and names, within the bounds, with
// one address per name and one name per address. A record with a
// descriptor is always refused, and every refusal has one of the two
// rules that end the sender (S04-08-records-and-bounds, "Accounting and
// event channels").
func FuzzAccountingRecords(f *testing.F) {
	f.Add(seed(f, opNetdWork, entryRecord(f, "a.example.com", wire(1)), opNetdWork, releaseRecord(f, wire(1))))
	f.Add(seed(f, opNetdWork, debitRecord(f, "a.example.com"), opServe, nil, opProxyd, proxydNameRecord(f, "work", "a.example.com")))
	f.Add(seed(f, opNetdIsolated, entryRecord(f, "a.example.com", wire(1)), opNetdWork, entryRecord(f, "a.example.com", wire(2))))
	f.Add(seed(f, opStop, nil, opNetdWork, entryRecord(f, "b.example.com", wire(3)), opRestore, nil))
	f.Fuzz(func(t *testing.T, in []byte) {
		s := NewState(StateConfig{Log: slog.New(slog.DiscardHandler)})
		s.maxEntries, s.maxNames = fuzzBound, fuzzBound
		s.Start("work", false)
		s.Start("isolated", false)
		for len(in) > 0 {
			op, hasFD := in[0]%opCount, in[0]&0x80 != 0
			in = in[1:]
			var record []byte
			record, in = take(in)
			if refused := apply(t, s, op, record, hasFD); refused {
				// The reader ends the sender and reads no more.
				return
			}
			checkKept(t, s)
		}
	})
}

// seed builds a fuzz input of ops, each followed by its record.
func seed(tb testing.TB, parts ...any) []byte {
	tb.Helper()
	var out []byte
	for i := 0; i+1 < len(parts); i += 2 {
		op, ok := parts[i].(int)
		if !ok {
			tb.Fatalf("part %d isn't an op", i)
		}
		rec, _ := parts[i+1].([]byte)
		out = append(out, byte(op), byte(len(rec)))
		out = append(out, rec...)
	}
	return out
}

// take splits a length-prefixed record off in.
func take(in []byte) (record, rest []byte) {
	if len(in) == 0 {
		return nil, nil
	}
	n := min(int(in[0]), len(in)-1)
	return in[1 : 1+n], in[1+n:]
}

// apply runs one op, and reports whether a record was refused.
func apply(t *testing.T, s *State, op byte, record []byte, hasFD bool) bool {
	t.Helper()
	var src Source
	switch op {
	case opNetdWork:
		src = NetdSource("work")
	case opNetdIsolated:
		src = NetdSource("isolated")
	case opProxyd:
		src = ProxydSource()
	case opServe:
		s.served("work", uint64(len(record))+1)
		return false
	case opStop:
		s.Stop("work")
		return false
	default:
		s.Start("work", true)
		return false
	}
	ref := s.Record(src, record, hasFD)
	if ref == nil && hasFD {
		t.Fatal("a record with a descriptor was kept")
	}
	if ref != nil && ref.Rule != RuleRecordMalformed && ref.Rule != RuleEntryInvalid {
		t.Fatalf("a refusal with the rule %s", ref.Rule)
	}
	return ref != nil
}

// checkKept checks the invariants of the kept state of each VM.
func checkKept(t *testing.T, s *State) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for vm, v := range s.vms {
		checkVM(t, vm, v)
	}
}

func checkVM(t *testing.T, vm string, v *vmState) {
	t.Helper()
	if len(v.byAddr) > fuzzBound || len(v.debited) > fuzzBound || len(v.proxydNames) > fuzzBound {
		t.Fatalf("%s: %d entries, %d debits, %d names: past the bound", vm, len(v.byAddr), len(v.debited), len(v.proxydNames))
	}
	if len(v.byAddr) != len(v.byName) {
		t.Fatalf("%s: %d addresses but %d names", vm, len(v.byAddr), len(v.byName))
	}
	for addr, e := range v.byAddr {
		if e.Addr != addr || v.byName[e.Name] != addr {
			t.Fatalf("%s: entry %v doesn't match its indexes", vm, e)
		}
		if !egressmsg.SyntheticRange.Contains(addr) {
			t.Fatalf("%s: kept address %v outside the range", vm, addr)
		}
		if _, ref := egressmsg.Name("name", e.Name.String()); ref != nil {
			t.Fatalf("%s: kept name out of form: %v", vm, ref)
		}
	}
}
