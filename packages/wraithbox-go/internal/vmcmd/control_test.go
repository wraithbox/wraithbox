package vmcmd_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeLinks answers each slot's link state with fn, down when unset.
type fakeLinks struct {
	mu sync.Mutex
	fn func(vm.Slot) guestconn.Status
}

func (f *fakeLinks) Status(slot vm.Slot) guestconn.Status {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return guestconn.Status{Slot: slot, State: guestconn.StateDown}
	}
	return fn(slot)
}

func (f *fakeLinks) set(fn func(vm.Slot) guestconn.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fn = fn
}

// linkOfCurrentRun makes every link of the fixture's VMs state, for the
// VM's current generation.
func (f *fixture) linkOfCurrentRun(state guestconn.State, guestVersion string, r *guestconn.HelloRefusal) {
	f.links.set(func(slot vm.Slot) guestconn.Status {
		return guestconn.Status{
			Slot: slot, Generation: f.vms.Status(slot).Generation, State: state,
			GuestVersion: guestVersion, Refusal: r,
		}
	})
}

// runWith runs wb vm with env changed by change.
func (f *fixture) runWith(change func(*vmcmd.Env), args ...string) int {
	env := f.env()
	change(&env)
	return vmcmd.Run(f.t.Context(), env, args)
}

// failedOnce checks a 255 exit with one line on standard error, the
// `wraith box:` line with rule.
func (f *fixture) failedOnce(code int, rule string) string {
	f.t.Helper()
	if code != 255 {
		f.t.Fatalf("exit %d, want 255\n%s", code, f.stderr.String())
	}
	got := strings.TrimRight(f.stderr.String(), "\n")
	if strings.Contains(got, "\n") || !strings.HasPrefix(got, "wraith box: ") || !strings.HasSuffix(got, "("+rule+")") {
		f.t.Fatalf("standard error %q, want one wraith box line with %s", got, rule)
	}
	return got
}

