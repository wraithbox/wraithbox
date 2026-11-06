package egress

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
)

// clock is a fake clock that a test moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newState(t *testing.T) (*State, *clock, *syncBuffer) {
	t.Helper()
	log, buf := newLog()
	c := &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	return NewState(StateConfig{Log: log, Now: c.now}), c, buf
}

func mustRecord(t *testing.T, s *State, src Source, record []byte) {
	t.Helper()
	if ref := s.Record(src, record, false); ref != nil {
		t.Fatalf("record refused: %+v", ref)
	}
}

func names(ns []dnsname.Name) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.String()
	}
	return out
}

// TestEntriesKeptUntilRelease: an entry is kept with wb-hostd's receipt
// time, a renewal moves the time, and only its release removes it
// (S04-07-supervision, "The kept name mapping").
func TestEntriesKeptUntilRelease(t *testing.T) {
	s, c, _ := newState(t)
	s.Start("work", false)
	netd := NetdSource("work")
	first := c.t
	mustRecord(t, s, netd, entryRecord(t, "b.example.com", wire(2)))
	mustRecord(t, s, netd, entryRecord(t, "a.example.com", wire(1)))
	c.t = c.t.Add(time.Hour)
	mustRecord(t, s, netd, entryRecord(t, "b.example.com", wire(2)))

	entries, _, _ := s.Kept("work")
	if len(entries) != 2 || entries[0].Addr != synth(1) || entries[1].Addr != synth(2) {
		t.Fatalf("kept %v, want both entries by address", entries)
	}
	if !entries[0].Received.Equal(first) || !entries[1].Received.Equal(c.t) {
		t.Fatalf("receipt times %v, %v; want %v and the renewal's %v", entries[0].Received, entries[1].Received, first, c.t)
	}

	mustRecord(t, s, netd, releaseRecord(t, wire(1)))
	entries, _, _ = s.Kept("work")
	if len(entries) != 1 || entries[0].Name.String() != "b.example.com" {
		t.Fatalf("after the release: %v", entries)
	}
	// The released address may go to another name, and the name to
	// another address.
	mustRecord(t, s, netd, entryRecord(t, "c.example.com", wire(1)))
	mustRecord(t, s, netd, releaseRecord(t, wire(1)))
	mustRecord(t, s, netd, entryRecord(t, "c.example.com", wire(3)))
	if other, _, _ := s.Kept("isolated"); other != nil {
		t.Fatalf("the other VM holds %v", other)
	}
}

// TestMappingEntryInvalid: an entry that fails a check is refused with
// mapping-entry-invalid and its check, which ends wb-netd, and nothing
// of it is kept (S04-08-records-and-bounds, "Mapping entries are
// checked").
func TestMappingEntryInvalid(t *testing.T) {
	tests := []struct {
		name   string
		record func(t *testing.T) []byte
		check  string
	}{
		{"uppercase", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "New.example.com", wire(9)) }, string(egressmsg.CheckNameForm)},
		{"trailing dot", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "new.example.com.", wire(9)) }, string(egressmsg.CheckNameForm)},
		{"not LDH", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "new_1.example.com", wire(9)) }, string(egressmsg.CheckNameForm)},
		{"outside the range", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "new.example.com", 10<<24|1) }, string(egressmsg.CheckSyntheticRange)},
		{"address held", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "new.example.com", wire(1)) }, CheckMappingConflict},
		{"name held", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "a.example.com", wire(9)) }, CheckMappingConflict},
		{"past the count", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "new.example.com", wire(9)) }, CheckEntryCount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newState(t)
			s.Start("work", false)
			mustRecord(t, s, NetdSource("work"), entryRecord(t, "a.example.com", wire(1)))
			if tt.check == CheckEntryCount {
				s.maxEntries = 1
			}
			ref := s.Record(NetdSource("work"), tt.record(t), false)
			if ref == nil || ref.Rule != RuleEntryInvalid || ref.Check != tt.check {
				t.Fatalf("refusal %+v, want %s %s", ref, RuleEntryInvalid, tt.check)
			}
			entries, _, _ := s.Kept("work")
			if len(entries) != 1 || entries[0].Addr != synth(1) {
				t.Fatalf("kept %v after a refusal", entries)
			}
		})
	}
}

