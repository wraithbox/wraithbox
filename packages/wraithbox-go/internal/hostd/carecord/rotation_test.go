package carecord

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
)

// bootDay0 boots generation 1 on day 0 with an empty record: wb-proxyd
// issues the first CA.
func bootDay0(t *testing.T, r *rig) {
	t.Helper()
	r.boot(t, 1, ReasonBoot)
	r.wantOrder(t, "clock boot", "issue", "set a1 1", "install 1", "accept 1")
	r.wantRecord(t, "a1 signing installed 1")
	r.wantChanges(t, "CA issued ca-rotation a1", "CA installed ca-rotation a1", "CA signs ca-rotation a1")
}

// day30 runs day 30 on the running generation 1: the successor is
// issued, loaded with the current CA signing, and installed.
func day30(t *testing.T, r *rig) {
	t.Helper()
	r.at(29, 23*time.Hour)
	r.tick(t, 1)
	r.wantOrder(t)
	r.at(30, 0)
	r.tick(t, 1)
	r.wantOrder(t, "issue", "set a1 2", "install 2")
	r.wantRecord(t, "a1 signing installed 1, a2 installed 1")
	r.wantChanges(t, "CA issued ca-rotation a2", "CA installed ca-rotation a1 a2")
}

// TestC02RotationDay30: day 30 issues the successor and installs it
// next to the current CA, which signs on (C02, "CA rotation").
func TestC02RotationDay30(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	r.at(31, 0)
	r.tick(t, 1)
	r.wantOrder(t)
}

// TestC02RotationDay58: day 58 moves signing to the successor once its
// install is recorded (C02, "CA rotation").
func TestC02RotationDay58(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	r.at(57, 23*time.Hour)
	r.tick(t, 1)
	r.wantOrder(t)
	r.at(58, 0)
	r.tick(t, 1)
	r.wantOrder(t, "set a2 2")
	r.wantRecord(t, "a1 installed 1, a2 signing installed 1")
	r.wantChanges(t, "CA signing switched ca-rotation a2")
}

// TestC02RotationDay58NotInstalled: a successor whose install the guest
// refused doesn't sign on day 58. No CA does, with ca-not-installed,
// until a boot installs it (C02, "CA rotation").
func TestC02RotationDay58NotInstalled(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	r.guest.installErr = errors.New("trust file not written")
	r.at(30, 0)
	err := r.k.Tick(t.Context(), "work", 1, r.guest)
	wantRefusal(t, err, RuleInstallRefused, true)
	r.wantOrder(t, "issue", "set a1 2", "install 2")
	r.wantRecord(t, "a1 signing installed 1, a2")
	r.wantChanges(t, "CA issued ca-rotation a2", "CA install refused ca-install-refused a1 a2")

	r.at(58, 0)
	r.tick(t, 1)
	r.wantOrder(t, "set none:"+egressmsg.RuleCANotInstalled+" 2")
	r.wantRecord(t, "a1 installed 1, a2")
	r.wantChanges(t, "CA signing waits for the successor's install ca-not-installed a1 a2")
	r.at(58, time.Hour)
	r.tick(t, 1)
	r.wantOrder(t)

	r.guest.installErr = nil
	r.boot(t, 2, ReasonBoot)
	r.wantOrder(t, "clock boot", "set a2 2", "install 2", "accept 2")
	r.wantRecord(t, "a1 installed 2, a2 signing installed 2")
}

