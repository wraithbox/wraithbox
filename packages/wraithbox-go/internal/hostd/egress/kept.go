package egress

import (
	"cmp"
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// Entry is one entry of a VM's kept name mapping.
type Entry struct {
	Name dnsname.Name
	Addr netip.Addr
	// Received is wb-hostd's receipt time of the entry's last record. A
	// time from wb-netd is never used (S04-08-records-and-bounds,
	// "Mapping entries are checked").
	Received time.Time
}

// Source is the sender of an accounting channel.
type Source struct {
	Sender egressmsg.Sender
	// VM is wb-netd's VM slot, and "" for wb-proxyd, whose records name
	// their VM.
	VM string
}

// NetdSource is the source of the wb-netd of the VM slot vm.
func NetdSource(vm string) Source { return Source{Sender: egressmsg.SenderNetd, VM: vm} }

// ProxydSource is the source of wb-proxyd, which serves every VM.
func ProxydSource() Source { return Source{Sender: egressmsg.SenderProxyd} }

func (s Source) String() string {
	if s.Sender == egressmsg.SenderProxyd {
		return "wb-proxyd"
	}
	return "wb-netd"
}

// Refused is a record wb-hostd refused: a crash of its sender.
type Refused struct {
	Rule  string
	Check string
	// Kind is the record's kind, when it decoded to one.
	Kind egressmsg.AccountingKind
	// Reason says what failed. It holds no name from the record.
	Reason string
}

// vmState is what wb-hostd keeps for one VM slot.
type vmState struct {
	// started is set from the VM's start to its stop.
	started bool
	// served is the generation of the hand-off socket wb-proxyd last
	// acknowledged since the VM's start, and 0 for none.
	served uint64
	// ended is the generation whose wb-netd a HandoffSocketClosed ended.
	ended uint64

	byAddr map[netip.Addr]Entry
	byName map[dnsname.Name]netip.Addr
	// debited are wb-netd's wildcard debits of the active period.
	debited map[dnsname.Name]bool
	// proxydNames are wb-proxyd's wildcard names of the active period
	// (S07-06-name-mapping, "Wildcard budget, second layer").
	proxydNames map[dnsname.Name]bool
}

func newVMState() *vmState {
	v := &vmState{byAddr: map[netip.Addr]Entry{}, byName: map[dnsname.Name]netip.Addr{}}
	v.newPeriod()
	return v
}

func (v *vmState) newPeriod() {
	v.debited = map[dnsname.Name]bool{}
	v.proxydNames = map[dnsname.Name]bool{}
}

// State holds each VM's kept name mapping and wildcard counts, learned
// from accounting records (S04-07-supervision, "The kept name mapping",
// "What a restart keeps"). It is safe for concurrent use.
type State struct {
	log  *slog.Logger
	rate *slog.Logger
	now  func() time.Time

	// maxEntries and maxNames are the bounds per VM, lowered by tests.
	maxEntries int
	maxNames   int

	mu  sync.Mutex
	vms map[string]*vmState
}

// StateConfig is what NewState needs.
type StateConfig struct {
	// Log takes the log records. Nil is slog.Default.
	Log *slog.Logger
	// Now is wb-hostd's clock. Nil is time.Now.
	Now func() time.Time
}

// NewState returns a State that holds no VM.
func NewState(cfg StateConfig) *State {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &State{
		log: log, rate: ratelog.New(log), now: now,
		maxEntries: egressmsg.MaxEntries, maxNames: egressmsg.MaxWildcardNames,
		vms: map[string]*vmState{},
	}
}

// Start marks the VM slot vm as started. A start that isn't a restore
// clears its kept mapping and its counts, and so does a restore of a VM
// wb-hostd holds nothing for (S07-06-name-mapping, "Lifetime").
func (s *State) Start(vm string, restore bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vms[vm]
	if !ok || !restore {
		v = newVMState()
		s.vms[vm] = v
	}
	v.started = true
	v.served = 0
	v.ended = 0
}

// Stop marks vm as stopped. Its kept mapping and counts stay for a
// restore. wb-proxyd's names for it are refused until its next start.
func (s *State) Stop(vm string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vms[vm]; ok {
		v.started = false
		v.served = 0
	}
}

// NewActivePeriod clears vm's wildcard counts (S07-03-dns, "Wildcard
// budget").
func (s *State) NewActivePeriod(vm string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vms[vm]; ok {
		v.newPeriod()
	}
}

// Kept returns vm's kept mapping, by address, and its counts: the names
// wb-netd debited and the names wb-proxyd counted in the active period,
// each sorted. A restart and a restore read them (#480).
func (s *State) Kept(vm string) (entries []Entry, debited, proxydNames []dnsname.Name) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vms[vm]
	if !ok {
		return nil, nil, nil
	}
	for _, e := range v.byAddr {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return a.Addr.Compare(b.Addr) })
	return entries, sortedNames(v.debited), sortedNames(v.proxydNames)
}

func sortedNames(set map[dnsname.Name]bool) []dnsname.Name {
	names := make([]dnsname.Name, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b dnsname.Name) int { return cmp.Compare(a.String(), b.String()) })
	return names
}

// served records that wb-proxyd acknowledged vm's hand-off socket of
// generation gen. A VM that isn't started keeps none.
func (s *State) served(vm string, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vms[vm]; ok && v.started {
		v.served = max(v.served, gen)
	}
}

// servedGeneration returns the generation of vm's acknowledged hand-off
// socket, and 0 for none.
func (s *State) servedGeneration(vm string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vms[vm]; ok && v.started {
		return v.served
	}
	return 0
}

