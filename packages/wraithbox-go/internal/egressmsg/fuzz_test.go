package egressmsg

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The fuzz targets of the egress contract (C02-streams-and-tls,
// "Fuzzing"; C11-host-daemons, "Fuzzing"). Each feeds arbitrary record
// bytes and a descriptor flag into a decoder and checks that it doesn't
// panic, that every refusal names a check, that nothing it accepts is
// outside the bounds the .proto comments state, and that an accepted
// message encodes again within MaxRecord.

// requireRefusal checks a refusal names a check.
func requireRefusal(t *testing.T, r *Refusal) {
	t.Helper()
	if r.Check == "" {
		t.Fatalf("refusal without a check: %v", r)
	}
}

// requireRecord checks an accepted record's size and that its message
// encodes again within MaxRecord.
func requireRecord(t *testing.T, record []byte, m proto.Message) {
	t.Helper()
	if len(record) < 1 || len(record) > MaxRecord {
		t.Fatalf("accepted a record of %d bytes", len(record))
	}
	if _, err := Marshal(m); err != nil {
		t.Fatalf("an accepted message doesn't encode as a record: %v", err)
	}
}

// requireName checks an accepted name is canonical and within the name
// form, by checking it again.
func requireName(t *testing.T, s string) {
	t.Helper()
	if s == "" || len(s) > 253 || s != strings.ToLower(s) || strings.HasSuffix(s, ".") {
		t.Fatalf("accepted name %q", s)
	}
	if _, r := Name("name", s); r != nil {
		t.Fatalf("accepted name %q that Name refuses: %v", s, r)
	}
}

func requireVM(t *testing.T, vm string) {
	t.Helper()
	if !vmbundle.ValidSlot(vm) {
		t.Fatalf("accepted VM %q", vm)
	}
}

func seedNetd(f *testing.F) {
	f.Add([]byte{}, false)
	f.Add(make([]byte, MaxRecord+1), false)
	f.Add(marshal(f, nameReportMsg("api.example.com", addr1)), false)
	f.Add(marshal(f, nameReportMsg("API.example.com.", addr1)), false)
	f.Add(marshal(f, nameReportMsg("a.example", 0x0A000001)), false)
	f.Add(marshal(f, &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{Release: &egressv1.Release{Address: addr1}}}), false)
	f.Add(marshal(f, streamMsg(nil)), true)
	f.Add(marshal(f, streamMsg(func(s *egressv1.Stream) { s.Port = 70000 })), true)
	f.Add(withField(marshal(f, streamMsg(nil)), 5), true)
}

// FuzzNetdMessage feeds wb-proxyd's decoder of the hand-off socket. The
// record is guest-influenced.
func FuzzNetdMessage(f *testing.F) {
	seedNetd(f)
	f.Fuzz(func(t *testing.T, record []byte, hasFD bool) {
		m, r := DecodeNetdMessage(record, hasFD)
		if r != nil {
			requireRefusal(t, r)
			return
		}
		if !SyntheticRange.Contains(m.Addr) {
			t.Fatalf("accepted address %v", m.Addr)
		}
		wire, _ := AddrToWire(m.Addr)
		var out *egressv1.NetdMessage
		switch m.Kind {
		case KindNameReport:
			requireName(t, m.Name.String())
			out = nameReportMsg(m.Name.String(), wire)
		case KindRelease:
			out = &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{Release: &egressv1.Release{Address: wire}}}
		case KindStream:
			if m.Generation == 0 || m.StreamID == 0 || m.Port == 0 {
				t.Fatalf("accepted stream %+v", m)
			}
			out = streamMsg(func(s *egressv1.Stream) {
				s.Generation, s.Address, s.Port, s.StreamId = m.Generation, wire, uint32(m.Port), m.StreamID
			})
		default:
			t.Fatalf("accepted kind %v", m.Kind)
		}
		if hasFD != (m.Kind == KindStream) {
			t.Fatalf("accepted %v with descriptor %v", m.Kind, hasFD)
		}
		requireRecord(t, record, out)
	})
}