// TestRecordMalformed: every other refused record is
// accounting-record-malformed, with its check.
func TestRecordMalformed(t *testing.T) {
	tests := []struct {
		name   string
		src    Source
		record func(t *testing.T) []byte
		hasFD  bool
		check  string
	}{
		{"no decoding", NetdSource("work"), func(*testing.T) []byte { return []byte{0xff, 0xff} }, false, string(egressmsg.CheckMessageForm)},
		{"empty", NetdSource("work"), func(*testing.T) []byte { return nil }, false, string(egressmsg.CheckMessageForm)},
		{"descriptor", NetdSource("work"), func(t *testing.T) []byte { t.Helper(); return debitRecord(t, "x.example.com") }, true, string(egressmsg.CheckMessageForm)},
		{"netd sends a proxyd name", NetdSource("work"), func(t *testing.T) []byte { t.Helper(); return proxydNameRecord(t, "work", "x.example.com") }, false, string(egressmsg.CheckMessageForm)},
		{"proxyd sends a debit", ProxydSource(), func(t *testing.T) []byte { t.Helper(); return debitRecord(t, "x.example.com") }, false, string(egressmsg.CheckMessageForm)},
		{"debit name form", NetdSource("work"), func(t *testing.T) []byte { t.Helper(); return debitRecord(t, "X.example.com") }, false, string(egressmsg.CheckNameForm)},
		{"release outside the range", NetdSource("work"), func(t *testing.T) []byte { t.Helper(); return releaseRecord(t, 10<<24|1) }, false, string(egressmsg.CheckSyntheticRange)},
		{"proxyd names no slot", ProxydSource(), func(t *testing.T) []byte { t.Helper(); return proxydNameRecord(t, "build", "x.example.com") }, false, string(egressmsg.CheckVMSlot)},
		{"netd of a VM never started", NetdSource("isolated"), func(t *testing.T) []byte { t.Helper(); return debitRecord(t, "x.example.com") }, false, CheckVMNotServed},
		{"proxyd names a VM never started", ProxydSource(), func(t *testing.T) []byte { t.Helper(); return proxydNameRecord(t, "isolated", "x.example.com") }, false, CheckVMNotServed},
		{"proxyd sends a mapping entry", ProxydSource(), func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "x.example.com", wire(9)) }, false, string(egressmsg.CheckMessageForm)},
		{"debit past the count", NetdSource("work"), func(t *testing.T) []byte { t.Helper(); return debitRecord(t, "y.example.com") }, false, CheckWildcardCount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newState(t)
			s.Start("work", false)
			mustRecord(t, s, NetdSource("work"), debitRecord(t, "a.example.com"))
			s.maxNames = 1
			ref := s.Record(tt.src, tt.record(t), tt.hasFD)
			if ref == nil || ref.Rule != RuleRecordMalformed || ref.Check != tt.check {
				t.Fatalf("refusal %+v, want %s %s", ref, RuleRecordMalformed, tt.check)
			}
			if _, debited, _ := s.Kept("work"); len(debited) != 1 {
				t.Fatalf("debited %v after a refusal", names(debited))
			}
		})
	}
}

// TestProxydNamesServedVM: wb-proxyd's wildcard names are kept per VM.
// A name for a slot wb-hostd never started is refused with
// vm-not-served. One for a VM that is stopped, or whose hand-off socket
// wb-proxyd hasn't acknowledged since a restart, is kept and logged with
// proxyd-name-unserved: wb-proxyd may have written it before
// (S07-06-name-mapping, "Kept by wb-hostd"; SEC08-proj-isolation).
func TestProxydNamesServedVM(t *testing.T) {
	s, _, buf := newState(t)
	s.Start("work", false)
	s.served("work", 3)
	mustRecord(t, s, ProxydSource(), proxydNameRecord(t, "work", "a.example.com"))
	mustRecord(t, s, ProxydSource(), proxydNameRecord(t, "work", "a.example.com"))
	if ref := s.Record(ProxydSource(), proxydNameRecord(t, "isolated", "a.example.com"), false); ref == nil || ref.Check != CheckVMNotServed {
		t.Fatalf("a name for a VM never started: %+v", ref)
	}
	if strings.Contains(buf.String(), RuleNameUnserved) {
		t.Fatalf("a name for a served VM logged %s", RuleNameUnserved)
	}

	s.Stop("work")
	mustRecord(t, s, ProxydSource(), proxydNameRecord(t, "work", "b.example.com"))
	expectLog(t, buf, `"rule":"proxyd-name-unserved"`, `"decision":"keep"`, `"vm":"work"`, `"name":"b.example.com"`, `"started":false`)
	if _, _, got := s.Kept("work"); !slices.Equal(names(got), []string{"a.example.com", "b.example.com"}) {
		t.Fatalf("wb-proxyd names %v after the stop", names(got))
	}

	s.Start("work", false)
	mustRecord(t, s, ProxydSource(), proxydNameRecord(t, "work", "c.example.com"))
	if _, _, got := s.Kept("work"); !slices.Equal(names(got), []string{"c.example.com"}) {
		t.Fatalf("wb-proxyd names %v after the restart", names(got))
	}
}

