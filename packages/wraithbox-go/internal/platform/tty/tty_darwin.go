package tty

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Open opens /dev/tty, the process's controlling terminal, for reading
// and writing. A process without one, as under launchd or a CI runner,
// gets an error, and so does a /dev/tty that isn't a terminal.
func Open() (io.ReadWriteCloser, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("tty: no controlling terminal: %w", err)
	}
	if _, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tty: /dev/tty isn't a terminal: %w", err)
	}
	return f, nil
}
