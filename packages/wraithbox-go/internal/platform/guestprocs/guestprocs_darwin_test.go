package guestprocs_test

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// find returns the process with pid in list.
func find(list []guestprocs.Proc, pid int) (guestprocs.Proc, bool) {
	for _, p := range list {
		if int(p.PID) == pid {
			return p, true
		}
	}
	return guestprocs.Proc{}, false
}

// TestListsOwnProcess: the test's own process is in both lists of its
// UID, with its IDs, its start time and its name.
func TestListsOwnProcess(t *testing.T) {
	s := &guestprocs.System{}
	uid := uint32(os.Getuid())
	byUID, err := s.ByUID(uid)
	if err != nil {
		t.Fatal(err)
	}
	inRange, err := s.InRange(uid, uid)
	if err != nil {
		t.Fatal(err)
	}
	for name, list := range map[string][]guestprocs.Proc{"ByUID": byUID, "InRange": inRange} {
		p, ok := find(list, os.Getpid())
		if !ok {
			t.Fatalf("%s: own PID %d not listed", name, os.Getpid())
		}
		if p.UID != uid || p.RUID != uid || p.Start <= 0 || p.Name == "" || p.State != guestprocs.Running {
			t.Errorf("%s: own process %+v", name, p)
		}
	}
	if other, err := s.InRange(uid+1, uid+1); err != nil || len(other) > 0 && other[0].UID == uid {
		t.Errorf("InRange of another UID: %v, %v", other, err)
	}
}

// waitState waits up to 5 s for the process with pid to reach state,
// or to be gone when gone is set.
func waitState(t *testing.T, s *guestprocs.System, pid int, state guestprocs.State, gone bool) {
	t.Helper()
	for range 100 {
		list, err := s.ByUID(uint32(os.Getuid()))
		if err != nil {
			t.Fatal(err)
		}
		p, ok := find(list, pid)
		if gone && (!ok || p.State == guestprocs.Zombie) || !gone && ok && p.State == state {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("PID %d never reached state %d (gone %v)", pid, state, gone)
}

// TestSignal: Signal stops and kills a child, and sends nothing to a PID
// whose start time is another's or below 2.
func TestSignal(t *testing.T) {
	s := &guestprocs.System{}
	cmd := exec.CommandContext(t.Context(), "/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	list, err := s.ByUID(uint32(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	child, ok := find(list, cmd.Process.Pid)
	if !ok {
		t.Fatal("child not listed")
	}
	if child.Name != "sleep" {
		t.Errorf("child's name %q", child.Name)
	}

	other := child
	other.Start++
	if err := s.Signal(other, guestprocs.Stop); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, cmd.Process.Pid, guestprocs.Running, false)

	if err := s.Signal(child, guestprocs.Stop); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, cmd.Process.Pid, guestprocs.Stopped, false)
	if err := s.Signal(child, guestprocs.Kill); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, cmd.Process.Pid, 0, true)

	for _, pid := range []int32{1, 0, -1} {
		if err := s.Signal(guestprocs.Proc{PID: pid}, guestprocs.Kill); !errors.Is(err, guestprocs.ErrPID) {
			t.Errorf("Signal of PID %d: %v, want ErrPID", pid, err)
		}
	}
	// A process that is gone is no error.
	_ = cmd.Wait()
	if err := s.Signal(child, guestprocs.Kill); err != nil {
		t.Errorf("Signal of a reaped child: %v", err)
	}
}