// FuzzProxydMessage feeds wb-netd's decoder of the hand-off socket.
func FuzzProxydMessage(f *testing.F) {
	f.Add([]byte{}, false)
	f.Add(marshal(f, &egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{StreamReset: &egressv1.StreamReset{StreamId: 7}}}), false)
	f.Add(marshal(f, &egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{StreamReset: &egressv1.StreamReset{}}}), false)
	f.Add(marshal(f, nameReportMsg("a.example", addr1)), false)
	f.Fuzz(func(t *testing.T, record []byte, hasFD bool) {
		id, r := DecodeProxydMessage(record, hasFD)
		if r != nil {
			requireRefusal(t, r)
			return
		}
		if id == 0 || hasFD {
			t.Fatalf("accepted stream id %d with descriptor %v", id, hasFD)
		}
		requireRecord(t, record, &egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{StreamReset: &egressv1.StreamReset{StreamId: id}}})
	})
}

// The directions FuzzControlMessage selects with its first byte.
const (
	dirHostdToProxyd = iota
	dirHostdToNetd
	dirProxydToHostd
	dirNetdToHostd
	dirCount
)

// FuzzControlMessage feeds the decoder of each control channel
// direction, which the input's first byte selects; the rest is the
// record.
func FuzzControlMessage(f *testing.F) {
	add := func(dir byte, m proto.Message, fd bool) { f.Add(append([]byte{dir}, marshal(f, m)...), fd) }
	add(dirHostdToProxyd, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ProxydStart{ProxydStart: &egressv1.ProxydStart{}}}, true)
	add(dirHostdToProxyd, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_HandoffSocket{HandoffSocket: &egressv1.HandoffSocket{Vm: "work", Generation: 1}}}, true)
	add(dirHostdToNetd, &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: NewNetdStart("isolated", 2, testLink())}}, true)
	add(dirHostdToNetd, &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{PolicyChange: &egressv1.PolicyChange{Vm: "work", Sequence: 3}}}, true)
	add(dirProxydToHostd, &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketClosed{HandoffSocketClosed: &egressv1.HandoffSocketClosed{
		Vm: "work", Generation: 1, Rule: RuleNameReportMalformed, Check: "name-form",
	}}}, false)
	add(dirProxydToHostd, &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: 1}}}, false)
	add(dirNetdToHostd, &egressv1.NetdToHostd{Kind: &egressv1.NetdToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: 1}}}, false)
	add(dirHostdToProxyd, caIssueMsg("work", caA), false)
	add(dirHostdToProxyd, caSetMsg(nil), false)
	add(dirHostdToProxyd, caAcceptMsg("isolated", 3), false)
	add(dirHostdToProxyd, caRemoveMsg("wraithbox/ca/work/"+caA, "wraithbox/binding/x"), false)
	add(dirProxydToHostd, caIssuedMsg(nil), false)
	add(dirProxydToHostd, caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate, m.Rule = nil, RuleCAIssueRefused }), false)
	add(dirProxydToHostd, caLoadedMsg("work", 1), false)
	add(dirProxydToHostd, caRestoreFailedMsg(nil), false)
	add(dirProxydToHostd, caRemovedMsg(&egressv1.CaRemoved{Rule: RuleCARemovalRefused, Refused: []*egressv1.CaRemovalRefusal{{Index: 1, Check: CheckCAInRecord}}}), false)
	f.Add([]byte{dirNetdToHostd}, false)
	f.Fuzz(func(t *testing.T, in []byte, hasFD bool) {
		if len(in) == 0 {
			return
		}
		record := in[1:]
		var m proto.Message
		var r *Refusal
		switch in[0] % dirCount {
		case dirHostdToProxyd:
			m, r = nilOr(DecodeHostdToProxyd(record, hasFD))
		case dirHostdToNetd:
			m, r = nilOr(DecodeHostdToNetd(record, hasFD))
		case dirProxydToHostd:
			m, r = nilOr(DecodeProxydToHostd(record, hasFD))
		default:
			m, r = nilOr(DecodeNetdToHostd(record, hasFD))
		}
		if r != nil {
			requireRefusal(t, r)
			return
		}
		requireControl(t, m, hasFD)
		requireRecord(t, record, m)
	})
}

// nilOr returns a decoder's result as a proto.Message that is nil, not
// a typed nil, when it refused.
func nilOr[M proto.Message](m M, r *Refusal) (proto.Message, *Refusal) {
	if r != nil {
		return nil, r
	}
	return m, nil
}

