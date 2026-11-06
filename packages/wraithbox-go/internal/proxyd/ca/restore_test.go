package ca

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestC02RestoreFails: a restore from the wrapped form of another key,
// of a token of another key, from random bytes, from a missing item, and
// with a restore error each report ca-key-restore, and the VM gets no
// leaf. No new CA is issued in its place (S09-06-ca-keys, "Restore").
func TestC02RestoreFails(t *testing.T) {
	cases := []struct {
		name string
		fail func(t *testing.T, e *env)
	}{
		{"wrapped form of another key", func(t *testing.T, e *env) {
			other, err := e.keys.Generate(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			e.write(item("work", caA), other.Wrapped())
			other.Close()
		}},
		{"token of another key", func(_ *testing.T, e *env) { e.keys.RestoreOther(true) }},
		{"random bytes", func(t *testing.T, e *env) {
			b := make([]byte, 121)
			if _, err := rand.Read(b); err != nil {
				t.Fatal(err)
			}
			e.write(item("work", caA), b)
		}},
		{"missing item", func(t *testing.T, e *env) {
			n, _ := storeitem.Parse(item("work", caA))
			if err := e.a.store.Delete(t.Context(), n, storeitem.RoleCA); err != nil {
				t.Fatal(err)
			}
		}},
		{"restore error", func(_ *testing.T, e *env) { e.keys.FailRestore(errors.New("the key store failed")) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkRestoreFails(t, c.fail) })
	}
}

// checkRestoreFails issues a CA, restarts, runs fail, and sees the CA
// set fail with ca-key-restore, the VM get no leaf, and no key made or
// left open.
func checkRestoreFails(t *testing.T, fail func(t *testing.T, e *env)) {
	e := newEnv(t, team)
	der := e.issue("work", caA)
	e.restart()
	fail(t, e)
	generated, _, open := e.keys.Counts()
	reply := e.set("work", 1, caA, caA, der)
	failed := reply.GetCaRestoreFailed()
	if failed.GetRule() != RuleKeyRestore || failed.GetCaId() != caA || failed.GetVm() != "work" || failed.GetGeneration() != 1 {
		t.Fatalf("SetCAs = %v, want CaRestoreFailed %s for %s", reply, RuleKeyRestore, caA)
	}
	e.accept("work", 1)
	e.wantRefusal("work", 1, "example.com", RuleKeyRestore, "")
	if now, _, _ := e.keys.Counts(); now != generated {
		t.Errorf("a failed restore generated %d keys", now-generated)
	}
	if _, _, after := e.keys.Counts(); after != open {
		t.Errorf("%d keys left open after the failed restore", after-open)
	}
}

// TestC02RestoreOnce: a CA this process restored, or issued, isn't
// restored again by the next CA set.
func TestC02RestoreOnce(t *testing.T) {
	e := newEnv(t, team)
	der := e.issue("work", caA)
	e.loaded("work", 1, caA, caA, der)
	e.restart()
	e.loaded("work", 1, caA, caA, der)
	e.loaded("work", 2, caA, caA, der)
	if _, restored, _ := e.keys.Counts(); restored != 2 {
		t.Errorf("restores = %d, want 2: one for the issue, one after the restart", restored)
	}
}

// TestC02RestoreBadCertificate: a CA set whose certificate isn't a
// self-signed CA, or whose public key isn't the item's, doesn't load.
func TestC02RestoreBadCertificate(t *testing.T) {
	e := newEnv(t, team)
	derA := e.issue("work", caA)
	derB := e.issue("work", caB)
	e.restart()
	for name, der := range map[string][]byte{
		"another CA's certificate": derB,
		"not a certificate":        []byte("not DER"),
	} {
		reply := e.set("work", 1, caA, caA, der)
		if reply.GetCaRestoreFailed().GetRule() != RuleKeyRestore {
			t.Errorf("%s: SetCAs = %v, want %s", name, reply, RuleKeyRestore)
		}
	}
	e.loaded("work", 1, caA, caA, derA)
}

// TestC02StoreLockedWaits: -25308 during an issue and a restore waits,
// logs the wait once, and continues once the store unlocks
// (S15-03-access-rules, rule 7).
func TestC02StoreLockedWaits(t *testing.T) {
	e := newEnv(t, team)
	name := item("work", caA)
	e.backend.SetStatus(storeitem.AccessWrite, name, secretstore.StatusLocked)
	issued := make(chan *egressv1.CaIssued)
	go func() { issued <- e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caA}) }()
	e.waitLog("waits for the unlock")
	e.backend.SetStatus(storeitem.AccessWrite, name, secretstore.StatusOK)
	got := <-issued
	if got.GetRule() != "" {
		t.Fatalf("Issue after the unlock = rule %q", got.GetRule())
	}
	if n := strings.Count(e.logs.String(), "waits for the unlock"); n != 1 {
		t.Errorf("the wait is logged %d times, want once", n)
	}

	e.restart()
	e.backend.SetStatus(storeitem.AccessFetch, name, secretstore.StatusLocked)
	replies := make(chan *egressv1.ProxydToHostd)
	go func() {
		reply, _ := e.a.SetCAs(t.Context(), &egressv1.CaSet{
			Vm: "work", Generation: 1, SigningCaId: caA,
			Cas: []*egressv1.InspectionCa{{CaId: caA, Certificate: got.GetCertificate()}},
		})
		replies <- reply
	}()
	e.waitLog(`waits for the unlock" call=fetch`)
	e.backend.SetStatus(storeitem.AccessFetch, name, secretstore.StatusOK)
	if reply := <-replies; reply.GetCaLoaded() == nil {
		t.Fatalf("SetCAs after the unlock = %v, want CaLoaded", reply)
	}
}

