package ca

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// FuzzCAControlMessage feeds any bytes to wb-proxyd's parser of the CA
// messages as one control record: egressmsg.DecodeHostdToProxyd, then
// the Authority over the fakes. It never panics. A message that parses
// has a VM slot and CA ids of the fixed form, a removal entry that
// passes its check is an item `ca/<vm>/<ca>`, and each reply has the
// form control.proto gives it.
func FuzzCAControlMessage(f *testing.F) {
	for _, m := range []*egressv1.HostdToProxyd{
		{Kind: &egressv1.HostdToProxyd_CaIssue{CaIssue: &egressv1.CaIssue{Vm: "work", CaId: caA}}},
		{Kind: &egressv1.HostdToProxyd_CaSet{CaSet: &egressv1.CaSet{
			Vm: "isolated", Generation: 3, SigningCaId: caA,
			Cas: []*egressv1.InspectionCa{{CaId: caA, Certificate: []byte{0x30, 0x03, 0x02, 0x01, 0x01}}},
		}}},
		{Kind: &egressv1.HostdToProxyd_CaSet{CaSet: &egressv1.CaSet{
			Vm: "work", Generation: 1, NoSigningRule: RuleNotInstalled,
			Cas: []*egressv1.InspectionCa{{CaId: caB, Certificate: []byte{1}}},
		}}},
		{Kind: &egressv1.HostdToProxyd_CaAccept{CaAccept: &egressv1.CaAccept{Vm: "work", Generation: 1}}},
		{Kind: &egressv1.HostdToProxyd_CaRemove{CaRemove: &egressv1.CaRemove{Items: []string{
			item("work", caA), "wraithbox/binding/x", item("other", caB), item("work", caB) + "/x",
		}}}},
	} {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, record []byte) {
		m, ref := egressmsg.DecodeHostdToProxyd(record, false)
		if ref != nil || !isCA(m) {
			return
		}
		requireParsed(t, m)
		e := newEnv(t, team)
		// A CA in the record, so an in-record removal entry can occur.
		e.ready()
		reply, _ := e.a.Handle(t.Context(), m)
		requireReply(t, m, reply)
	})
}

func isCA(m *egressv1.HostdToProxyd) bool {
	switch m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue, *egressv1.HostdToProxyd_CaSet,
		*egressv1.HostdToProxyd_CaAccept, *egressv1.HostdToProxyd_CaRemove:
		return true
	}
	return false
}

// requireParsed checks the fields of a CA message that parsed.
func requireParsed(t *testing.T, m *egressv1.HostdToProxyd) {
	t.Helper()
	var vm string
	var ids []string
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue:
		vm, ids = k.CaIssue.GetVm(), []string{k.CaIssue.GetCaId()}
	case *egressv1.HostdToProxyd_CaSet:
		vm = k.CaSet.GetVm()
		for _, ca := range k.CaSet.GetCas() {
			ids = append(ids, ca.GetCaId())
		}
	case *egressv1.HostdToProxyd_CaAccept:
		vm = k.CaAccept.GetVm()
	case *egressv1.HostdToProxyd_CaRemove:
		requireRemoval(t, k.CaRemove.GetItems())
		return
	}
	if !vmbundle.ValidSlot(vm) {
		t.Fatalf("parsed VM %q isn't a slot", vm)
	}
	for _, id := range ids {
		if !egressmsg.ValidCAID(id) {
			t.Fatalf("parsed CA id %q isn't of the fixed form", id)
		}
		if _, err := itemName(vm, id); err != nil {
			t.Fatalf("the item of %s, %s doesn't parse: %v", vm, id, err)
		}
	}
}

// requireRemoval checks that each removal entry that passes its check
// is an item of the CA form with a VM slot and a CA id.
func requireRemoval(t *testing.T, items []string) {
	t.Helper()
	ok, refused := parseRemoval(items, func(string) bool { return false })
	if len(refused) > 0 && ok != nil {
		t.Fatal("parseRemoval returned entries with a refusal")
	}
	for _, r := range ok {
		if r.item.Form() != storeitem.FormCA || !vmbundle.ValidSlot(r.vm) || !egressmsg.ValidCAID(r.id) ||
			r.item.String() != item(r.vm, r.id) {
			t.Fatalf("removal entry %q passed as %s, %s", r.item, r.vm, r.id)
		}
	}
}

// requireReply checks the form of the reply to a CA message.
func requireReply(t *testing.T, m *egressv1.HostdToProxyd, reply *egressv1.ProxydToHostd) {
	t.Helper()
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue:
		got := reply.GetCaIssued()
		if (got.GetRule() == "") == (len(got.GetCertificate()) == 0) {
			t.Fatalf("CaIssued = %v, want exactly one of a certificate and a rule", got)
		}
	case *egressv1.HostdToProxyd_CaSet:
		if reply.GetCaLoaded() == nil && reply.GetCaRestoreFailed().GetRule() == "" {
			t.Fatalf("reply to a CaSet = %v", reply)
		}
	case *egressv1.HostdToProxyd_CaRemove:
		got := reply.GetCaRemoved()
		if got.GetRule() != "" && len(got.GetDeleted()) != 0 {
			t.Fatalf("CaRemoved = %v: a refused batch deleted entries", got)
		}
		for _, d := range got.GetDeleted() {
			if !slices.Contains(k.CaRemove.GetItems(), d) {
				t.Fatalf("CaRemoved names %q, which the batch didn't", d)
			}
		}
	}
}