// requireControl checks the bounds of an accepted control message: its
// descriptor, its VM, and its numbers.
func requireControl(t *testing.T, m proto.Message, hasFD bool) {
	t.Helper()
	wantFD := false
	var vm string
	var num uint64 = 1
	switch k := m.(type) {
	case *egressv1.HostdToProxyd:
		if requireCA(t, k) {
			wantFD, vm = false, "work"
			break
		}
		wantFD = true
		if s := k.GetHandoffSocket(); s != nil {
			vm, num = s.GetVm(), s.GetGeneration()
		} else if c := k.GetPolicyChange(); c != nil {
			vm, num = c.GetVm(), c.GetSequence()
		} else {
			vm = "work"
		}
	case *egressv1.HostdToNetd:
		wantFD = true
		vm, num = vmNum(
			vmNumber{k.GetNetdStart().GetVm(), k.GetNetdStart().GetGeneration()},
			vmNumber{k.GetHandoffSocket().GetVm(), k.GetHandoffSocket().GetGeneration()},
			vmNumber{k.GetPolicyChange().GetVm(), k.GetPolicyChange().GetSequence()})
	case *egressv1.ProxydToHostd:
		if requireCAReply(t, k) {
			vm = "work"
			break
		}
		c := k.GetHandoffSocketClosed()
		if c != nil && (c.GetRule() != RuleNameReportMalformed || !closedChecks[c.GetCheck()]) {
			t.Fatalf("accepted rule %q and check %q", c.GetRule(), c.GetCheck())
		}
		vm, num = vmNum(
			vmNumber{k.GetHandoffSocketAck().GetVm(), k.GetHandoffSocketAck().GetGeneration()},
			vmNumber{k.GetPolicyAck().GetVm(), k.GetPolicyAck().GetSequence()},
			vmNumber{c.GetVm(), c.GetGeneration()})
	case *egressv1.NetdToHostd:
		vm, num = k.GetPolicyAck().GetVm(), k.GetPolicyAck().GetSequence()
	}
	if hasFD != wantFD {
		t.Fatalf("accepted %T with descriptor %v", m, hasFD)
	}
	requireVM(t, vm)
	if num == 0 {
		t.Fatalf("accepted %T with a generation or sequence of 0", m)
	}
}

// requireCA checks the bounds of an accepted CA message from wb-hostd,
// and reports whether m is one.
func requireCA(t *testing.T, m *egressv1.HostdToProxyd) bool {
	t.Helper()
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue:
		requireVM(t, k.CaIssue.GetVm())
		requireCAID(t, k.CaIssue.GetCaId())
	case *egressv1.HostdToProxyd_CaSet:
		requireCASet(t, k.CaSet)
	case *egressv1.HostdToProxyd_CaAccept:
		requireVM(t, k.CaAccept.GetVm())
		if k.CaAccept.GetGeneration() == 0 {
			t.Fatal("accepted a CaAccept of generation 0")
		}
	case *egressv1.HostdToProxyd_CaRemove:
		if n := len(k.CaRemove.GetItems()); n < 1 || n > MaxRemoveItems {
			t.Fatalf("accepted a CaRemove of %d entries", n)
		}
	default:
		return false
	}
	return true
}

// requireCAReply checks the bounds of an accepted CA reply from
// wb-proxyd, and reports whether m is one.
func requireCAReply(t *testing.T, m *egressv1.ProxydToHostd) bool {
	t.Helper()
	switch k := m.GetKind().(type) {
	case *egressv1.ProxydToHostd_CaIssued:
		requireVM(t, k.CaIssued.GetVm())
		requireCAID(t, k.CaIssued.GetCaId())
		cert, rule := k.CaIssued.GetCertificate(), k.CaIssued.GetRule()
		if (len(cert) > 0) == issueRules[rule] || len(cert) > MaxCACert {
			t.Fatalf("accepted a CaIssued of %d bytes with rule %q", len(cert), rule)
		}
	case *egressv1.ProxydToHostd_CaLoaded:
		requireVM(t, k.CaLoaded.GetVm())
		if k.CaLoaded.GetGeneration() == 0 {
			t.Fatal("accepted a CaLoaded of generation 0")
		}
	case *egressv1.ProxydToHostd_CaRestoreFailed:
		f := k.CaRestoreFailed
		requireVM(t, f.GetVm())
		requireCAID(t, f.GetCaId())
		if f.GetGeneration() == 0 || (f.GetRule() != RuleCAKeyRestore && f.GetRule() != RuleCANeedsTeam) {
			t.Fatalf("accepted a CaRestoreFailed of generation %d with rule %q", f.GetGeneration(), f.GetRule())
		}
	case *egressv1.ProxydToHostd_CaRemoved:
		requireCARemoved(t, k.CaRemoved)
	default:
		return false
	}
	return true
}

