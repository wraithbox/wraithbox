package carecord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

const resetCommand = "wb vm reset-ca work"

func fingerprints(t *testing.T, r *rig) []string {
	t.Helper()
	cas, err := r.k.List(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ca := range cas {
		out = append(out, ca.SHA256)
	}
	return out
}

// TestC02CAKeyRestoreFailed: a CA whose key doesn't restore refuses the
// start with ca-key-restore, names `wb vm reset-ca`, and issues
// nothing. After the reset the next boot issues a new CA, and wb-proxyd
// deletes the old item once its CA set no longer has it (C02, "CA
// key").
func TestC02CAKeyRestoreFailed(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	r.px.restoreFail = "a1" + zeros
	r.at(1, 0)
	err := r.k.Boot(t.Context(), "work", 2, ReasonBoot, r.guest)
	ref := wantRefusal(t, err, egressmsg.RuleCAKeyRestore, false)
	if !strings.Contains(ref.Message, "wb vm reset-ca work") {
		t.Errorf("message %q doesn't name wb vm reset-ca", ref.Message)
	}
	r.wantOrder(t, "clock boot", "set a1 1")
	r.wantRecord(t, "a1 signing installed 1")

	sums := fingerprints(t, r)
	dropped, err := r.k.Reset(t.Context(), "work", sums, resetCommand)
	if err != nil || len(dropped) != 1 {
		t.Fatalf("Reset = %d CAs, %v", len(dropped), err)
	}
	r.wantOrder(t)
	r.wantRecord(t, "")
	if got := r.removals(t); !slices.Equal(got, []string{"a1 reset-ca"}) {
		t.Errorf("removal list = %q", got)
	}
	wantResetRecord(t, r, sums)

	r.px.restoreFail = ""
	r.boot(t, 3, ReasonBoot)
	r.wantOrder(t, "clock boot", "issue", "set a2 1", "remove 1", "install 1", "accept 3")
	r.wantRecord(t, "a2 signing installed 3")
	if got := r.removals(t); len(got) != 0 {
		t.Errorf("removal list = %q, want it empty", got)
	}
}

// wantResetRecord checks the 5019 record of the reset: each
// fingerprint, the command, written before the drop committed.
func wantResetRecord(t *testing.T, r *rig, sums []string) {
	t.Helper()
	recs := r.audit.Records()
	m := audittest.AsMap(recs[len(recs)-1].Event)
	u, _ := m["unmapped"].(map[string]any)
	if m["class_uid"] != float64(5019) || u["command"] != resetCommand || u["rule"] != RuleReset {
		t.Fatalf("last record = %v", m)
	}
	cas, _ := u["cas"].([]any)
	var got []string
	for _, ca := range cas {
		got = append(got, fmt.Sprint(lookup(ca, "sha256")))
	}
	if !slices.Equal(got, sums) {
		t.Errorf("record fingerprints = %q, want %q", got, sums)
	}
}

// TestResetRefused: a reset whose confirmed CAs aren't the record's, or
// whose 5019 record the writer refuses, drops nothing.
func TestResetRefused(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	_, err := r.k.Reset(t.Context(), "work", []string{strings.Repeat("ab", 32)}, resetCommand)
	wantRefusal(t, err, RuleRecordChanged, false)
	_, err = r.k.Reset(t.Context(), "work", nil, resetCommand)
	wantRefusal(t, err, RuleRecordChanged, false)
	if want := `"msg":"CA reset refused","vm":"work","decision":"refuse","rule":"` + RuleRecordChanged; !strings.Contains(r.logs.String(), want) {
		t.Errorf("logs don't hold %s:\n%s", want, r.logs.String())
	}

	r.audit.RefuseWrites("audit-write", "the audit log can't be written")
	if _, err = r.k.Reset(t.Context(), "work", fingerprints(t, r), resetCommand); err == nil {
		t.Error("reset with the audit writer refusing: no error")
	}
	r.wantRecord(t, "a1 signing installed 1")
	if got := r.removals(t); len(got) != 0 {
		t.Errorf("removal list = %q", got)
	}

	dropped, err := r.k.Reset(t.Context(), "isolated", nil, resetCommand)
	if err != nil || len(dropped) != 0 {
		t.Errorf("reset of an empty record = %v, %v", dropped, err)
	}
}

// TestResetDeletesSavedState: the reset calls the saved state hook after
// the drop.
func TestResetDeletesSavedState(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	var deleted []string
	r.k.cfg.DeleteSavedState = func(_ context.Context, slot string) error {
		deleted = append(deleted, r.summary(t, slot)+"|"+slot)
		return nil
	}
	if _, err := r.k.Reset(t.Context(), "work", fingerprints(t, r), resetCommand); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deleted, []string{"|work"}) {
		t.Errorf("saved state hook calls = %q, want one after the drop", deleted)
	}
}

