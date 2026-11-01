//go:build unix

package hostservice_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
)

// wantRecords waits until the audit records are want, in order. The end
// record comes after the shell's stream has ended.
func (f *shellFixture) wantRecords(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(f.audit.Messages(), want) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := f.audit.Messages(); !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

// TestShellAuditRefusesOpen: while the audit log can't take records,
// every shell is refused with the writer's rule before anything runs,
// and the refusal is logged with it (SEC10-audit; S09-11-audit, "Free
// space and failed writes").
func TestShellAuditRefusesOpen(t *testing.T) {
	for _, rule := range []string{audit.RuleFreeSpace, audit.RuleWriteFailed} {
		t.Run(rule, func(t *testing.T) {
			f := newShells(t, nil)
			f.audit.Refuse(rule, "the audit log can't take records")
			_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
			wantRefused(t, "open", err, codes.FailedPrecondition, rule)
			f.wantNothingRan(t, rule)
		})
	}
}

// TestShellAuditOpenNotWritten: a shell whose open record can't be
// written is refused with the writer's rule, starts nothing in the
// guest, and leaves no shell registered, so the next open goes ahead
// once records are written.
func TestShellAuditOpenNotWritten(t *testing.T) {
	f := newShells(t, nil)
	f.audit.RefuseWrites(audit.RuleWriteFailed, "the record wasn't written")
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "open", err, codes.FailedPrecondition, audit.RuleWriteFailed)
	f.wantNothingRan(t, audit.RuleWriteFailed)
	if n := len(f.reg.Sessions("work")); n != 0 {
		t.Errorf("%d shells left registered", n)
	}
	f.audit.RefuseWrites("", "")
	stream, opened, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	if err != nil {
		t.Fatalf("open after records are written again: %v", err)
	}
	f.wantStarted(t, opened)
	_ = stream.CloseSend()
}

// wantNothingRan checks that a refused shell started nothing in the
// guest, left no record, and logged its refusal with rule.
func (f *shellFixture) wantNothingRan(t *testing.T, rule string) {
	t.Helper()
	select {
	case s := <-f.starts:
		t.Errorf("the guest got a start: %v", s)
	default:
	}
	if got := f.audit.Messages(); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
	if !strings.Contains(f.logs.String(), `"rule":"`+rule+`"`) {
		t.Errorf("the refusal isn't logged with %s:\n%s", rule, f.logs.String())
	}
}

// TestShellNoAuditLog: without the audit writer every shell is refused.
func TestShellNoAuditLog(t *testing.T) {
	f := newShells(t, func(s *hostservice.ShellServer) { s.Audit = nil })
	_, _, err := f.open(t.Context(), t, &hostdv1.ShellOpen{ProjectId: f.projectID, Setup: pipedSetup()})
	wantRefused(t, "open without an audit log", err, codes.Internal, hostservice.RuleInternal)
}
