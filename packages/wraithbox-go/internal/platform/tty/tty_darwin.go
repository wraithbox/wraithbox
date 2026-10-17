package tty

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// fread is FREAD of sys/fcntl.h: TIOCFLUSH with it discards the input
// queue, as tcflush(fd, TCIFLUSH) does.
const fread = 0x0001

// Open opens /dev/tty, the process's controlling terminal, for reading
// and writing, and discards any input typed before it opened, so only
// an answer typed after the question can answer it
// (SEC14-no-fake-approvals). A process without a controlling terminal,
// as under launchd or a CI runner, gets an error, and so does a /dev/tty
// that isn't a terminal.
func Open() (io.ReadWriteCloser, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("tty: no controlling terminal: %w", err)
	}
	fd := int(f.Fd())
	if _, err := unix.IoctlGetTermios(fd, unix.TIOCGETA); err != nil {
		return nil, errors.Join(fmt.Errorf("tty: /dev/tty isn't a terminal: %w", err), f.Close())
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, fread); err != nil {
		return nil, errors.Join(fmt.Errorf("tty: the typed-ahead input can't be discarded: %w", err), f.Close())
	}
	return f, nil
}
