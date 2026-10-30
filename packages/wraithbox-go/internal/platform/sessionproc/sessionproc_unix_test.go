//go:build darwin || linux

package sessionproc_test

import (
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// own is the test's own user, which a PTY replica can be given to
// without privileges.
func own() sessionproc.Cred {
	return sessionproc.Cred{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
}

func TestOpenPTYOwnerModeAndSize(t *testing.T) {
	ctl, replica, err := sessionproc.OpenPTY(own(), 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctl.Close(); _ = replica.Close() }()
	fi, err := replica.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != sessionproc.ReplicaMode {
		t.Errorf("replica mode %o, want %o", got, sessionproc.ReplicaMode)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != own().UID {
		t.Errorf("replica owner %v, want %d", fi.Sys(), own().UID)
	}
	rows, cols, err := sessionproc.Size(replica)
	if err != nil || rows != 24 || cols != 80 {
		t.Fatalf("size %dx%d (%v), want 24x80", rows, cols, err)
	}
	if err := sessionproc.SetSize(ctl, 50, 132); err != nil {
		t.Fatal(err)
	}
	rows, cols, err = sessionproc.Size(replica)
	if err != nil || rows != 50 || cols != 132 {
		t.Fatalf("size %dx%d (%v), want 50x132", rows, cols, err)
	}
}

// TestCloseEndsRead checks that a Close of the controller ends a Read
// that waits on it, which the session's hangup relies on.
func TestCloseEndsRead(t *testing.T) {
	ctl, replica, err := sessionproc.OpenPTY(own(), 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := ctl.Read(make([]byte, 16))
		done <- err
	}()
	_ = ctl.Close()
	if err := <-done; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after close: %v, want os.ErrClosed", err)
	}
}

// TestWaitEvents starts sleep in a group of its own, stops, continues
// and kills it, and checks each event Wait reports.
func TestWaitEvents(t *testing.T) {
	pid, err := sessionproc.StartProgram(sessionproc.ProgramSpec{Path: "/bin/sleep", Args: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	if pgid, err := sessionproc.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("process group %d (%v), want %d", pgid, err, pid)
	}
	steps := []struct {
		sig  sessionproc.Signal
		want sessionproc.Status
	}{
		{sessionproc.SigStop, sessionproc.Status{Event: sessionproc.Stopped}},
		{sessionproc.SigCont, sessionproc.Status{Event: sessionproc.Continued}},
		{sessionproc.SigTerm, sessionproc.Status{Event: sessionproc.Signaled, Signal: int(syscall.SIGTERM)}},
	}
	for _, s := range steps {
		if err := sessionproc.SignalGroup(pid, s.sig); err != nil {
			t.Fatal(err)
		}
		got, err := sessionproc.Wait(pid)
		if err != nil || got != s.want {
			t.Fatalf("after %s: %+v (%v), want %+v", s.sig, got, err, s.want)
		}
	}
}

func TestWaitExitCode(t *testing.T) {
	pid, err := sessionproc.StartProgram(sessionproc.ProgramSpec{Path: "/bin/sh", Args: []string{"sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sessionproc.Wait(pid)
	if want := (sessionproc.Status{Event: sessionproc.Exited, Code: 3}); err != nil || got != want {
		t.Fatalf("%+v (%v), want %+v", got, err, want)
	}
}

func TestSignalGroupRefusesLowPIDs(t *testing.T) {
	for _, pgid := range []int{-1, 0, 1} {
		if err := sessionproc.SignalGroup(pgid, sessionproc.SigHup); !errors.Is(err, sessionproc.ErrPID) {
			t.Errorf("group %d: %v, want ErrPID", pgid, err)
		}
	}
}

func TestSystemKillRefusesLowPIDs(t *testing.T) {
	for _, pid := range []int32{-1, 0, 1} {
		if err := (sessionproc.System{}).Kill(sessionproc.Proc{PID: pid}); !errors.Is(err, sessionproc.ErrPID) {
			t.Errorf("pid %d: %v, want ErrPID", pid, err)
		}
	}
}

// TestSystemByUIDListsOwnProcess finds the test itself among its
// user's processes, with its own POSIX session ID.
func TestSystemByUIDListsOwnProcess(t *testing.T) {
	procs, err := sessionproc.System{}.ByUID(own().UID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := sessionproc.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(procs, func(p sessionproc.Proc) bool { return int(p.PID) == os.Getpid() })
	if i < 0 {
		t.Fatalf("the test's pid %d isn't among %d processes", os.Getpid(), len(procs))
	}
	if p := procs[i]; int(p.SID) != sid || p.Start == 0 || p.UID != own().UID {
		t.Errorf("own process %+v, want sid %d, a start time and uid %d", p, sid, own().UID)
	}
}

// TestSystemKillChecksStart kills a child only when its start time
// matches.
func TestSystemKillChecksStart(t *testing.T) {
	pid, err := sessionproc.StartProgram(sessionproc.ProgramSpec{Path: "/bin/sleep", Args: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	procs, err := sessionproc.System{}.ByUID(own().UID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(procs, func(p sessionproc.Proc) bool { return int(p.PID) == pid })
	if i < 0 {
		t.Fatalf("child %d not listed", pid)
	}
	stale := procs[i]
	stale.Start++
	if err := (sessionproc.System{}).Kill(stale); err != nil {
		t.Fatal(err)
	}
	if err := sessionproc.SignalGroup(pid, sessionproc.SigStop); err != nil {
		t.Fatal(err)
	}
	if st, err := sessionproc.Wait(pid); err != nil || st.Event != sessionproc.Stopped {
		t.Fatalf("the stale kill reached the child: %+v (%v)", st, err)
	}
	if err := (sessionproc.System{}).Kill(procs[i]); err != nil {
		t.Fatal(err)
	}
	st, err := sessionproc.Wait(pid)
	if want := (sessionproc.Status{Event: sessionproc.Signaled, Signal: int(syscall.SIGKILL)}); err != nil || st != want {
		t.Fatalf("%+v (%v), want %+v", st, err, want)
	}
}

func TestCheckPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if err := sessionproc.CheckPipe(int(r.Fd())); err != nil {
		t.Errorf("a pipe: %v", err)
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := sessionproc.CheckPipe(int(f.Fd())); err == nil {
		t.Error("/dev/null passed as a pipe")
	}
}