// endOnce reports whether a HandoffSocketClosed for vm and gen names
// the VM's current socket and is the first for it. It then marks the
// generation as ended.
func (s *State) endOnce(vm string, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vms[vm]
	if !ok || !v.started || v.served == 0 || gen != v.served || v.ended == gen {
		return false
	}
	v.ended = gen
	return true
}

// Record checks one accounting record from src, and whether a
// descriptor came with it, and applies it to the kept state. A refusal
// is a crash of the sender, and nothing of the record is kept
// (S04-08-records-and-bounds, "Accounting and event channels",
// "Mapping entries are checked").
func (s *State) Record(src Source, record []byte, hasFD bool) *Refused {
	rec, ref := egressmsg.DecodeAccountingRecord(record, hasFD, src.Sender)
	if ref != nil {
		rule := RuleRecordMalformed
		if rec.Kind == egressmsg.KindMappingEntry {
			rule = RuleEntryInvalid
		}
		return &Refused{Rule: rule, Check: string(ref.Check), Kind: rec.Kind, Reason: ref.Error()}
	}
	vm := src.VM
	if rec.Kind == egressmsg.KindProxydWildcardName {
		vm = rec.VM
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vms[vm]
	if bad := s.notServed(src, rec, v, ok); bad != nil {
		return bad
	}
	switch rec.Kind {
	case egressmsg.KindWildcardDebit:
		return s.count(v.debited, rec)
	case egressmsg.KindMappingEntry:
		return s.entry(v, rec)
	case egressmsg.KindMappingRelease:
		s.release(vm, v, rec.Addr)
		return nil
	case egressmsg.KindProxydWildcardName:
		return s.count(v.proxydNames, rec)
	}
	// DecodeAccountingRecord returns a kind with every record it accepts.
	return &Refused{Rule: RuleRecordMalformed, Check: string(egressmsg.CheckMessageForm), Reason: "a record with no kind"}
}

// notServed refuses a wb-netd record for a VM that wasn't started, and
// a wb-proxyd name for a VM whose hand-off socket wb-proxyd hasn't
// acknowledged since the VM's start (SEC08-proj-isolation;
// S07-06-name-mapping, "Kept by wb-hostd"). wb-netd
// records that were in the channel when its VM stopped are still kept,
// so a stop keeps them.
func (s *State) notServed(src Source, rec egressmsg.AccountingRecord, v *vmState, ok bool) *Refused {
	switch {
	case !ok:
		return &Refused{Rule: RuleRecordMalformed, Check: CheckVMNotServed, Kind: rec.Kind, Reason: "a record for a VM that wasn't started"}
	case src.Sender == egressmsg.SenderProxyd && (!v.started || v.served == 0):
		return &Refused{
			Rule: RuleRecordMalformed, Check: CheckVMNotServed, Kind: rec.Kind,
			Reason: "a wildcard name for a VM with no acknowledged hand-off socket",
		}
	}
	return nil
}

// count adds a wildcard name to a set. A name the set holds is a
// repeat, which netd sends again after a write it thinks failed, and
// changes nothing.
func (s *State) count(set map[dnsname.Name]bool, rec egressmsg.AccountingRecord) *Refused {
	if set[rec.Name] {
		return nil
	}
	if len(set) >= s.maxNames {
		return &Refused{
			Rule: RuleRecordMalformed, Check: CheckWildcardCount, Kind: rec.Kind,
			Reason: "a wildcard name past the bound of names per VM",
		}
	}
	set[rec.Name] = true
	return nil
}

// entry keeps a new mapping entry, or renews one. It refuses one past
// the count and one that conflicts with a live entry
// (S07-06-name-mapping, "Kept by wb-hostd").
func (s *State) entry(v *vmState, rec egressmsg.AccountingRecord) *Refused {
	held, addrHeld := v.byAddr[rec.Addr]
	at, nameHeld := v.byName[rec.Name]
	switch {
	case addrHeld && held.Name == rec.Name:
		held.Received = s.now()
		v.byAddr[rec.Addr] = held
		return nil
	case addrHeld:
		return &Refused{
			Rule: RuleEntryInvalid, Check: CheckMappingConflict, Kind: rec.Kind,
			Reason: "the address is held for another name",
		}
	case nameHeld:
		return &Refused{
			Rule: RuleEntryInvalid, Check: CheckMappingConflict, Kind: rec.Kind,
			Reason: "the name is held at " + at.String(),
		}
	case len(v.byAddr) >= s.maxEntries:
		return &Refused{
			Rule: RuleEntryInvalid, Check: CheckEntryCount, Kind: rec.Kind,
			Reason: "a new entry past the bound of live entries per VM",
		}
	}
	v.byAddr[rec.Addr] = Entry{Name: rec.Name, Addr: rec.Addr, Received: s.now()}
	v.byName[rec.Name] = rec.Addr
	return nil
}

// release removes the entry of addr. A release for an address the kept
// mapping doesn't hold is logged and ignored (S07-06-name-mapping,
// "Kept by wb-hostd").
func (s *State) release(vm string, v *vmState, addr netip.Addr) {
	e, ok := v.byAddr[addr]
	if !ok {
		s.rate.LogAttrs(context.Background(), slog.LevelWarn, "mapping release ignored",
			slog.String("rule", RuleReleaseUnheld), slog.String("decision", "ignore"),
			slog.String("vm", vm), slog.String("address", addr.String()))
		return
	}
	delete(v.byAddr, addr)
	delete(v.byName, e.Name)
}
