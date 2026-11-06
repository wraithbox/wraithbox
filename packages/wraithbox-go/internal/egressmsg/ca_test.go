package egressmsg

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	caA = "0123456789abcdef0123456789abcdef"
	caB = "fedcba9876543210fedcba9876543210"
	caC = "00000000000000000000000000000000"
	caD = "11111111111111111111111111111111"
)

func caIssueMsg(vm, id string) proto.Message {
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaIssue{CaIssue: &egressv1.CaIssue{Vm: vm, CaId: id}}}
}

func caSetMsg(edit func(*egressv1.CaSet)) proto.Message {
	s := &egressv1.CaSet{
		Vm: "work", Generation: 2,
		Cas: []*egressv1.InspectionCa{
			{CaId: caA, Certificate: []byte{0x30, 1}},
			{CaId: caB, Certificate: bytes.Repeat([]byte{1}, MaxCACert)},
		},
		SigningCaId: caA,
	}
	if edit != nil {
		edit(s)
	}
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaSet{CaSet: s}}
}

func caAcceptMsg(vm string, gen uint64) proto.Message {
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaAccept{CaAccept: &egressv1.CaAccept{Vm: vm, Generation: gen}}}
}

func caRemoveMsg(items ...string) proto.Message {
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaRemove{CaRemove: &egressv1.CaRemove{Items: items}}}
}

func TestDecodeHostdToProxydCA(t *testing.T) {
	cert := []byte{0x30}
	full := make([]string, MaxRemoveItems)
	for i := range full {
		full[i] = "wraithbox/ca/isolated/" + caA
	}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeHostdToProxyd(b, fd); return r }, []controlCase{
		{"issue", caIssueMsg("work", caA), false, ""},
		{"issue with descriptor", caIssueMsg("work", caA), true, CheckMessageForm},
		{"issue bad VM", caIssueMsg("build", caA), false, CheckVMSlot},
		{"issue short id", caIssueMsg("work", caA[:31]), false, CheckMessageForm},
		{"issue uppercase id", caIssueMsg("work", strings.ToUpper(caA)), false, CheckMessageForm},
		{"issue id with a slash", caIssueMsg("work", "0123456789abcdef/123456789abcdef"), false, CheckMessageForm},
		{"set", caSetMsg(nil), false, ""},
		{"set with descriptor", caSetMsg(nil), true, CheckMessageForm},
		{"set generation 0", caSetMsg(func(s *egressv1.CaSet) { s.Generation = 0 }), false, CheckMessageForm},
		{"set bad VM", caSetMsg(func(s *egressv1.CaSet) { s.Vm = "x" }), false, CheckVMSlot},
		{"set no CAs", caSetMsg(func(s *egressv1.CaSet) { s.Cas = nil; s.SigningCaId = "" }), false, CheckMessageForm},
		{"set four CAs", caSetMsg(func(s *egressv1.CaSet) {
			s.Cas = append(s.Cas, &egressv1.InspectionCa{CaId: caC, Certificate: cert}, &egressv1.InspectionCa{CaId: caD, Certificate: cert})
		}), false, CheckMessageForm},
		{"set three CAs", caSetMsg(func(s *egressv1.CaSet) {
			s.Cas = append(s.Cas, &egressv1.InspectionCa{CaId: caC, Certificate: cert})
		}), false, ""},
		{"set id twice", caSetMsg(func(s *egressv1.CaSet) { s.Cas[1].CaId = caA }), false, CheckMessageForm},
		{"set bad id", caSetMsg(func(s *egressv1.CaSet) { s.Cas[1].CaId = "x" }), false, CheckMessageForm},
		{"set empty certificate", caSetMsg(func(s *egressv1.CaSet) { s.Cas[0].Certificate = nil }), false, CheckMessageForm},
		{"set long certificate", caSetMsg(func(s *egressv1.CaSet) { s.Cas[1].Certificate = append(s.Cas[1].Certificate, 0) }), false, CheckMessageForm},
		{"set signing CA not in the set", caSetMsg(func(s *egressv1.CaSet) { s.SigningCaId = caC }), false, CheckMessageForm},
		{"set signing CA and a rule", caSetMsg(func(s *egressv1.CaSet) { s.NoSigningRule = RuleCANotInstalled }), false, CheckMessageForm},
		{"set not installed", caSetMsg(func(s *egressv1.CaSet) { s.SigningCaId = ""; s.NoSigningRule = RuleCANotInstalled }), false, ""},
		{"set no signing CA and no rule", caSetMsg(func(s *egressv1.CaSet) { s.SigningCaId = "" }), false, CheckMessageForm},
		{"set no signing CA, other rule", caSetMsg(func(s *egressv1.CaSet) { s.SigningCaId = ""; s.NoSigningRule = "ca-key-restore" }), false, CheckMessageForm},
		{"accept", caAcceptMsg("isolated", 3), false, ""},
		{"accept generation 0", caAcceptMsg("isolated", 0), false, CheckMessageForm},
		{"accept with descriptor", caAcceptMsg("isolated", 3), true, CheckMessageForm},
		{"accept bad VM", caAcceptMsg("", 3), false, CheckVMSlot},
		{"remove", caRemoveMsg("wraithbox/ca/work/" + caA), false, ""},
		// The form of an entry is wb-proxyd's check of the batch.
		{"remove another form", caRemoveMsg("wraithbox/binding/x"), false, ""},
		{"remove full", caRemoveMsg(full...), false, ""},
		{"remove none", caRemoveMsg(), false, CheckMessageForm},
		{"remove too many", caRemoveMsg(append(full, full[0])...), false, CheckMessageForm},
		{"remove with descriptor", caRemoveMsg("wraithbox/ca/work/" + caA), true, CheckMessageForm},
	})
}