func requireCARemoved(t *testing.T, m *egressv1.CaRemoved) {
	t.Helper()
	if len(m.GetDeleted()) > MaxRemoveItems || len(m.GetRefused()) > MaxRemoveItems {
		t.Fatalf("accepted a CaRemoved of %d and %d entries", len(m.GetDeleted()), len(m.GetRefused()))
	}
	for _, s := range m.GetDeleted() {
		if len(s) < 1 || len(s) > MaxCAItem {
			t.Fatalf("accepted a deleted entry of %d bytes", len(s))
		}
	}
	if (m.GetRule() == RuleCARemovalRefused) != (len(m.GetRefused()) > 0) || (len(m.GetRefused()) > 0 && len(m.GetDeleted()) > 0) {
		t.Fatalf("accepted rule %q with %d refused and %d deleted", m.GetRule(), len(m.GetRefused()), len(m.GetDeleted()))
	}
	for _, r := range m.GetRefused() {
		if r.GetIndex() >= MaxRemoveItems || (r.GetCheck() != CheckCAItemForm && r.GetCheck() != CheckCAInRecord) {
			t.Fatalf("accepted refusal %d %q", r.GetIndex(), r.GetCheck())
		}
	}
}

func requireCAID(t *testing.T, id string) {
	t.Helper()
	if !ValidCAID(id) {
		t.Fatalf("accepted CA id %q", id)
	}
}

func requireCASet(t *testing.T, s *egressv1.CaSet) {
	t.Helper()
	requireVM(t, s.GetVm())
	if s.GetGeneration() == 0 || len(s.GetCas()) < 1 || len(s.GetCas()) > MaxCAs {
		t.Fatalf("accepted a CaSet of generation %d with %d CAs", s.GetGeneration(), len(s.GetCas()))
	}
	ids := map[string]bool{}
	for _, ca := range s.GetCas() {
		requireCAID(t, ca.GetCaId())
		if ids[ca.GetCaId()] || len(ca.GetCertificate()) < 1 || len(ca.GetCertificate()) > MaxCACert {
			t.Fatalf("accepted CA %q twice or with a certificate of %d bytes", ca.GetCaId(), len(ca.GetCertificate()))
		}
		ids[ca.GetCaId()] = true
	}
	signing := s.GetSigningCaId()
	if (signing == "") != (s.GetNoSigningRule() == RuleCANotInstalled) || (signing != "" && !ids[signing]) {
		t.Fatalf("accepted signing CA %q with rule %q", signing, s.GetNoSigningRule())
	}
}

// vmNumber is a VM and its generation or sequence number.
type vmNumber struct {
	vm  string
	num uint64
}

// vmNum returns the one set pair among the kinds of a oneof: an unset
// kind's getters return "" and 0.
func vmNum(pairs ...vmNumber) (string, uint64) {
	for _, p := range pairs {
		if p.vm != "" {
			return p.vm, p.num
		}
	}
	return "", 0
}

// FuzzAccountingRecord feeds wb-hostd's decoder of an accounting
// channel, from the sender the input's flag selects. wb-netd's records
// are guest-influenced.
func FuzzAccountingRecord(f *testing.F) {
	f.Add([]byte{}, false, true)
	f.Add(marshal(f, debitRec("x.example.com")), false, true)
	f.Add(marshal(f, entryRec("x.example.com", addr1)), false, true)
	f.Add(marshal(f, releaseRec(addr1)), false, true)
	f.Add(marshal(f, proxydRec("work", "x.example.com")), false, false)
	f.Add(marshal(f, proxydRec("work", "x.example.com")), false, true)
	f.Add(withField(marshal(f, debitRec("a.example")), 5), false, true)
	f.Fuzz(func(t *testing.T, record []byte, hasFD, netd bool) {
		from := SenderProxyd
		if netd {
			from = SenderNetd
		}
		rec, r := DecodeAccountingRecord(record, hasFD, from)
		if r != nil {
			requireRefusal(t, r)
			return
		}
		if hasFD {
			t.Fatal("accepted a record with a descriptor")
		}
		if (rec.Kind == KindProxydWildcardName) != (from == SenderProxyd) {
			t.Fatalf("accepted %v from sender %d", rec.Kind, from)
		}
		out := requireAccounting(t, rec)
		requireRecord(t, record, out)
	})
}

