//go:build darwin || linux

package tty_test

import (
	"os"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

// openPTY returns the terminal end of a new PTY. Both ends close when
// the test ends.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = ptm.Close()
		_ = pts.Close()
	})
	return pts
}

func termios(t *testing.T, f *os.File) unix.Termios {
	t.Helper()
	attrs, err := unix.IoctlGetTermios(int(f.Fd()), getTermios)
	if err != nil {
		t.Fatalf("termios: %v", err)
	}
	return *attrs
}

// TestMakeRaw: raw mode turns ISIG off, so the keyboard's signal keys
// reach the guest as bytes (S05-02-exit-status-and-signals, "From the
// keyboard"), and the restore gives back the termios it found.
func TestMakeRaw(t *testing.T) {
	pts := openPTY(t)
	before := termios(t, pts)
	if before.Lflag&unix.ISIG == 0 {
		t.Fatal("a new PTY without ISIG")
	}
	term, err := tty.NewTerminal(int(pts.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	restore, err := term.MakeRaw()
	if err != nil {
		t.Fatal(err)
	}
	raw := termios(t, pts)
	if raw.Lflag&(unix.ISIG|unix.ICANON|unix.ECHO) != 0 {
		t.Errorf("raw mode keeps ISIG, ICANON or ECHO: lflag %#x", raw.Lflag)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if after := termios(t, pts); after != before {
		t.Errorf("termios after the restore:\n%+v\nwant\n%+v", after, before)
	}
}

func TestSize(t *testing.T) {
	pts := openPTY(t)
	if err := pty.Setsize(pts, &pty.Winsize{Rows: 33, Cols: 101}); err != nil {
		t.Fatal(err)
	}
	term, err := tty.NewTerminal(int(pts.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	rows, cols, err := term.Size()
	if err != nil || rows != 33 || cols != 101 {
		t.Errorf("Size() = %d, %d, %v, want 33, 101, nil", rows, cols, err)
	}
}

func TestNotTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if tty.IsTerminal(int(r.Fd())) {
		t.Error("IsTerminal(pipe) = true")
	}
	if _, err := tty.NewTerminal(int(r.Fd())); err == nil {
		t.Error("NewTerminal(pipe) succeeded")
	}
}

// TestNotify: a signal of the list arrives by its name, and SIGPIPE is
// ignored afterwards, so a write to a closed pipe returns EPIPE
// (S05-02-exit-status-and-signals, "A closed output").
func TestNotify(t *testing.T) {
	c := make(chan tty.Signal, 1)
	stop, err := tty.Notify(c)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := unix.Kill(unix.Getpid(), unix.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-c:
		if s != tty.WindowChange {
			t.Errorf("got %v, want SIGWINCH", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no signal")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("a write to a closed pipe succeeded")
	}
}
