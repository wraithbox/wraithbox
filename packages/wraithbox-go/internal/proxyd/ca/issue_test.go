package ca

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

var oidBasicConstraints = asn1.ObjectIdentifier{2, 5, 29, 19}

// TestC02IssueProfile: an issue makes a CA of exactly the CA profile of
// S09-07-ca-certificates, and its item holds the key's wrapped form.
func TestC02IssueProfile(t *testing.T) {
	e := newEnv(t, team)
	cert, err := x509.ParseCertificate(e.issue("work", caA))
	if err != nil {
		t.Fatal(err)
	}
	checkCAProfile(t, cert)
	if want := "Wraith Box inspection CA work 01234567"; cert.Subject.CommonName != want || cert.Subject.String() != "CN="+want {
		t.Errorf("subject = %q, want CN=%s", cert.Subject, want)
	}
	if !cert.NotBefore.Equal(start.Add(-backdate)) || !cert.NotAfter.Equal(start.Add(60*24*time.Hour)) {
		t.Errorf("validity = %v to %v, want %v to 60 days after %v", cert.NotBefore, cert.NotAfter, start.Add(-backdate), start)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Errorf("not self-signed: %v", err)
	}
	// The fake's wrapped form is the key's PKCS #8 encoding, so the item
	// holds the CA's key.
	n, _ := storeitem.Parse(item("work", caA))
	value, err := e.a.store.Fetch(t.Context(), n, storeitem.RoleCA)
	if err != nil {
		t.Fatalf("the item: %v", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(value)
	if err != nil {
		t.Fatalf("the item isn't the fake's wrapped form: %v", err)
	}
	if priv, ok := key.(*ecdsa.PrivateKey); !ok || !priv.PublicKey.Equal(cert.PublicKey) {
		t.Error("the item's key isn't the CA's key")
	}
	if !strings.Contains(e.logs.String(), "sha256="+fingerprint(cert.Raw)) {
		t.Errorf("the issue isn't logged with the fingerprint:\n%s", e.logs)
	}
}

// checkCAProfile checks the CA profile: basicConstraints critical with
// CA:TRUE and pathLen:0, keyUsage keyCertSign only, no extended key
// usage, no name constraints, and a serial of at most 128 bits.
func checkCAProfile(t *testing.T, cert *x509.Certificate) {
	t.Helper()
	if !cert.BasicConstraintsValid || !cert.IsCA || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Errorf("basic constraints = valid %v, CA %v, pathLen %d (zero %v)", cert.BasicConstraintsValid, cert.IsCA, cert.MaxPathLen, cert.MaxPathLenZero)
	}
	critical := false
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidBasicConstraints) {
			critical = ext.Critical
		}
	}
	if !critical {
		t.Error("basicConstraints isn't critical")
	}
	if cert.KeyUsage != x509.KeyUsageCertSign {
		t.Errorf("key usage = %b, want keyCertSign only", cert.KeyUsage)
	}
	if len(cert.ExtKeyUsage) != 0 || len(cert.UnknownExtKeyUsage) != 0 {
		t.Errorf("extended key usage = %v %v, want none", cert.ExtKeyUsage, cert.UnknownExtKeyUsage)
	}
	if len(cert.PermittedDNSDomains)+len(cert.ExcludedDNSDomains)+len(cert.PermittedIPRanges)+len(cert.ExcludedIPRanges) != 0 {
		t.Error("the CA has name constraints")
	}
	if cert.SerialNumber.Sign() <= 0 || cert.SerialNumber.BitLen() > 128 {
		t.Errorf("serial = %v, want 1 to 128 bits", cert.SerialNumber)
	}
}

// TestC02IssueRoundTripFails: a failed read-back, a failed restore, and
// a restore of another key during an issue delete the item, report
// ca-key-restore, and leave no CA (C02, "CA key").
func TestC02IssueRoundTripFails(t *testing.T) {
	cases := []struct {
		name string
		fail func(e *env)
	}{
		{"read-back", func(e *env) {
			e.hooked.afterWrite = func() {
				e.backend.SetStatus(storeitem.AccessFetch, item("work", caA), secretstore.StatusMissingEntitlement)
			}
		}},
		{"restore", func(e *env) { e.keys.FailRestore(errors.New("no such key")) }},
		{"restore of another key", func(e *env) { e.keys.RestoreOther(true) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, team)
			c.fail(e)
			got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caA})
			if got.GetRule() != RuleKeyRestore || got.GetCertificate() != nil {
				t.Fatalf("Issue = rule %q, %d bytes, want %s", got.GetRule(), len(got.GetCertificate()), RuleKeyRestore)
			}
			if !deleted(e, item("work", caA)) {
				t.Errorf("the item wasn't deleted: %v", e.backend.Calls())
			}
			e.backend.SetStatus(storeitem.AccessFetch, item("work", caA), secretstore.StatusOK)
			if e.has(item("work", caA)) {
				t.Error("the item is still in the store")
			}
			if _, _, open := e.keys.Counts(); open != 0 {
				t.Errorf("%d keys left open", open)
			}
			// No CA is left: the same id issues again.
			e.keys.FailRestore(nil)
			e.keys.RestoreOther(false)
			e.hooked.afterWrite = nil
			e.issue("work", caA)
		})
	}
}

