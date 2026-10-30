package procs_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// fakeClock moves only when a loop sleeps.
type fakeClock struct {
	now time.Time
	// step is added on each Sleep, on top of the wait itself.
	step time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) { c.now = c.now.Add(d + c.step) }

// fakeTable is a process table. A process that gets SIGKILL is gone,
// unless it is sticky, and one that gets SIGSTOP is stopped, unless it
// ignores it.
type fakeTable struct {
	mu          sync.Mutex
	procs       []gp.Proc
	sticky      map[int32]bool
	unstoppable map[int32]bool
	// respawn adds a new process of the UID for each of the first
	// respawn bootouts, as launchd starting a KeepAlive agent again does
	// before the bootout takes.
	respawn  int
	nextPID  int32
	bootouts []uint32
	signals  []string
	listErr  error
	ranges   [][2]uint32
}

func (f *fakeTable) ByUID(uid uint32) ([]gp.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []gp.Proc
	for _, p := range f.procs {
		if p.UID == uid || p.RUID == uid {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeTable) InRange(first, last uint32) ([]gp.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ranges = append(f.ranges, [2]uint32{first, last})
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []gp.Proc
	for _, p := range f.procs {
		if p.UID >= first && p.UID <= last || p.RUID >= first && p.RUID <= last {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeTable) Signal(p gp.Proc, sig gp.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, sig.String())
	for i, q := range f.procs {
		if q.Key() != p.Key() {
			continue
		}
		switch {
		case sig == gp.Kill && !f.sticky[q.PID]:
			f.procs = append(f.procs[:i], f.procs[i+1:]...)
		case sig == gp.Stop && !f.unstoppable[q.PID]:
			f.procs[i].State = gp.Stopped
		}
		return nil
	}
	return nil
}

func (f *fakeTable) Bootout(_ context.Context, uid uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootouts = append(f.bootouts, uid)
	if f.respawn > 0 {
		f.respawn--
		f.nextPID++
		f.procs = append(f.procs, gp.Proc{PID: f.nextPID, Start: 1, UID: uid, RUID: uid, Name: "agent", State: gp.Running})
	}
	return errors.New("no such domain")
}

func proc(pid int32, uid uint32, name string) gp.Proc {
	return gp.Proc{PID: pid, Start: int64(pid) * 7, UID: uid, RUID: uid, Name: name, State: gp.Running}
}

func newLoops(t *testing.T, table *fakeTable) (*procs.Loops, *fakeClock, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	return procs.New(table, clock, slog.New(slog.NewJSONHandler(&buf, nil))), clock, &buf
}

func TestMaxLeftIsTheWireCap(t *testing.T) {
	if procs.MaxLeft != guestlink.MaxProcessesLeft {
		t.Errorf("MaxLeft = %d, guestlink.MaxProcessesLeft = %d", procs.MaxLeft, guestlink.MaxProcessesLeft)
	}
}

func TestKillClean(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{
		proc(100, 10000, "zsh"), proc(101, 10000, "claude"), proc(102, 10001, "other"),
		// A setuid program run by the user: real UID 10000.
		{PID: 103, Start: 1, UID: 0, RUID: 10000, Name: "sudo"},
	}}
	l, _, log := newLoops(t, table)
	res := l.Kill(t.Context(), 10000)
	if !res.Clean() || res.Rounds != 1 {
		t.Errorf("Kill = %+v, want clean in one round", res)
	}
	if len(table.procs) != 1 || table.procs[0].PID != 102 {
		t.Errorf("left %+v, want only the other user's process", table.procs)
	}
	if len(table.bootouts) != 1 || table.bootouts[0] != 10000 {
		t.Errorf("bootouts %v", table.bootouts)
	}
	if !strings.Contains(log.String(), `"decision":"clean"`) {
		t.Errorf("log %s", log)
	}
}

// TestKillBootsOutWithNoProcess: a round runs, with its bootout, even
// when the first check finds nothing.
func TestKillBootsOutWithNoProcess(t *testing.T) {
	table := &fakeTable{}
	l, _, _ := newLoops(t, table)
	if res := l.Kill(t.Context(), 10000); !res.Clean() || res.Rounds != 1 || len(table.bootouts) != 1 {
		t.Errorf("Kill = %+v, bootouts %v", res, table.bootouts)
	}
}

// TestKillRespawn: an agent launchd starts again is killed in the next
// round, and every round boots out the domain.
func TestKillRespawn(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, respawn: 3, nextPID: 500}
	l, _, _ := newLoops(t, table)
	res := l.Kill(t.Context(), 10000)
	if !res.Clean() || res.Rounds != 4 || len(table.bootouts) != 4 {
		t.Errorf("Kill = %+v, bootouts %d", res, len(table.bootouts))
	}
}

// TestKillRoundBound: a process that SIGKILL doesn't remove ends the
// loop after MaxRounds, with the rule, the count and escaped names.
func TestKillRoundBound(t *testing.T) {
	var list []gp.Proc
	sticky := map[int32]bool{}
	for i := range int32(7) {
		list = append(list, proc(100+i, 10000, "evil\x1b[31m\n"+strings.Repeat("x", 100)))
		sticky[100+i] = true
	}
	table := &fakeTable{procs: list, sticky: sticky}
	l, _, log := newLoops(t, table)
	res := l.Kill(t.Context(), 10000)
	if res.Clean() || res.Left != 7 || res.Rounds != procs.MaxRounds || len(table.bootouts) != procs.MaxRounds {
		t.Errorf("Kill = %+v, bootouts %d", res, len(table.bootouts))
	}
	out := log.String()
	for _, want := range []string{procs.RuleKillIncomplete, `"count":7`, `"decision":"processes-left"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, `\u001b[31m`) && !strings.Contains(out, `\\x1b`) {
		t.Errorf("log holds an unescaped name: %s", out)
	}
	if got := strings.Count(out, `evil`); got != procs.MaxNames {
		t.Errorf("log names %d processes, want %d", got, procs.MaxNames)
	}
}

// TestKillTimeBound: rounds that take long end the loop at MaxTime.
func TestKillTimeBound(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, sticky: map[int32]bool{100: true}}
	l, clock, _ := newLoops(t, table)
	clock.step = 11 * time.Second
	res := l.Kill(t.Context(), 10000)
	if res.Clean() || res.Rounds != 3 {
		t.Errorf("Kill = %+v, want 3 rounds of 11 s", res)
	}
}

// TestKillCap: more processes left than MaxLeft report MaxLeft.
func TestKillCap(t *testing.T) {
	table := &fakeTable{sticky: map[int32]bool{}}
	for i := range int32(1500) {
		table.procs = append(table.procs, proc(100+i, 10000, "p"))
		table.sticky[100+i] = true
	}
	l, _, _ := newLoops(t, table)
	if res := l.Kill(t.Context(), 10000); res.Left != procs.MaxLeft {
		t.Errorf("Left = %d, want %d", res.Left, procs.MaxLeft)
	}
}

// TestKillListError: a table that can't be read fails closed, with
// MaxLeft processes left.
func TestKillListError(t *testing.T) {
	table := &fakeTable{listErr: errors.New("sysctl failed")}
	l, _, log := newLoops(t, table)
	res := l.Kill(t.Context(), 10000)
	if res.Left != procs.MaxLeft || res.Rounds != procs.MaxRounds {
		t.Errorf("Kill = %+v", res)
	}
	if !strings.Contains(log.String(), procs.RuleKillIncomplete) || !strings.Contains(log.String(), "sysctl failed") {
		t.Errorf("log %s", log)
	}
}

// TestKillCanceled: a kill whose ctx is done before the first round
// isn't clean, even with no process listed.
func TestKillCanceled(t *testing.T) {
	l, _, _ := newLoops(t, &fakeTable{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if res := l.Kill(ctx, 10000); res.Clean() || res.Rounds != 0 {
		t.Errorf("Kill = %+v", res)
	}
}

// TestKillZombie: a zombie has exited and doesn't count.
func TestKillZombie(t *testing.T) {
	z := proc(100, 10000, "z")
	z.State = gp.Zombie
	table := &fakeTable{procs: []gp.Proc{z}, sticky: map[int32]bool{100: true}}
	l, _, _ := newLoops(t, table)
	if res := l.Kill(t.Context(), 10000); !res.Clean() {
		t.Errorf("Kill = %+v", res)
	}
}

// TestStopAll: the stop loop stops every running process in the
// project range, leaves the others, never boots out, and records which
// it stopped.
func TestStopAll(t *testing.T) {
	table := stopTable()
	l, _, log := newLoops(t, table)
	res := l.StopAll(t.Context(), "link-drop")
	if res.Running != 0 || res.Stopped != 2 || res.AlreadyStopped != 1 || res.Rounds != 1 {
		t.Errorf("StopAll = %+v", res)
	}
	if len(table.bootouts) != 0 {
		t.Errorf("stop loop booted out %v", table.bootouts)
	}
	for _, p := range table.procs {
		want := gp.Running
		if projectuser.InRange(p.UID) {
			want = gp.Stopped
		}
		if p.State != want {
			t.Errorf("PID %d (UID %d) state %d, want %d", p.PID, p.UID, p.State, want)
		}
	}
	got := l.StoppedByUs()
	if len(got) != 2 || got[0].PID != 100 || got[1].PID != 101 {
		t.Errorf("StoppedByUs = %v", got)
	}
	if r := table.ranges[0]; r != [2]uint32{projectuser.FirstID, projectuser.LastID} {
		t.Errorf("range %v", r)
	}
	if !strings.Contains(log.String(), `"reason":"link-drop"`) {
		t.Errorf("log %s", log)
	}
}

// stopTable holds processes in the project range, one of them stopped
// already, and processes outside it: root, an admin, nobody, and the
// first UID past the range.
func stopTable() *fakeTable {
	pre := proc(103, 10002, "vim")
	pre.State = gp.Stopped
	return &fakeTable{procs: []gp.Proc{
		proc(100, 10000, "zsh"), proc(101, 10001, "claude"), pre,
		proc(1, 0, "launchd"), proc(200, 501, "admin"),
		proc(201, 4294967294, "nobody"), proc(202, projectuser.LastID+1, "high"),
	}}
}

// TestStopAllAgain: a second run keeps what the first stopped as its
// own, and forgets a process that is gone.
func TestStopAllAgain(t *testing.T) {
	table := stopTable()
	l, _, _ := newLoops(t, table)
	l.StopAll(t.Context(), "link-drop")
	table.procs = table.procs[1:]
	res := l.StopAll(t.Context(), "start")
	if res.Stopped != 1 || res.AlreadyStopped != 1 {
		t.Errorf("second StopAll = %+v", res)
	}
	if got := l.StoppedByUs(); len(got) != 1 || got[0].PID != 101 {
		t.Errorf("StoppedByUs = %v", got)
	}
}

// TestStopAllBound: a process that SIGSTOP doesn't stop ends the loop
// at MaxRounds with the rule.
func TestStopAllBound(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, unstoppable: map[int32]bool{100: true}}
	l, _, log := newLoops(t, table)
	res := l.StopAll(t.Context(), "link-drop")
	if res.Running != 1 || res.Rounds != procs.MaxRounds {
		t.Errorf("StopAll = %+v", res)
	}
	if !strings.Contains(log.String(), procs.RuleStopIncomplete) {
		t.Errorf("log %s", log)
	}

	table2 := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, unstoppable: map[int32]bool{100: true}}
	l2, clock, _ := newLoops(t, table2)
	clock.step = 11 * time.Second
	if res := l2.StopAll(t.Context(), "start"); res.Rounds != 3 {
		t.Errorf("StopAll with slow rounds = %+v, want 3", res)
	}
}

// TestStopAllListError fails closed: a table that can't be read counts
// as a process still running, and keeps what was stopped before.
func TestStopAllListError(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}}
	l, _, log := newLoops(t, table)
	l.StopAll(t.Context(), "link-drop")
	table.listErr = errors.New("sysctl failed")
	res := l.StopAll(t.Context(), "link-drop")
	if res.Running != 1 || res.Stopped != 1 {
		t.Errorf("StopAll = %+v", res)
	}
	if !strings.Contains(log.String(), procs.RuleStopIncomplete) {
		t.Errorf("log %s", log)
	}
}

func TestLabel(t *testing.T) {
	if got := procs.Label("a\x00b\"é"); got != `"a\x00b\"\u00e9"` {
		t.Errorf("Label = %s", got)
	}
	if got := procs.Label(strings.Repeat("x", 100)); len(got) != 66 {
		t.Errorf("Label of 100 bytes is %d long, want 64 and quotes", len(got))
	}
}
