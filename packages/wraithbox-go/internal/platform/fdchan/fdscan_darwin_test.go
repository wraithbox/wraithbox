package fdchan

import (
	"os"
	"syscall"
	"testing"
)

// TestFirstOpenFrom: an open descriptor is found from its number on,
// and isn't once it is closed.
func TestFirstOpenFrom(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	high := int(w.Fd())
	got, err := FirstOpenFrom(high)
	if err != nil || got != high {
		t.Fatalf("FirstOpenFrom(%d) = %d, %v", high, got, err)
	}
	_ = w.Close()
	// Another test may open a descriptor meanwhile, but not this one.
	if got, err := FirstOpenFrom(high); err != nil || got == high {
		t.Fatalf("FirstOpenFrom(%d) after the close = %d, %v", high, got, err)
	}
}

// TestCloseOnExecFrom: a descriptor without close-on-exec gets it.
func TestCloseOnExecFrom(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = r.Close(), w.Close() }()
	fd := int(w.Fd())
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, 0); errno != 0 {
		t.Fatal(errno)
	}
	if n, err := CloseOnExecFrom(fd); err != nil || n < 1 {
		t.Fatalf("CloseOnExecFrom(%d) = %d, %v", fd, n, err)
	}
	if flags, open := fdFlags(fd); !open || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("descriptor %d: open %v, flags %#x", fd, open, flags)
	}
}