// TestProxydNameAfterStopKeepsProxyd: VM A stops, or restarts, while
// wb-proxyd's wildcard name for it is still in the accounting channel.
// The reader keeps it and doesn't end wb-proxyd, which serves VM B too
// (SEC08-proj-isolation).
func TestProxydNameAfterStopKeepsProxyd(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%v", restart), func(t *testing.T) {
			s, _, _ := newState(t)
			s.Start("work", false)
			s.Start("isolated", false)
			s.served("work", 1)
			s.served("isolated", 1)
			conn, end, r := startReader(t, s, ProxydSource())
			conn.push(hostversion.Record(own))
			s.Stop("work")
			if restart {
				s.Start("work", false)
			}
			conn.push(proxydNameRecord(t, "work", "a.example.com"))
			conn.push(proxydNameRecord(t, "isolated", "b.example.com"))
			close(conn.in)
			done(t, r)
			if got := end.got(); len(got) != 0 {
				t.Fatalf("wb-proxyd ended with %q", got)
			}
			if _, _, got := s.Kept("isolated"); !slices.Equal(names(got), []string{"b.example.com"}) {
				t.Fatalf("VM B's names %v", names(got))
			}
		})
	}
}

// TestReleaseUnheld: a release for an address the kept mapping doesn't
// hold is logged with mapping-release-unheld and ignored.
func TestReleaseUnheld(t *testing.T) {
	s, _, buf := newState(t)
	s.Start("work", false)
	mustRecord(t, s, NetdSource("work"), entryRecord(t, "a.example.com", wire(1)))
	mustRecord(t, s, NetdSource("work"), releaseRecord(t, wire(7)))
	expectLog(t, buf, `"rule":"mapping-release-unheld"`, `"decision":"ignore"`, `"address":"198.18.0.7"`)
	if entries, _, _ := s.Kept("work"); len(entries) != 1 {
		t.Fatalf("kept %v", entries)
	}
}

// TestRepeatsCountOnce: a repeated debit, after a write netd thought had
// failed, is counted once.
func TestRepeatsCountOnce(t *testing.T) {
	s, _, _ := newState(t)
	s.Start("work", false)
	s.maxNames = 2
	for range 3 {
		mustRecord(t, s, NetdSource("work"), debitRecord(t, "a.example.com"))
	}
	mustRecord(t, s, NetdSource("work"), debitRecord(t, "b.example.com"))
	if _, debited, _ := s.Kept("work"); !slices.Equal(names(debited), []string{"a.example.com", "b.example.com"}) {
		t.Fatalf("debited %v", names(debited))
	}
}

// TestLifetime: a start that isn't a restore clears the kept mapping and
// the counts, a stop and a restore keep them, and a new active period
// clears only the counts (S07-06-name-mapping, "Lifetime").
func TestLifetime(t *testing.T) {
	s, _, _ := newState(t)
	s.Start("work", false)
	mustRecord(t, s, NetdSource("work"), entryRecord(t, "a.example.com", wire(1)))
	mustRecord(t, s, NetdSource("work"), debitRecord(t, "a.example.com"))
	s.Stop("work")
	// Records left in the channel when the VM stopped are still kept.
	mustRecord(t, s, NetdSource("work"), entryRecord(t, "b.example.com", wire(2)))
	s.Start("work", true)
	entries, debited, _ := s.Kept("work")
	if len(entries) != 2 || len(debited) != 1 {
		t.Fatalf("a restore kept %v and %v", entries, names(debited))
	}
	s.NewActivePeriod("work")
	entries, debited, _ = s.Kept("work")
	if len(entries) != 2 || len(debited) != 0 {
		t.Fatalf("a new active period kept %v and %v", entries, names(debited))
	}
	s.Start("work", false)
	if entries, _, _ := s.Kept("work"); len(entries) != 0 {
		t.Fatalf("a fresh start kept %v", entries)
	}
	s.Start("isolated", true)
	if entries, _, _ := s.Kept("isolated"); len(entries) != 0 {
		t.Fatalf("a restore of a VM with nothing kept holds %v", entries)
	}
}