// TestCAMessagesFit builds the largest CA messages the bounds allow, and
// sees each fit in one record.
func TestCAMessagesFit(t *testing.T) {
	set := caSetMsg(func(s *egressv1.CaSet) {
		s.Vm = "isolated"
		s.Generation = ^uint64(0)
		s.Cas = append(s.Cas, &egressv1.InspectionCa{CaId: caC, Certificate: bytes.Repeat([]byte{1}, MaxCACert)})
		s.Cas[0].Certificate = bytes.Repeat([]byte{1}, MaxCACert)
		s.SigningCaId = ""
		s.NoSigningRule = RuleCANotInstalled
	})
	items := make([]string, MaxRemoveItems)
	for i := range items {
		items[i] = "wraithbox/ca/isolated/" + caA
	}
	refused := make([]*egressv1.CaRemovalRefusal, MaxRemoveItems)
	for i := range refused {
		refused[i] = &egressv1.CaRemovalRefusal{Index: uint32(i), Check: "item-form"}
	}
	for _, m := range []proto.Message{
		set,
		caRemoveMsg(items...),
		&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: &egressv1.CaIssued{
			Vm: "isolated", CaId: caA, Certificate: bytes.Repeat([]byte{1}, MaxCACert),
		}}},
		&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: &egressv1.CaRemoved{Deleted: items}}},
		&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: &egressv1.CaRemoved{
			Rule: "ca-removal-refused", Refused: refused,
		}}},
	} {
		if _, err := Marshal(m); err != nil {
			t.Errorf("%T: %v", m, err)
		}
	}
}

func caIssuedMsg(edit func(*egressv1.CaIssued)) proto.Message {
	m := &egressv1.CaIssued{Vm: "work", CaId: caA, Certificate: []byte{0x30, 1}}
	if edit != nil {
		edit(m)
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: m}}
}

func caRestoreFailedMsg(edit func(*egressv1.CaRestoreFailed)) proto.Message {
	m := &egressv1.CaRestoreFailed{Vm: "work", Generation: 2, CaId: caA, Rule: RuleCAKeyRestore}
	if edit != nil {
		edit(m)
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRestoreFailed{CaRestoreFailed: m}}
}

func caLoadedMsg(vm string, gen uint64) proto.Message {
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{Vm: vm, Generation: gen}}}
}

func caRemovedMsg(m *egressv1.CaRemoved) proto.Message {
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: m}}
}