// addRemovals puts n entries on the removal list, CA ids b0… on, one
// second apart.
func addRemovals(t *testing.T, r *rig, n int) []string {
	t.Helper()
	var items []string
	err := r.db.CAs(t.Context(), func(tx *state.CATx) error {
		for i := range n {
			it := item("isolated", fmt.Sprintf("b%02d%s", i, strings.Repeat("0", 29)))
			items = append(items, it)
			if err := tx.AddRemoval(it, state.RemovalExpired, start.Add(time.Duration(i)*time.Second)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// TestC02CARemovalList: deleted entries leave the list with their store
// access record, a refused entry leaves it logged, the rest go in the
// next batch, and a batch holds at most 64 entries (C02, "CA removal
// list").
func TestC02CARemovalList(t *testing.T) {
	r := newRig(t)
	items := addRemovals(t, r, 3)
	r.px.refuse = map[string]string{items[1]: egressmsg.CheckCAItemForm}
	r.k.ProxydStarted(t.Context())
	r.wantOrder(t, "remove 3", "remove 2")
	if want := []string{items[0], items[2]}; !slices.Equal(r.px.removed, want) {
		t.Errorf("deleted %q, want %q", r.px.removed, want)
	}
	if got := r.removals(t); len(got) != 0 {
		t.Errorf("removal list = %q, want it empty", got)
	}
	wantDeleteRecords(t, r, items[0], items[2])

	r.px.refuse = nil
	addRemovals(t, r, 70)
	r.k.ProxydStarted(t.Context())
	r.wantOrder(t, "remove 64", "remove 6")
	if got := r.removals(t); len(got) != 0 {
		t.Errorf("removal list = %q, want it empty", got)
	}
}

func wantDeleteRecords(t *testing.T, r *rig, want ...string) {
	t.Helper()
	var got []string
	for _, rec := range r.audit.Records() {
		m := audittest.AsMap(rec.Event)
		if m["class_uid"] == float64(3004) && m["activity_id"] == float64(4) {
			got = append(got, fmt.Sprint(lookup(m, "entity", "name")))
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("store delete records = %q, want %q", got, want)
	}
}

// TestCARemovalReplyInvalid: a CaRemoved that names an entry not sent,
// or answers with another kind, ends wb-proxyd and drops no entry.
func TestCARemovalReplyInvalid(t *testing.T) {
	for name, reply := range map[string]*egressv1.ProxydToHostd{
		"entry not sent": {Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: &egressv1.CaRemoved{
			Deleted: []string{item("work", "c0"+zeros)},
		}}},
		"index past the batch": {Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: &egressv1.CaRemoved{
			Rule: egressmsg.RuleCARemovalRefused, Refused: []*egressv1.CaRemovalRefusal{{Index: 3, Check: egressmsg.CheckCAInRecord}},
		}}},
		"another kind": {Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{Vm: "work", Generation: 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			addRemovals(t, r, 2)
			r.px.reply = func(*egressv1.HostdToProxyd) []byte { return mustMarshal(t, reply) }
			r.k.ProxydStarted(t.Context())
			if got := r.ender.ended(); !slices.Equal(got, []string{RuleReplyInvalid}) {
				t.Errorf("ended = %q", got)
			}
			if got := r.removals(t); len(got) != 2 {
				t.Errorf("removal list = %q, want both entries", got)
			}
		})
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestIssueFailed: a failed issue refuses the boot with wb-proxyd's
// rule, and its item goes on the removal list and out to wb-proxyd,
// unless wb-proxyd refused the id as one in use. An invalid reply ends
// wb-proxyd, and its item waits on the list for the next one.
func TestIssueFailed(t *testing.T) {
	for _, tc := range []struct {
		rule, reply string
		calls       []string
		removals    []string
	}{
		{rule: egressmsg.RuleCAKeyRestore, calls: []string{"clock boot", "issue", "remove 1"}},
		{rule: egressmsg.RuleCANeedsTeam, calls: []string{"clock boot", "issue", "remove 1"}},
		{rule: egressmsg.RuleCAIssueRefused, calls: []string{"clock boot", "issue"}},
		{rule: RuleReplyInvalid, reply: "expired certificate", calls: []string{"clock boot", "issue"}, removals: []string{"a1 failed-issue"}},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			r := newRig(t)
			r.px.issueRule = tc.rule
			if tc.reply != "" {
				r.px.issueRule = ""
				r.px.reply = func(m *egressv1.HostdToProxyd) []byte {
					return mustMarshal(t, issuedReply(m, newCA(start.Add(-61*day), nil)))
				}
			}
			err := r.k.Boot(t.Context(), "work", 1, ReasonBoot, r.guest)
			wantRefusal(t, err, tc.rule, false)
			r.wantOrder(t, tc.calls...)
			r.wantRecord(t, "")
			if got := r.removals(t); !slices.Equal(got, tc.removals) {
				t.Errorf("removal list = %q, want %q", got, tc.removals)
			}
			if ended := r.ender.ended(); (tc.reply != "") != (len(ended) == 1) {
				t.Errorf("ended = %q", ended)
			}
		})
	}
}

func issuedReply(m *egressv1.HostdToProxyd, cert []byte) *egressv1.ProxydToHostd {
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: &egressv1.CaIssued{
		Vm: m.GetCaIssue().GetVm(), CaId: m.GetCaIssue().GetCaId(), Certificate: cert,
	}}}
}

// TestLoadReplyInvalid: a CaSet answered for another generation or CA,
// or with another kind, ends wb-proxyd and refuses the start.
func TestLoadReplyInvalid(t *testing.T) {
	for name, reply := range map[string]func(*egressv1.CaSet) *egressv1.ProxydToHostd{
		"other generation": func(s *egressv1.CaSet) *egressv1.ProxydToHostd {
			return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{Vm: s.GetVm(), Generation: 9}}}
		},
		"restore failed of a CA not in the set": func(s *egressv1.CaSet) *egressv1.ProxydToHostd {
			return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRestoreFailed{CaRestoreFailed: &egressv1.CaRestoreFailed{
				Vm: s.GetVm(), Generation: s.GetGeneration(), CaId: "c0" + zeros, Rule: egressmsg.RuleCAKeyRestore,
			}}}
		},
		"another kind": func(*egressv1.CaSet) *egressv1.ProxydToHostd {
			return issuedReply(&egressv1.HostdToProxyd{}, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			bootDay0(t, r)
			r.px.reply = func(m *egressv1.HostdToProxyd) []byte { return mustMarshal(t, reply(m.GetCaSet())) }
			err := r.k.Boot(t.Context(), "work", 2, ReasonBoot, r.guest)
			wantRefusal(t, err, RuleReplyInvalid, false)
			r.wantOrder(t, "clock boot", "set a1 1")
			if got := r.ender.ended(); !slices.Equal(got, []string{RuleReplyInvalid}) {
				t.Errorf("ended = %q", got)
			}
		})
	}
}

// TestBootGuestClock: a guest clock that isn't set refuses the start
// before anything else runs.
func TestBootGuestClock(t *testing.T) {
	r := newRig(t)
	err := r.k.Boot(t.Context(), "work", 1, ReasonBoot, noClockGuest{})
	wantRefusal(t, err, RuleGuestClock, false)
	r.wantOrder(t)
	r.wantRecord(t, "")
}

type noClockGuest struct{}

func (noClockGuest) SetClock(context.Context, Reason) error { return errors.New("no guest link") }

func (noClockGuest) InstallTrust(context.Context, [][]byte) error { return nil }
