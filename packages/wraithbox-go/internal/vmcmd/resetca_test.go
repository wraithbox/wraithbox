package vmcmd_test

import (
	"context"
	"strings"
	"testing"
	"time"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

// `wb vm reset-ca` (S05-03-management-commands, "`wb vm reset-ca`"),
// against the real service and CA record.

var (
	sha1st = strings.Repeat("1a", 32)
	sha2nd = strings.Repeat("2b", 32)
)

// seedCAs records two CAs of the work VM, the second signing.
func (f *fixture) seedCAs() {
	f.t.Helper()
	day0 := time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)
	err := f.db.CAs(f.t.Context(), func(tx *state.CATx) error {
		for i, sha := range []string{sha1st, sha2nd} {
			at := day0.Add(time.Duration(i) * 30 * 24 * time.Hour)
			ca := state.CA{
				ID: strings.Repeat(string(rune('a'+i)), 32), Slot: "work", Certificate: []byte("der"), SHA256: sha,
				Issued: at, NotBefore: at, NotAfter: at.Add(60 * 24 * time.Hour),
			}
			if err := tx.Insert(ca); err != nil {
				return err
			}
		}
		return tx.SetSigning("work", strings.Repeat("b", 32))
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// record returns the work VM's CA fingerprints and the removal list's
// length.
func (f *fixture) record() ([]string, int) {
	f.t.Helper()
	var shas []string
	var removals int
	err := f.db.CAs(f.t.Context(), func(tx *state.CATx) error {
		cas, err := tx.List("work")
		if err != nil {
			return err
		}
		for _, ca := range cas {
			shas = append(shas, ca.SHA256)
		}
		list, err := tx.Removals()
		removals = len(list)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return shas, removals
}

func (f *fixture) wantRecordKept() {
	f.t.Helper()
	if shas, removals := f.record(); len(shas) != 2 || removals != 0 {
		f.t.Errorf("record %v, %d removals", shas, removals)
	}
	if len(f.caAudit.Records()) != 0 {
		f.t.Errorf("audit records %v", f.caAudit.Messages())
	}
}

// TestResetCAAccepted: the question lists each CA, and after "yes" the
// record is empty, both items are on the removal list, and one 5019
// record names the command (SEC10-audit).
func TestResetCAAccepted(t *testing.T) {
	f := newFixture(t, "yes\n")
	f.seedCAs()
	if code := f.run("reset-ca", "work"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	asked := f.term.out.String()
	for _, want := range []string{"work VM's inspection CAs", sha1st, sha2nd + ", valid 2027-02-03 00:00 UTC to 2027-04-04 00:00 UTC, signing", "removal list", "Type yes"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the question misses %q:\n%s", want, asked)
		}
	}
	for _, want := range []string{sha1st, sha2nd, "cold boots"} {
		if !strings.Contains(f.stdout.String(), want) {
			t.Errorf("stdout misses %q: %s", want, f.stdout.String())
		}
	}
	if shas, removals := f.record(); len(shas) != 0 || removals != 2 {
		t.Errorf("record %v, %d removals", shas, removals)
	}
	if got := f.caAudit.Find("CA dropped by wb vm reset-ca work"); len(got) != 1 {
		t.Errorf("audit records %v", f.caAudit.Messages())
	}
}

// TestResetCARefusals: without a terminal, after a declined question,
// and while the VM runs, the record, the removal list and the audit log
// stay as they were (SEC14-no-fake-approvals).
func TestResetCARefusals(t *testing.T) {
	for _, tc := range []struct {
		name, typed, rule string
		noTerm, running   bool
	}{
		{name: "no terminal", rule: "confirm-no-terminal", noTerm: true},
		{name: "declined", typed: "no\n", rule: "confirm-declined"},
		{name: "running", typed: "yes\n", rule: string(vm.RuleRunning), running: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.typed)
			f.noTerm = tc.noTerm
			f.seedCAs()
			if tc.running {
				if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
					t.Fatal(err)
				}
			}
			if code := f.run("reset-ca", "work"); code != 255 {
				t.Errorf("exit %d, want 255", code)
			}
			if got := f.lastLine(); !strings.HasSuffix(got, "("+tc.rule+")") {
				t.Errorf("last line %q", got)
			}
			f.wantRecordKept()
		})
	}
}

// TestResetCANothingToReset: a VM without a CA isn't asked about.
func TestResetCANothingToReset(t *testing.T) {
	f := newFixture(t, "yes\n")
	if code := f.run("reset-ca", "isolated"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	if f.term.out.Len() != 0 || !strings.Contains(f.stdout.String(), "nothing changed") {
		t.Errorf("asked %q, stdout %q", f.term.out.String(), f.stdout.String())
	}
}

// listing is a wb-hostd that lists cas and fails every other call.
type listing struct {
	failing
	cas []*hostdv1.InspectionCA
}

func (l listing) ListCAs(context.Context, hostdv1.VM) ([]*hostdv1.InspectionCA, error) {
	return l.cas, nil
}

// TestResetCAListInvalid: a fingerprint that isn't SHA-256 in
// lowercase hexadecimal is neither shown nor confirmed.
func TestResetCAListInvalid(t *testing.T) {
	for _, sha := range []string{"", strings.ToUpper(sha1st), sha1st + "00", "\x1b[2J" + sha1st[4:]} {
		f := newFixture(t, "yes\n")
		env := f.env()
		h := listing{cas: []*hostdv1.InspectionCA{{Sha256: sha}}}
		env.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil }
		if code := vmcmd.Run(t.Context(), env, []string{"reset-ca", "work"}); code != 255 {
			t.Fatalf("%q: exit %d", sha, code)
		}
		if got := f.lastLine(); !strings.HasSuffix(got, "("+vmcmd.RuleCAListInvalid+")") || strings.Contains(got, "\x1b") {
			t.Errorf("%q: last line %q", sha, got)
		}
		if f.term.out.Len() != 0 {
			t.Errorf("%q: asked", sha)
		}
	}
}

// TestResetCAMayHaveRun: when the answer to the reset doesn't arrive,
// wb says the reset may have run.
func TestResetCAMayHaveRun(t *testing.T) {
	f := newFixture(t, "yes\n")
	env := f.env()
	h := listing{failing: failing{context.DeadlineExceeded}, cas: []*hostdv1.InspectionCA{{Sha256: sha1st}}}
	env.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil }
	if code := vmcmd.Run(t.Context(), env, []string{"reset-ca", "work"}); code != 255 {
		t.Fatalf("exit %d", code)
	}
	if got := f.lastLine(); !strings.HasSuffix(got, "(vm-reset-unknown)") || !strings.Contains(got, "wb vm reset-ca work") {
		t.Errorf("last line %q", got)
	}
}