// TestCAReplies checks the form of each CA reply wb-hostd reads from
// wb-proxyd (control.proto).
func TestCAReplies(t *testing.T) {
	item := "wraithbox/ca/work/" + caA
	full := make([]string, MaxRemoveItems)
	for i := range full {
		full[i] = item
	}
	refusal := func(index uint32, check string) []*egressv1.CaRemovalRefusal {
		return []*egressv1.CaRemovalRefusal{{Index: index, Check: check}}
	}
	refused := func(r []*egressv1.CaRemovalRefusal) *egressv1.CaRemoved {
		return &egressv1.CaRemoved{Rule: RuleCARemovalRefused, Refused: r}
	}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeProxydToHostd(b, fd); return r }, []controlCase{
		{"issued", caIssuedMsg(nil), false, ""},
		{"issued largest certificate", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate = bytes.Repeat([]byte{1}, MaxCACert) }), false, ""},
		{"issued long certificate", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate = bytes.Repeat([]byte{1}, MaxCACert+1) }), false, CheckMessageForm},
		{"issued key restore", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate, m.Rule = nil, RuleCAKeyRestore }), false, ""},
		{"issued needs team", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate, m.Rule = nil, RuleCANeedsTeam }), false, ""},
		{"issued refused", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate, m.Rule = nil, RuleCAIssueRefused }), false, ""},
		{"issued other rule", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate, m.Rule = nil, "ca-leaf-refused" }), false, CheckMessageForm},
		{"issued neither", caIssuedMsg(func(m *egressv1.CaIssued) { m.Certificate = nil }), false, CheckMessageForm},
		{"issued both", caIssuedMsg(func(m *egressv1.CaIssued) { m.Rule = RuleCAKeyRestore }), false, CheckMessageForm},
		{"issued bad VM", caIssuedMsg(func(m *egressv1.CaIssued) { m.Vm = "x" }), false, CheckVMSlot},
		{"issued bad id", caIssuedMsg(func(m *egressv1.CaIssued) { m.CaId = "X" }), false, CheckMessageForm},
		{"issued with descriptor", caIssuedMsg(nil), true, CheckMessageForm},
		{"loaded", caLoadedMsg("isolated", 1), false, ""},
		{"loaded generation 0", caLoadedMsg("work", 0), false, CheckMessageForm},
		{"loaded bad VM", caLoadedMsg("", 1), false, CheckVMSlot},
		{"loaded with descriptor", caLoadedMsg("work", 1), true, CheckMessageForm},
		{"restore failed", caRestoreFailedMsg(nil), false, ""},
		{"restore failed needs team", caRestoreFailedMsg(func(m *egressv1.CaRestoreFailed) { m.Rule = RuleCANeedsTeam }), false, ""},
		{"restore failed issue rule", caRestoreFailedMsg(func(m *egressv1.CaRestoreFailed) { m.Rule = RuleCAIssueRefused }), false, CheckMessageForm},
		{"restore failed no rule", caRestoreFailedMsg(func(m *egressv1.CaRestoreFailed) { m.Rule = "" }), false, CheckMessageForm},
		{"restore failed bad id", caRestoreFailedMsg(func(m *egressv1.CaRestoreFailed) { m.CaId = "" }), false, CheckMessageForm},
		{"restore failed generation 0", caRestoreFailedMsg(func(m *egressv1.CaRestoreFailed) { m.Generation = 0 }), false, CheckMessageForm},
		{"restore failed with descriptor", caRestoreFailedMsg(nil), true, CheckMessageForm},
		{"removed", caRemovedMsg(&egressv1.CaRemoved{Deleted: []string{item}}), false, ""},
		{"removed none", caRemovedMsg(&egressv1.CaRemoved{}), false, ""},
		{"removed full", caRemovedMsg(&egressv1.CaRemoved{Deleted: full}), false, ""},
		{"removed too many", caRemovedMsg(&egressv1.CaRemoved{Deleted: append(full, item)}), false, CheckMessageForm},
		{"removed empty entry", caRemovedMsg(&egressv1.CaRemoved{Deleted: []string{""}}), false, CheckMessageForm},
		{"removed long entry", caRemovedMsg(&egressv1.CaRemoved{Deleted: []string{strings.Repeat("a", MaxCAItem+1)}}), false, CheckMessageForm},
		{"removed refusal with no rule", caRemovedMsg(&egressv1.CaRemoved{Refused: refusal(0, CheckCAItemForm)}), false, CheckMessageForm},
		{"removed other rule", caRemovedMsg(&egressv1.CaRemoved{Rule: "ca-issue-refused"}), false, CheckMessageForm},
		{"refused item form", caRemovedMsg(refused(refusal(0, CheckCAItemForm))), false, ""},
		{"refused in record", caRemovedMsg(refused(refusal(MaxRemoveItems-1, CheckCAInRecord))), false, ""},
		{"refused index past the batch", caRemovedMsg(refused(refusal(MaxRemoveItems, CheckCAInRecord))), false, CheckMessageForm},
		{"refused other check", caRemovedMsg(refused(refusal(0, "vm-slot"))), false, CheckMessageForm},
		{"refused no entries", caRemovedMsg(refused(nil)), false, CheckMessageForm},
		{"refused with deleted", caRemovedMsg(&egressv1.CaRemoved{Rule: RuleCARemovalRefused, Deleted: []string{item}, Refused: refusal(0, CheckCAItemForm)}), false, CheckMessageForm},
		{"removed with descriptor", caRemovedMsg(&egressv1.CaRemoved{}), true, CheckMessageForm},
	})
}

func TestValidCAID(t *testing.T) {
	for _, s := range []string{caA, caB, caC} {
		if !ValidCAID(s) || !storeitem.ValidPart(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", caA[:31], caA + "0", "0123456789ABCDEF0123456789abcdef", caA[:15] + "g" + caA[16:], "-123456789abcdef0123456789abcdef"} {
		if ValidCAID(s) {
			t.Errorf("%q accepted", s)
		}
	}
}