func TestControlUsage(t *testing.T) {
	for _, args := range [][]string{
		{"start"},
		{"start", "build"},
		{"start", "work", "isolated"},
		{"start", "--force", "work"},
		{"stop"},
		{"stop", "Work"},
		{"stop", "work", "now"},
		{"suspend"},
		{"suspend", "maintenance"},
		{"status", "build"},
		{"status", "work", "isolated"},
	} {
		f := newFixture(t, "")
		if code := f.run(args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if f.dials != 0 {
			t.Errorf("%q: dialed", args)
		}
	}
}

// TestSuspendRefusedWithoutHostd: suspend needs warm start, and wb
// refuses it without a connection to wb-hostd.
func TestSuspendRefusedWithoutHostd(t *testing.T) {
	f := newFixture(t, "")
	line := f.failedOnce(f.run("suspend", "work"), vmcmd.RuleSuspendNeedsWarmStart)
	if !strings.Contains(line, "warm start") || f.dials != 0 {
		t.Errorf("line %q, dials %d", line, f.dials)
	}
}

func TestStartLinked(t *testing.T) {
	f := newFixture(t, "")
	f.linkOfCurrentRun(guestconn.StateLinked, "1.2.3", nil)
	if code := f.run("start", "isolated"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	if got := f.stdout.String(); got != "The isolated VM runs generation 1, and its guest is linked: wb-guestd version 1.2.3.\n" {
		t.Errorf("stdout %q", got)
	}
	if s := f.vms.Status(vm.SlotIsolated); s.Phase != vm.PhaseRunning {
		t.Errorf("isolated is %s", s.Phase)
	}
}

// TestStartGuestVersionEscaped: a guest version with escape sequences
// prints without them, and cut to 64 bytes (SEC11-root-gains-nothing).
func TestStartGuestVersionEscaped(t *testing.T) {
	f := newFixture(t, "")
	f.linkOfCurrentRun(guestconn.StateLinked, "\x1b[2J\x1b]0;owned\a1.2.3\u202e"+strings.Repeat("9", 100), nil)
	if code := f.run("start", "work"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	out := f.stdout.String()
	if strings.ContainsAny(out, "\x1b\a\u202e") || !strings.Contains(out, "[2J]0;owned1.2.3") {
		t.Errorf("stdout %q", out)
	}
	if strings.Contains(out, strings.Repeat("9", 65)) {
		t.Errorf("the version isn't cut: %q", out)
	}
}

// TestStartLinkRefused: a refused hello fails the start with the link's
// rule, and leaves the VM running.
func TestStartLinkRefused(t *testing.T) {
	f := newFixture(t, "")
	f.linkOfCurrentRun(guestconn.StateRefused, "", &guestconn.HelloRefusal{
		Rule: "peer-version-mismatch", OwnVersion: `"1.2.3"`, GuestVersion: `"1.2.4"`,
		Message: "wb-guestd in the work VM is version \"1.2.4\". Stop and start the VM.",
	})
	line := f.failedOnce(f.run("start", "work"), "peer-version-mismatch")
	for _, want := range []string{`wb-guestd version "1.2.4"`, `wb-hostd version "1.2.3"`, "left running", "Stop and start the VM"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q misses %q", line, want)
		}
	}
	if s := f.vms.Status(vm.SlotWork); s.Phase != vm.PhaseRunning {
		t.Errorf("work is %s", s.Phase)
	}
}

// TestStartLinkTimeout: a link that isn't up by the deadline fails the
// start, and the VM is left running for wb vm stop.
func TestStartLinkTimeout(t *testing.T) {
	f := newFixture(t, "")
	f.linkOfCurrentRun(guestconn.StateConnecting, "", nil)
	code := f.runWith(func(e *vmcmd.Env) { e.LinkTimeout = 300 * time.Millisecond }, "start", "work")
	line := f.failedOnce(code, vmcmd.RuleLinkTimeout)
	if !strings.Contains(line, "wb vm stop work") {
		t.Errorf("line %q", line)
	}
	if s := f.vms.Status(vm.SlotWork); s.Phase != vm.PhaseRunning {
		t.Errorf("work is %s", s.Phase)
	}
}

func TestStartRefused(t *testing.T) {
	f := newFixture(t, "")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	f.failedOnce(f.run("start", "work"), string(vm.RuleRunning))
}

// scripted is a wb-hostd that starts generation 4, and then answers
// every status with status.
type scripted struct {
	failing
	status *hostdv1.VMStatus
}

func (s scripted) StartVM(context.Context, hostdv1.VM) (*hostdv1.StartVMResponse, error) {
	return &hostdv1.StartVMResponse{Generation: 4}, nil
}

func (s scripted) GetVMStatus(context.Context, hostdv1.VM) (*hostdv1.VMStatus, error) {
	return s.status, nil
}

// TestStartStoppedBeforeLink: a run that ended before its link came up
// fails the start, with the framework error, escaped.
func TestStartStoppedBeforeLink(t *testing.T) {
	f := newFixture(t, "")
	h := scripted{status: &hostdv1.VMStatus{
		Phase: hostdv1.VMPhase_VM_PHASE_STOPPED, Generation: 4,
		LastError: &hostdv1.VMError{Code: 1, Description: "boot \x1b[2J failed"},
	}}
	code := f.runWith(func(e *vmcmd.Env) { e.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil } }, "start", "work")
	line := f.failedOnce(code, vmcmd.RuleStoppedUnexpectedly)
	if !strings.Contains(line, "generation 4") || !strings.Contains(line, "boot [2J failed (code 1)") {
		t.Errorf("line %q", line)
	}
}

// TestMayHaveRun: when the answer to a start or a stop doesn't arrive,
// wb says the VM may be starting or stopping.
func TestMayHaveRun(t *testing.T) {
	for _, tc := range []struct{ cmd, rule string }{
		{"start", vmcmd.RuleStartUnknown},
		{"stop", vmcmd.RuleStopUnknown},
	} {
		for _, err := range []error{status.Error(codes.Unavailable, "connection closed"), context.DeadlineExceeded} {
			f := newFixture(t, "")
			code := f.runWith(func(e *vmcmd.Env) {
				e.Dial = func(context.Context) (vmcmd.Hostd, error) { return failing{err}, nil }
			}, tc.cmd, "work")
			if line := f.failedOnce(code, tc.rule); !strings.Contains(line, "wb vm status work") {
				t.Errorf("%s %v: line %q", tc.cmd, err, line)
			}
		}
	}
}

func TestStop(t *testing.T) {
	f := newFixture(t, "")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if code := f.run("stop", "work"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	if f.stdout.String() != "The work VM is stopped.\n" || f.vms.Status(vm.SlotWork).Phase != vm.PhaseStopped {
		t.Errorf("stdout %q, phase %s", f.stdout.String(), f.vms.Status(vm.SlotWork).Phase)
	}
	g := newFixture(t, "")
	g.failedOnce(g.run("stop", "work"), string(vm.RuleNotRunning))
}

// TestStopLiveSessions: the refusal names each live session.
func TestStopLiveSessions(t *testing.T) {
	f := newFixture(t, "")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	f.live = []vm.Session{
		{ID: "a1", State: vm.SessionRunning, PID: 101},
		{ID: "b2", State: vm.SessionEnding, PID: 202},
	}
	line := f.failedOnce(f.run("stop", "work"), string(vm.RuleLiveSessions))
	for _, want := range []string{"a1", "101", "close its terminal", "b2", "202", "wait for its deadline"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q misses %q", line, want)
		}
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t, "")
	if err := f.vms.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	f.linkOfCurrentRun(guestconn.StateLinked, "1.2.3", nil)
	if code := f.run("status"); code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	want := "work VM: running, generation 1\n  guest link: linked, wb-guestd version 1.2.3\nisolated VM: stopped, generation 0\n"
	if got := f.stdout.String(); got != want {
		t.Errorf("stdout\n%s\nwant\n%s", got, want)
	}

	g := newFixture(t, "")
	if code := g.run("status", "isolated"); code != 0 || g.stdout.String() != "isolated VM: stopped, generation 0\n" {
		t.Errorf("exit %d, stdout %q", code, g.stdout.String())
	}
}

// TestStatusEscaped: every text in a status from wb-hostd prints
// without control and escape characters: the guest's version, the
// link's refusal and the framework error.
func TestStatusEscaped(t *testing.T) {
	f := newFixture(t, "")
	// A version of 64 bytes in version.Label's quoted form, 66 bytes: it
	// prints whole, with its closing quote.
	label := `"1.2.3-` + strings.Repeat("a", 58) + `"`
	h := scripted{status: &hostdv1.VMStatus{
		Phase: hostdv1.VMPhase_VM_PHASE_RUNNING, Generation: 2, LinkState: hostdv1.LinkState_LINK_STATE_REFUSED,
		LinkRefusal: &hostdv1.LinkRefusal{
			Rule: "peer\x1b[1m-version", GuestVersion: "\x1b]0;x\a\"1.2.4\"", HostdVersion: label,
			Message: "\x1b[2J stop\u202e and\u2028 start\u200d",
		},
		LastError: &hostdv1.VMError{Code: 2, Errno: 5, Description: "\x9b31m red", FailureReason: "\x07bell"},
	}}
	code := f.runWith(func(e *vmcmd.Env) { e.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil } }, "status", "work")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, f.stderr.String())
	}
	out := f.stdout.String()
	if strings.ContainsAny(out, "\x1b\a\u009b\u202e\u2028\u200d") {
		t.Errorf("stdout %q", out)
	}
	for _, want := range []string{`wb-guestd version ]0;x"1.2.4"`, "wb-hostd version " + label, "stop and start", "(code 2, errno 5)"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout %q misses %q", out, want)
		}
	}
}