// TestC02RotationDay60: day 60 removes the old CA from the record and
// its key item, which wb-proxyd deletes once its CA set no longer has
// it. Day 60 of the old CA is day 30 of the successor, so the next one
// is issued (C02, "CA rotation").
func TestC02RotationDay60(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	r.at(58, 0)
	r.tick(t, 1)
	r.wantOrder(t, "set a2 2")
	r.wantChanges(t, "CA signing switched ca-rotation a2")

	r.at(59, 23*time.Hour)
	r.tick(t, 1)
	r.wantOrder(t)
	r.at(60, 0)
	r.tick(t, 1)
	r.wantOrder(t, "issue", "set a2 2", "remove 1", "install 2")
	r.wantRecord(t, "a2 signing installed 1, a3 installed 1")
	r.wantChanges(t, "CA removed ca-expired a1", "CA issued ca-rotation a3", "CA installed ca-rotation a2 a3")
	if got := r.removals(t); len(got) != 0 {
		t.Errorf("removal list = %q, want it empty", got)
	}
	if !slices.Equal(r.px.removed, []string{item("work", "a1"+zeros)}) {
		t.Errorf("wb-proxyd deleted %q", r.px.removed)
	}
}

// TestC02RotationBootOrder: a boot runs the seven steps in order, and a
// step that came due while the VM was off runs in step 3 (C02, "CA
// rotation").
func TestC02RotationBootOrder(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	r.at(35, 0)
	r.boot(t, 2, ReasonBoot)
	r.wantOrder(t, "clock boot", "issue", "set a1 2", "install 2", "accept 2")
	r.wantRecord(t, "a1 signing installed 2, a2 installed 2")
	r.wantChanges(t, "CA issued ca-rotation a2", "CA installed ca-rotation a1 a2")
	r.at(59, 0)
	r.boot(t, 3, ReasonBoot)
	r.wantOrder(t, "clock boot", "set a2 2", "install 2", "accept 3")
	r.wantRecord(t, "a1 installed 3, a2 signing installed 3")
}

// TestC02RotationRestoreAndWakeKeepSigner: past day 58, a restore and a
// wake keep the CA that signed, and only a boot switches (C02, "CA
// rotation").
func TestC02RotationRestoreAndWakeKeepSigner(t *testing.T) {
	for _, reason := range []Reason{ReasonRestore, ReasonWake} {
		t.Run(reason.String(), func(t *testing.T) {
			r := newRig(t)
			bootDay0(t, r)
			day30(t, r)
			r.at(59, 0)
			r.boot(t, 2, reason)
			r.wantOrder(t, "clock "+reason.String(), "set a1 2", "install 2", "accept 2")
			r.wantRecord(t, "a1 signing installed 2, a2 installed 2")
			r.at(59, time.Hour)
			r.tick(t, 2)
			r.wantOrder(t)
			r.boot(t, 3, ReasonBoot)
			r.wantOrder(t, "clock boot", "set a2 2", "install 2", "accept 3")
			r.wantRecord(t, "a1 installed 3, a2 signing installed 3")
		})
	}
}

// TestC02RotationSaveDay40RestoreDay45: a VM saved on day 40 and
// restored on day 45 signs with the old CA past day 58, and with the
// successor once the old CA ends (C02, "CA rotation").
func TestC02RotationSaveDay40RestoreDay45(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	r.at(40, 0)
	r.tick(t, 1)
	r.wantOrder(t)
	r.at(45, 0)
	r.boot(t, 2, ReasonRestore)
	r.wantOrder(t, "clock restore", "set a1 2", "install 2", "accept 2")
	r.at(58, 0)
	r.tick(t, 2)
	r.wantOrder(t)
	r.wantRecord(t, "a1 signing installed 2, a2 installed 2")
	r.wantChanges(t, "CA installed ca-rotation a1 a2")

	r.at(60, 0)
	r.tick(t, 2)
	r.wantOrder(t, "issue", "set a2 2", "remove 1", "install 2")
	r.wantRecord(t, "a2 signing installed 2, a3 installed 2")
}

// TestC02RotationSleepDay50To61: a host that sleeps from day 50 to day
// 61 removes the old CA before it sends the accept, and the successor
// signs (C02, "CA rotation").
func TestC02RotationSleepDay50To61(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	r.at(50, 0)
	r.tick(t, 1)
	r.wantOrder(t)
	r.at(61, 0)
	r.boot(t, 1, ReasonWake)
	r.wantOrder(t, "clock wake", "issue", "set a2 2", "remove 1", "install 2", "accept 1")
	r.wantRecord(t, "a2 signing installed 1, a3 installed 1")
	r.wantChanges(t, "CA removed ca-expired a1", "CA issued ca-rotation a3", "CA installed ca-rotation a2 a3",
		"CA signing switched ca-rotation a2")
}

