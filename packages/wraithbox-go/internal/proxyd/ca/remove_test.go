package ca

import (
	"errors"
	"slices"
	"strings"
	"testing"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestC02RemovalRefused: the batches of C02, "CA removal list", each
// with one entry that fails its check next to a valid entry, are
// refused whole with ca-removal-refused, and nothing is deleted.
func TestC02RemovalRefused(t *testing.T) {
	cases := []struct {
		name, entry, check string
		setup              func(e *env)
	}{
		{"binding", "wraithbox/binding/x", CheckItemForm, nil},
		{"wbadmin build", "wraithbox/wbadmin/build/x", CheckItemForm, nil},
		{"no prefix", "ca/work/" + caB, CheckItemForm, nil},
		{"not a VM slot", item("other", caB), CheckItemForm, nil},
		{"not a CA id", item("work", "x"), CheckItemForm, nil},
		{"upper case CA id", item("work", strings.ToUpper(caB)), CheckItemForm, nil},
		{"a part outside the form", item("work", caB) + "/x", CheckItemForm, nil},
		{"a CA in the record", item("work", caA), CheckInRecord, nil},
		{"a CA in another VM's record", item("isolated", caA), CheckInRecord, nil},
		{"a CA in the record after a failed restore", item("work", caA), CheckInRecord, failRestoreOfB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkRefusedBatch(t, c.entry, c.check, c.setup) })
	}
}

// failRestoreOfB sends VM work a CA set of caA and caB in which caB's
// certificate is caA's, so caB doesn't restore and the set fails.
func failRestoreOfB(e *env) {
	e.t.Helper()
	derA := e.a.vms["work"].cas[caA].cert.Raw
	e.issue("work", caB)
	if reply := e.set("work", 2, caA, caA, derA, caB, derA); reply.GetCaRestoreFailed() == nil {
		e.t.Fatalf("SetCAs with caB's certificate wrong = %v, want CaRestoreFailed", reply)
	}
}

// checkRefusedBatch runs setup, if any, sends entry before and after a
// valid entry, and sees each batch refused with check and nothing
// deleted. Then it sees the valid entry alone deleted.
func checkRefusedBatch(t *testing.T, entry, check string, setup func(e *env)) {
	e := newEnv(t, team)
	e.ready()
	if setup != nil {
		setup(e)
	}
	e.issue("isolated", caC)
	valid := item("isolated", caC)
	for _, items := range [][]string{{entry, valid}, {valid, entry}} {
		got := e.a.Remove(t.Context(), &egressv1.CaRemove{Items: items})
		want := []*egressv1.CaRemovalRefusal{{Index: uint32(slices.Index(items, entry)), Check: check}}
		if got.GetRule() != RuleRemovalRefused || len(got.GetDeleted()) != 0 || !sameRefusals(got.GetRefused(), want) {
			t.Errorf("Remove(%q) = %v, want %s with %v", items, got, RuleRemovalRefused, want)
		}
	}
	if calls := deletes(e); len(calls) != 0 {
		t.Errorf("a refused batch deleted %v", calls)
	}
	if !e.has(valid) || !e.has(item("work", caA)) {
		t.Error("a refused batch removed an item")
	}
	if !strings.Contains(e.logs.String(), "check="+check+" rule=ca-removal-refused") {
		t.Errorf("the refused entry isn't logged:\n%s", e.logs)
	}
	// The next batch, without the refused entry, deletes the valid one.
	got := e.a.Remove(t.Context(), &egressv1.CaRemove{Items: []string{valid}})
	if got.GetRule() != "" || !slices.Equal(got.GetDeleted(), []string{valid}) {
		t.Errorf("Remove(valid) = %v", got)
	}
}

func sameRefusals(got, want []*egressv1.CaRemovalRefusal) bool {
	return slices.EqualFunc(got, want, func(a, b *egressv1.CaRemovalRefusal) bool {
		return a.GetIndex() == b.GetIndex() && a.GetCheck() == b.GetCheck()
	})
}

// deletes returns the deletes the fake store saw.
func deletes(e *env) []storetest.Call {
	var out []storetest.Call
	for _, c := range e.backend.Calls() {
		if c.Access == storeitem.AccessDelete {
			out = append(out, c)
		}
	}
	return out
}

// TestC02RemovalDeletes: a valid batch deletes each item and the key
// this process held of it. An item already gone counts as deleted, and
// an entry whose delete failed is left out, logged with
// store-write-failed.
func TestC02RemovalDeletes(t *testing.T) {
	e := newEnv(t, team)
	e.issue("work", caA)
	e.issue("work", caB)
	gone := item("isolated", caC)
	failing := item("work", caB)
	e.backend.SetStatus(storeitem.AccessDelete, failing, secretstore.StatusMissingEntitlement)
	got := e.a.Remove(t.Context(), &egressv1.CaRemove{Items: []string{item("work", caA), gone, failing}})
	if got.GetRule() != "" || !slices.Equal(got.GetDeleted(), []string{item("work", caA), gone}) {
		t.Fatalf("Remove = %v, want %s and %s deleted", got, item("work", caA), gone)
	}
	if e.has(item("work", caA)) {
		t.Error("the item is still in the store")
	}
	if _, _, open := e.keys.Counts(); open != 0 {
		t.Errorf("%d keys left open after the removal", open)
	}
	if !strings.Contains(e.logs.String(), "rule="+string(storeitem.RuleWriteFailed)) {
		t.Errorf("the failed delete isn't logged with %s:\n%s", storeitem.RuleWriteFailed, e.logs)
	}
}

// TestHandle: Handle runs each CA message and answers it, and refuses a
// message of another kind.
func TestHandle(t *testing.T) {
	e := newEnv(t, team)
	reply, err := e.a.Handle(t.Context(), &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaIssue{CaIssue: &egressv1.CaIssue{Vm: "work", CaId: caA}}})
	der := reply.GetCaIssued().GetCertificate()
	if err != nil || len(der) == 0 {
		t.Fatalf("Handle(CaIssue) = %v, %v", reply, err)
	}
	reply, err = e.a.Handle(t.Context(), &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaSet{CaSet: &egressv1.CaSet{
		Vm: "work", Generation: 1, SigningCaId: caA, Cas: []*egressv1.InspectionCa{{CaId: caA, Certificate: der}},
	}}})
	if err != nil || reply.GetCaLoaded() == nil {
		t.Fatalf("Handle(CaSet) = %v, %v", reply, err)
	}
	reply, err = e.a.Handle(t.Context(), &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaAccept{CaAccept: &egressv1.CaAccept{Vm: "work", Generation: 1}}})
	if err != nil || reply != nil {
		t.Fatalf("Handle(CaAccept) = %v, %v", reply, err)
	}
	reply, err = e.a.Handle(t.Context(), &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaRemove{CaRemove: &egressv1.CaRemove{Items: []string{item("work", caB)}}}})
	if err != nil || reply.GetCaRemoved() == nil {
		t.Fatalf("Handle(CaRemove) = %v, %v", reply, err)
	}
	reply, err = e.a.Handle(t.Context(), &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaAccept{CaAccept: &egressv1.CaAccept{Vm: "work", Generation: 2}}})
	if !errors.Is(err, ErrOutOfOrder) || reply != nil {
		t.Fatalf("Handle(CaAccept of another generation) = %v, %v", reply, err)
	}
	if _, err := e.a.Handle(t.Context(), &egressv1.HostdToProxyd{}); err == nil {
		t.Fatal("Handle of no kind didn't fail")
	}
}