// TestStatusDataMount: the data volume's state prints, and a refusal
// prints its rule and the fix without escapes.
func TestStatusDataMount(t *testing.T) {
	tests := []struct {
		dm   *hostdv1.DataMount
		want string
	}{
		{&hostdv1.DataMount{State: hostdv1.DataMountState_DATA_MOUNT_STATE_MOUNTED}, "  data volume: mounted\n"},
		{&hostdv1.DataMount{State: hostdv1.DataMountState_DATA_MOUNT_STATE_PENDING}, "  data volume: pending\n"},
		{
			&hostdv1.DataMount{
				State: hostdv1.DataMountState_DATA_MOUNT_STATE_REFUSED, Rule: "data-mount\x1b[1m-flags",
				Message: "\x1b[2J stop\u202e and reset",
			},
			"  data volume: refused, rule data-mount[1m-flags: [2J stop and reset\n",
		},
	}
	for _, tt := range tests {
		f := newFixture(t, "")
		h := scripted{status: &hostdv1.VMStatus{
			Phase: hostdv1.VMPhase_VM_PHASE_RUNNING, Generation: 1, LinkState: hostdv1.LinkState_LINK_STATE_LINKED,
			GuestVersion: "1.2.3", DataMount: tt.dm,
		}}
		code := f.runWith(func(e *vmcmd.Env) { e.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil } }, "status", "work")
		want := "work VM: running, generation 1\n  guest link: linked, wb-guestd version 1.2.3\n" + tt.want
		if code != 0 || f.stdout.String() != want {
			t.Errorf("exit %d, stdout %q, want %q", code, f.stdout.String(), want)
		}
	}
}