// deleted reports whether the fake store saw a delete of name.
func deleted(e *env, name string) bool {
	return slices.ContainsFunc(e.backend.Calls(), func(c storetest.Call) bool {
		return c.Access == storeitem.AccessDelete && c.Item == name
	})
}

// TestC02IssueRefused: an issue of a CA id that wb-proxyd holds, or that
// is a CA of a VM's last CA set, is refused before any store call.
func TestC02IssueRefused(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	calls := len(e.backend.Calls())
	for _, vm := range []string{"work", "isolated"} {
		got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: vm, CaId: caA})
		if got.GetRule() != RuleIssueRefused {
			t.Errorf("Issue(%s) of a held id = rule %q, want %s", vm, got.GetRule(), RuleIssueRefused)
		}
	}
	if n := len(e.backend.Calls()); n != calls {
		t.Errorf("a refused issue made %d store calls", n-calls)
	}
	if leaf, ref := e.a.For("work", 1, "example.com"); leaf == nil {
		t.Errorf("the live CA stopped signing after the refused issue: %v", ref)
	}

	// A CA set whose second CA doesn't restore still records both ids:
	// an issue of either, for either VM, is refused before any store
	// call.
	failRestoreOfB(e)
	calls = len(e.backend.Calls())
	for _, vm := range []string{"work", "isolated"} {
		for _, id := range []string{caA, caB} {
			if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: vm, CaId: id}); got.GetRule() != RuleIssueRefused {
				t.Errorf("Issue(%s, %s) after a failed restore = rule %q, want %s", vm, id, got.GetRule(), RuleIssueRefused)
			}
		}
	}
	if n := len(e.backend.Calls()); n != calls {
		t.Errorf("an issue of a recorded id made %d store calls", n-calls)
	}
}

// TestC02IssueGenerateFails: a key store that can't make a key fails the
// issue with ca-key-restore, and writes no item. The only store call is
// the check that the item is new.
func TestC02IssueGenerateFails(t *testing.T) {
	e := newEnv(t, team)
	e.keys.FailGenerate(errors.ErrUnsupported)
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caA}); got.GetRule() != RuleKeyRestore {
		t.Fatalf("Issue = rule %q, want %s", got.GetRule(), RuleKeyRestore)
	}
	want := []storetest.Call{{Access: storeitem.AccessFetch, Group: storeitem.Group(team, storeitem.RoleCA), Item: item("work", caA)}}
	if calls := e.backend.Calls(); !slices.Equal(calls, want) {
		t.Errorf("store calls = %v, want %v", calls, want)
	}
}

// TestC02IssueStored: after a restart, an issue for an id whose item the
// store holds is refused with ca-issue-refused, and the item stays. A
// store error on that check fails the issue closed, and a failed write
// deletes nothing (S09-06-ca-keys, "Issue").
func TestC02IssueStored(t *testing.T) {
	e := newEnv(t, team)
	e.issue("work", caA)
	e.restart()
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caA}); got.GetRule() != RuleIssueRefused {
		t.Errorf("Issue of a stored id = rule %q, want %s", got.GetRule(), RuleIssueRefused)
	}
	if !e.has(item("work", caA)) || deleted(e, item("work", caA)) {
		t.Error("a refused issue removed the stored item")
	}

	e.backend.SetStatus(storeitem.AccessFetch, item("work", caB), secretstore.StatusMissingEntitlement)
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caB}); got.GetRule() != RuleKeyRestore {
		t.Errorf("Issue with a failing check = rule %q, want %s", got.GetRule(), RuleKeyRestore)
	}
	e.backend.SetStatus(storeitem.AccessFetch, item("work", caB), secretstore.StatusOK)

	e.backend.SetStatus(storeitem.AccessWrite, item("work", caB), secretstore.StatusMissingEntitlement)
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caB}); got.GetRule() != RuleKeyRestore {
		t.Errorf("Issue with a failing write = rule %q, want %s", got.GetRule(), RuleKeyRestore)
	}
	if deleted(e, item("work", caB)) {
		t.Error("a failed write deleted the item")
	}
}

// TestC02IssueDeleteFails: when the delete of a failed issue's item
// fails too, it is logged with store-write-failed.
func TestC02IssueDeleteFails(t *testing.T) {
	e := newEnv(t, team)
	e.keys.FailRestore(errors.New("no such key"))
	e.backend.SetStatus(storeitem.AccessDelete, item("work", caA), secretstore.StatusMissingEntitlement)
	if got := e.a.Issue(t.Context(), &egressv1.CaIssue{Vm: "work", CaId: caA}); got.GetRule() != RuleKeyRestore {
		t.Fatalf("Issue = rule %q, want %s", got.GetRule(), RuleKeyRestore)
	}
	if !strings.Contains(e.logs.String(), "rule="+string(storeitem.RuleWriteFailed)) {
		t.Errorf("the failed delete isn't logged with %s:\n%s", storeitem.RuleWriteFailed, e.logs)
	}
}
