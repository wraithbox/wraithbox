package hostservice_test

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/carecord"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// `wb vm reset-ca`'s calls (S05-03-management-commands, "`wb vm
// reset-ca`").

// fakeCAs holds a record per slot and answers Reset with err when set.
type fakeCAs struct {
	mu     sync.Mutex
	cas    map[string][]state.CA
	err    error
	resets []string
}

func (f *fakeCAs) List(_ context.Context, slot string) ([]state.CA, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cas[slot], nil
}

func (f *fakeCAs) Reset(_ context.Context, slot string, confirmed []string, command string) ([]state.CA, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets = append(f.resets, slot+" "+strings.Join(confirmed, ",")+" "+command)
	if f.err != nil {
		return nil, f.err
	}
	cas := f.cas[slot]
	delete(f.cas, slot)
	return cas, nil
}

func (f *fakeCAs) Resets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.resets)
}

var (
	day0 = time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)
	ca1  = state.CA{SHA256: strings.Repeat("1", 64), NotBefore: day0, NotAfter: day0.Add(60 * 24 * time.Hour)}
	ca2  = state.CA{SHA256: strings.Repeat("2", 64), NotBefore: day0.Add(30 * 24 * time.Hour), NotAfter: day0.Add(90 * 24 * time.Hour), Signing: true}
)

func TestListCAs(t *testing.T) {
	cas := &fakeCAs{cas: map[string][]state.CA{"work": {ca1, ca2}}}
	res, err := serve(t, &hostservice.Server{VMs: &fakeVMs{}, CAs: cas}).ListCAs(t.Context(), &hostdv1.ListCAsRequest{Vm: hostdv1.VM_VM_WORK})
	if err != nil {
		t.Fatal(err)
	}
	got := res.GetCas()
	if len(got) != 2 || got[0].GetSha256() != ca1.SHA256 || got[0].GetSigning() || got[1].GetSha256() != ca2.SHA256 || !got[1].GetSigning() {
		t.Fatalf("CAs %v", got)
	}
	if got[0].GetNotBeforeUnix() != day0.Unix() || got[1].GetNotAfterUnix() != ca2.NotAfter.Unix() {
		t.Errorf("dates %v", got)
	}
}

// TestResetCA: refused with vm-running while the VM runs, without a
// call to the CA record. Stopped, the record gets the confirmed
// fingerprints and the command, and the dropped CAs come back.
func TestResetCA(t *testing.T) {
	m := newManager(t, slog.New(slog.DiscardHandler), nil)
	cas := &fakeCAs{cas: map[string][]state.CA{"work": {ca1, ca2}}}
	c := serve(t, &hostservice.Server{VMs: m, CAs: cas, Log: slog.New(slog.DiscardHandler)})
	req := &hostdv1.ResetCARequest{Vm: hostdv1.VM_VM_WORK, Sha256: []string{ca1.SHA256, ca2.SHA256}}
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	_, err := c.ResetCA(t.Context(), req)
	if code, rule, msg := ruleOf(t, err); code != codes.FailedPrecondition || rule != string(vm.RuleRunning) ||
		!strings.Contains(msg, "inspection CA") {
		t.Errorf("running: %s %s %q", code, rule, msg)
	}
	if len(cas.Resets()) != 0 {
		t.Fatalf("the record was reset while the VM runs: %v", cas.Resets())
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	res, err := c.ResetCA(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.GetDroppedSha256(), req.GetSha256()) {
		t.Errorf("dropped %v", res.GetDroppedSha256())
	}
	want := "work " + ca1.SHA256 + "," + ca2.SHA256 + " wb vm reset-ca work"
	if got := cas.Resets(); len(got) != 1 || got[0] != want {
		t.Errorf("resets %v, want %s", got, want)
	}
}

// TestResetCARefusals: the CA record's refusal arrives with its rule,
// and a wb-hostd without a CA record refuses both calls.
func TestResetCARefusals(t *testing.T) {
	cas := &fakeCAs{err: &carecord.Refusal{Rule: carecord.RuleRecordChanged, Message: "changed"}}
	_, err := serve(t, &hostservice.Server{VMs: &fakeVMs{}, CAs: cas}).ResetCA(t.Context(), &hostdv1.ResetCARequest{Vm: hostdv1.VM_VM_WORK})
	if code, rule, _ := ruleOf(t, err); code != codes.FailedPrecondition || rule != carecord.RuleRecordChanged {
		t.Errorf("record changed: %s %s", code, rule)
	}
	c := serve(t, &hostservice.Server{VMs: &fakeVMs{}, Log: slog.New(slog.DiscardHandler)})
	_, err = c.ResetCA(t.Context(), &hostdv1.ResetCARequest{Vm: hostdv1.VM_VM_WORK})
	if _, rule, _ := ruleOf(t, err); rule != hostservice.RuleCAUnavailable {
		t.Errorf("ResetCA without a record: %s", rule)
	}
	_, err = c.ListCAs(t.Context(), &hostdv1.ListCAsRequest{Vm: hostdv1.VM_VM_WORK})
	if _, rule, _ := ruleOf(t, err); rule != hostservice.RuleCAUnavailable {
		t.Errorf("ListCAs without a record: %s", rule)
	}
}
