//go:build darwin || linux

package relay

import (
	"os"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

func termios(t *testing.T, f *os.File) unix.Termios {
	t.Helper()
	attrs, err := unix.IoctlGetTermios(int(f.Fd()), getTermios)
	if err != nil {
		t.Fatalf("termios: %v", err)
	}
	return *attrs
}

// panicSizeTerminal is a real terminal whose Size panics.
type panicSizeTerminal struct {
	*tty.Terminal
}

func (panicSizeTerminal) Size() (int, int, error) { panic("size") }

// TestRawModeRestored is S05-02-exit-status-and-signals and
// S05-01-agent-commands, "Interactive and non-interactive use", on a
// real PTY: raw mode, without ISIG, is on during the relay, and the
// termios is what it was after an exit frame, a failure frame, a closed
// stream, and a panic in the relay's goroutine or in its receiver.
func TestRawModeRestored(t *testing.T) {
	tests := []struct {
		name      string
		panicSize bool
		panicRecv bool
		end       func(h *harness)
	}{
		{"exit frame", false, false, func(h *harness) { h.stream.serve(exitCode(0)) }},
		{"failure frame", false, false, func(h *harness) { h.stream.serve(failureFrame("guest-lost", "lost")) }},
		{"closed stream", false, false, func(h *harness) { close(h.stream.frames) }},
		{"panic in the relay", true, false, func(h *harness) { h.sigs <- tty.WindowChange }},
		{"panic in the receiver", false, true, func(*harness) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkRawCase(t, tt.panicSize, tt.panicRecv, tt.end)
		})
	}
}

// checkRawCase runs one relay on a new PTY, ends it with end, and
// compares the termios before and after.
func checkRawCase(t *testing.T, panicSize, panicRecv bool, end func(*harness)) {
	t.Helper()
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptm.Close(); _ = pts.Close() }()
	before := termios(t, pts)
	term, err := tty.NewTerminal(int(pts.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, allTTYs)
	h.cfg.Terminal = term
	if panicSize {
		h.cfg.Terminal = panicSizeTerminal{term}
	}
	h.stream.panicRecv = panicRecv
	h.start()
	if !panicRecv {
		h.e.waitOut(t, "wb: ")
		if during := termios(t, pts); during.Lflag&(unix.ISIG|unix.ICANON|unix.ECHO) != 0 {
			t.Errorf("during the relay lflag %#x, want ISIG, ICANON and ECHO off", during.Lflag)
		}
	}
	end(h)
	h.wait(t)
	if after := termios(t, pts); after != before {
		t.Errorf("termios after the relay:\n%+v\nwant\n%+v", after, before)
	}
}