// TestStatusAuditRefusal: the audit log's refusal prints with its rule
// and wb-hostd's text without escapes, and with its end when it ends at
// a set time (S09-11-audit, "Free space and failed writes").
func TestStatusAuditRefusal(t *testing.T) {
	tests := []struct {
		r    *hostdv1.AuditRefusal
		want string
	}{
		{
			&hostdv1.AuditRefusal{Rule: "audit-free-space", Message: "free \x1b[2J space\u202e", Sessions: true},
			"  audit log: refusing starts and new sessions, rule audit-free-space: free [2J space\n",
		},
		{
			&hostdv1.AuditRefusal{Rule: "audit-write-failed", Message: "no writes", UntilUnix: 1_800_000_000, Sessions: true},
			"  audit log: refusing starts and new sessions, rule audit-write-failed: no writes, until 2027-01-15T08:00:00Z\n",
		},
		{
			&hostdv1.AuditRefusal{Rule: "audit-free-space", Message: "no reserve"},
			"  audit log: refusing VM starts, rule audit-free-space: no reserve\n",
		},
	}
	for _, tt := range tests {
		f := newFixture(t, "")
		h := scripted{status: &hostdv1.VMStatus{Phase: hostdv1.VMPhase_VM_PHASE_STOPPED, LinkState: hostdv1.LinkState_LINK_STATE_DOWN, AuditRefusal: tt.r}}
		code := f.runWith(func(e *vmcmd.Env) { e.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil } }, "status", "work")
		want := "work VM: stopped, generation 0\n" + tt.want
		if code != 0 || f.stdout.String() != want {
			t.Errorf("exit %d, stdout %q, want %q", code, f.stdout.String(), want)
		}
	}
}

// TestStatusAuditBudget: the audit budgets print with their use, and
// with the rule, without escapes, and its end while the VM is over one
// (S09-11-audit, "Budgets").
func TestStatusAuditBudget(t *testing.T) {
	tests := []struct {
		b    *hostdv1.AuditBudget
		want string
	}{
		{
			&hostdv1.AuditBudget{EventsThisMinute: 12, EventsPerMinute: 6000, BytesToday: 3 << 19, BytesPerDay: 64 << 20},
			"  audit budgets: 12 of 6000 events this minute, 1.5 of 64.0 MiB today\n",
		},
		{
			&hostdv1.AuditBudget{
				Rule: "audit-budget\x1b[2J", UntilUnix: 1_800_000_000, EventsThisMinute: 6000, EventsPerMinute: 6000, BytesPerDay: 64 << 20,
			},
			"  audit budgets: 6000 of 6000 events this minute, 0.0 of 64.0 MiB today, over, rule audit-budget[2J, until 2027-01-15T08:00:00Z\n",
		},
	}
	for _, tt := range tests {
		f := newFixture(t, "")
		h := scripted{status: &hostdv1.VMStatus{Phase: hostdv1.VMPhase_VM_PHASE_STOPPED, LinkState: hostdv1.LinkState_LINK_STATE_DOWN, AuditBudget: tt.b}}
		code := f.runWith(func(e *vmcmd.Env) { e.Dial = func(context.Context) (vmcmd.Hostd, error) { return h, nil } }, "status", "work")
		want := "work VM: stopped, generation 0\n" + tt.want
		if code != 0 || f.stdout.String() != want {
			t.Errorf("exit %d, stdout %q, want %q", code, f.stdout.String(), want)
		}
	}
}

// TestStatusFailure: a failed status call prints one wraith box line
// with its rule, without escapes.
func TestStatusFailure(t *testing.T) {
	f := newFixture(t, "")
	code := f.runWith(func(e *vmcmd.Env) {
		e.Dial = func(context.Context) (vmcmd.Hostd, error) { return newEvil(), nil }
	}, "status")
	f.failedOnce(code, vmcmd.RuleUnreachable)
}