// TestC02RotationInstallUnreported: an install the guest doesn't answer
// within the timeout is audited, and no accept follows: the VM runs with
// its inspected streams refused (C02, "CA rotation").
func TestC02RotationInstallUnreported(t *testing.T) {
	r := newRig(t)
	r.guest.block = true
	err := r.k.Boot(t.Context(), "work", 1, ReasonBoot, r.guest)
	wantRefusal(t, err, RuleInstallUnreported, true)
	r.wantOrder(t, "clock boot", "issue", "set a1 1", "install 1")
	r.wantRecord(t, "a1")
	r.wantChanges(t, "CA issued ca-rotation a1", "CA install not reported ca-install-unreported a1")
}

// TestDue walks the schedule day by day.
func TestDue(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	day30(t, r)
	cas, err := r.k.List(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		day                   int
		expired, live         int
		issue, switchToSecond bool
	}{
		{day: 0, live: 2},
		{day: 29, live: 2},
		{day: 30, live: 2},
		{day: 57, live: 2},
		{day: 58, live: 2, switchToSecond: true},
		{day: 59, live: 2, switchToSecond: true},
		{day: 60, expired: 1, live: 1, issue: true},
		{day: 90, expired: 2},
	} {
		p := Due(cas, start.Add(time.Duration(tc.day)*day))
		if len(p.Expired) != tc.expired || len(p.Live) != tc.live || p.IssueSuccessor != tc.issue || p.SwitchDue != tc.switchToSecond {
			t.Errorf("day %d: plan %d expired, %d live, issue %v, switch %v", tc.day, len(p.Expired), len(p.Live), p.IssueSuccessor, p.SwitchDue)
		}
	}
	if p := Due(cas[:1], start.Add(30*day)); !p.IssueSuccessor {
		t.Error("day 30 with one CA: no successor due")
	}
}

func wantRefusal(t *testing.T, err error, rule string, keepsRunning bool) *Refusal {
	t.Helper()
	ref, ok := errors.AsType[*Refusal](err)
	if !ok {
		t.Fatalf("error %v, want a refusal with %s", err, rule)
	}
	if ref.Rule != rule || ref.KeepsRunning != keepsRunning {
		t.Fatalf("refusal %s (keeps running %v), want %s (%v): %s", ref.Rule, ref.KeepsRunning, rule, keepsRunning, ref.Message)
	}
	return ref
}

// TestInstallRefusalLogsGuestText: the guest's text about a refused
// install is logged escaped and cut (proto/wraithbox/guest/v1,
// InstallTrust), and the refusal's message doesn't carry it.
func TestInstallRefusalLogsGuestText(t *testing.T) {
	r := newRig(t)
	bootDay0(t, r)
	text := "bad\x1b[0m trust\n" + strings.Repeat("x", 2*guestTextCut)
	r.guest.installErr = errors.New(text)
	r.at(30, 0)
	ref := wantRefusal(t, r.k.Tick(t.Context(), "work", 1, r.guest), RuleInstallRefused, true)
	if strings.Contains(ref.Message, "bad") {
		t.Errorf("refusal message %q carries the guest's text", ref.Message)
	}
	logs := r.logs.String()
	if !strings.Contains(logs, `bad\\x1b[0m trust\\x0a`) {
		t.Errorf("logs don't hold the escaped guest text:\n%s", logs)
	}
	if want := fmt.Sprintf("[cut from %d bytes]", len(text)); !strings.Contains(logs, want) {
		t.Errorf("logs don't hold %s:\n%s", want, logs)
	}
}