// requireAccounting checks the fields of an accepted accounting record
// and returns it as a message.
func requireAccounting(t *testing.T, rec AccountingRecord) *egressv1.AccountingRecord {
	t.Helper()
	wire, _ := AddrToWire(rec.Addr)
	switch rec.Kind {
	case KindWildcardDebit:
		requireName(t, rec.Name.String())
		return debitRec(rec.Name.String())
	case KindMappingEntry, KindMappingRelease:
		if !SyntheticRange.Contains(rec.Addr) {
			t.Fatalf("accepted address %v", rec.Addr)
		}
		if rec.Kind == KindMappingRelease {
			return releaseRec(wire)
		}
		requireName(t, rec.Name.String())
		return entryRec(rec.Name.String(), wire)
	case KindProxydWildcardName:
		requireVM(t, rec.VM)
		requireName(t, rec.Name.String())
		return proxydRec(rec.VM, rec.Name.String())
	}
	t.Fatalf("accepted kind %v", rec.Kind)
	return nil
}

// FuzzPayload feeds the payload reader and the start payload checks.
// The payloads come from wb-hostd, but a NetdStartPayload holds names
// that came from a guest.
func FuzzPayload(f *testing.F) {
	var b bytes.Buffer
	_ = writePayload(&b, &egressv1.NetdStartPayload{
		Entries: []*egressv1.MappingEntry{{Name: "a.example", Address: addr1}}, Debited: []string{"a.example"}, DebitCount: 1,
	})
	f.Add(b.Bytes(), true)
	b.Reset()
	_ = writePayload(&b, &egressv1.ProxydStartPayload{WildcardNames: []*egressv1.WildcardNames{{Vm: "work", Names: []string{"a.example"}}}})
	f.Add(b.Bytes(), false)
	f.Add([]byte{0, 0, 0, 0}, true)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, true)
	f.Fuzz(func(t *testing.T, in []byte, netd bool) {
		if netd {
			fuzzNetdStart(t, in)
			return
		}
		fuzzProxydStart(t, in)
	})
}

func fuzzNetdStart(t *testing.T, in []byte) {
	t.Helper()
	p := &egressv1.NetdStartPayload{}
	if r := readPayload(bytes.NewReader(in), p); r != nil {
		requireRefusal(t, r)
		return
	}
	if r := CheckNetdStart(p); r != nil {
		requireRefusal(t, r)
		return
	}
	requireNetdStart(t, p)
}

func fuzzProxydStart(t *testing.T, in []byte) {
	t.Helper()
	p := &egressv1.ProxydStartPayload{}
	if r := readPayload(bytes.NewReader(in), p); r != nil {
		requireRefusal(t, r)
		return
	}
	if r := CheckProxydStart(p); r != nil {
		requireRefusal(t, r)
		return
	}
	for _, set := range p.GetWildcardNames() {
		requireVM(t, set.GetVm())
		for _, n := range set.GetNames() {
			requireName(t, n)
		}
	}
}

func requireNetdStart(t *testing.T, p *egressv1.NetdStartPayload) {
	t.Helper()
	if len(p.GetEntries()) > MaxEntries || len(p.GetDebited()) > MaxWildcardNames || p.GetDebitCount() > MaxWildcardNames {
		t.Fatal("accepted a payload over its bounds")
	}
	for _, e := range p.GetEntries() {
		requireName(t, e.GetName())
		if !SyntheticRange.Contains(AddrFromWire(e.GetAddress())) {
			t.Fatalf("accepted address %#x", e.GetAddress())
		}
	}
	for _, n := range p.GetDebited() {
		requireName(t, n)
	}
}