// TestC02StoreLockedCancel: a wait for the unlock ends with the
// context, and the restore fails closed.
func TestC02StoreLockedCancel(t *testing.T) {
	e := newEnv(t, team)
	der := e.issue("work", caA)
	e.restart()
	e.backend.SetStatus(storeitem.AccessFetch, item("work", caA), secretstore.StatusLocked)
	ctx, cancel := context.WithCancel(t.Context())
	replies := make(chan *egressv1.ProxydToHostd)
	go func() {
		reply, _ := e.a.SetCAs(ctx, &egressv1.CaSet{
			Vm: "work", Generation: 1, SigningCaId: caA,
			Cas: []*egressv1.InspectionCa{{CaId: caA, Certificate: der}},
		})
		replies <- reply
	}()
	e.waitLog("waits for the unlock")
	cancel()
	if reply := <-replies; reply.GetCaRestoreFailed().GetRule() != RuleKeyRestore {
		t.Fatalf("SetCAs after the cancel = %v, want %s", reply, RuleKeyRestore)
	}
}

// TestC02AdHocStore: in an ad-hoc signed build the store refuses every
// call, and an issue and a restore fail with keychain-needs-team
// (S09-06-ca-keys, "Ad-hoc signed builds").
func TestC02AdHocStore(t *testing.T) {
	der := newEnv(t, team).issue("work", caA)
	e := newEnv(t, "")
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caB}); got.GetRule() != RuleNeedsTeam {
		t.Errorf("Issue = rule %q, want %s", got.GetRule(), RuleNeedsTeam)
	}
	reply := e.set("work", 1, caA, caA, der)
	if reply.GetCaRestoreFailed().GetRule() != RuleNeedsTeam {
		t.Errorf("SetCAs = %v, want %s", reply, RuleNeedsTeam)
	}
	e.accept("work", 1)
	e.wantRefusal("work", 1, "example.com", RuleNeedsTeam, "")
}

// TestC02AcceptGating: before the first accept of a generation, and
// after a CA set of a newer generation, the VM's streams are refused,
// and after the accept they get leaves. A CA set of an older
// generation, and an accept of another generation, end the channel.
func TestC02AcceptGating(t *testing.T) {
	e := newEnv(t, team)
	der := e.issue("work", caA)
	e.loaded("work", 1, caA, caA, der)
	e.wantRefusal("work", 1, "example.com", RuleLeafRefused, ReasonNotAccepted)
	if err := e.a.Accept(&egressv1.CaAccept{Vm: "work", Generation: 2}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("Accept of a later generation = %v, want ErrOutOfOrder", err)
	}
	if err := e.a.Accept(&egressv1.CaAccept{Vm: "isolated", Generation: 1}); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("Accept of a VM with no CA set = %v, want ErrOutOfOrder", err)
	}
	e.accept("work", 1)
	if leaf, ref := e.a.For(leafReq("work", 1, "example.com")); leaf == nil {
		t.Fatalf("For after the accept: %v", ref)
	}

	// The successor's CA set of the same generation keeps the VM
	// accepted (S09-07-ca-certificates, "Rotation", day 30).
	derB := e.issue("work", caB)
	e.loaded("work", 1, caA, caA, der, caB, derB)
	if leaf, ref := e.a.For(leafReq("work", 1, "example.com")); leaf == nil {
		t.Fatalf("For after a CA set of the same generation: %v", ref)
	}

	e.loaded("work", 2, caA, caA, der, caB, derB)
	e.wantRefusal("work", 2, "example.com", RuleLeafRefused, ReasonNotAccepted)
	e.wantRefusal("work", 1, "example.com", RuleLeafRefused, ReasonGeneration)
	e.accept("work", 2)
	if leaf, ref := e.a.For(leafReq("work", 2, "example.com")); leaf == nil {
		t.Fatalf("For after the second accept: %v", ref)
	}

	_, err := e.a.SetCAs(t.Context(), &egressv1.CaSet{
		Vm: "work", Generation: 1, SigningCaId: caA,
		Cas: []*egressv1.InspectionCa{{CaId: caA, Certificate: der}},
	})
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("SetCAs of an older generation = %v, want ErrOutOfOrder", err)
	}
	for _, msg := range []string{"CA accept refused", "CA set refused"} {
		if !strings.Contains(e.logs.String(), msg+`" vm=`) ||
			strings.Count(e.logs.String(), "rule="+RuleControlRefused+" decision=refuse") < 3 {
			t.Errorf("%q isn't logged with rule %s and decision=refuse:\n%s", msg, RuleControlRefused, e.logs)
		}
	}
	if leaf, ref := e.a.For(leafReq("work", 2, "example.com")); leaf == nil {
		t.Fatalf("For after the refused CA set: %v", ref)
	}
}
