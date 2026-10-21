package tty

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// The relay's calls. term_darwin.go makes the same POSIX calls.

// Terminal is the terminal on one file descriptor, wb's standard input
// for the relay.
type Terminal struct {
	fd int
}

// NewTerminal returns the terminal on fd, for the relay. It fails when
// fd isn't a terminal.
func NewTerminal(fd int) (*Terminal, error) {
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("tty: descriptor %d isn't a terminal", fd)
	}
	return &Terminal{fd: fd}, nil
}

// IsTerminal reports whether fd is a terminal. It decides interactive
// use (S05-01-agent-commands, "Interactive and non-interactive use").
func IsTerminal(fd int) bool { return term.IsTerminal(fd) }

// MakeRaw puts the terminal in raw mode, with ISIG off, so `Ctrl-C`,
// `Ctrl-\` and `Ctrl-Z` reach the guest as bytes
// (S05-02-exit-status-and-signals, "From the keyboard"). It returns a
// function that restores the mode the terminal had.
func (t *Terminal) MakeRaw() (func() error, error) {
	st, err := term.MakeRaw(t.fd)
	if err != nil {
		return nil, fmt.Errorf("tty: raw mode: %w", err)
	}
	return func() error { return term.Restore(t.fd, st) }, nil
}

// Size returns the window size in rows and columns.
func (t *Terminal) Size() (rows, cols int, err error) {
	cols, rows, err = term.GetSize(t.fd)
	if err != nil {
		return 0, 0, fmt.Errorf("tty: window size: %w", err)
	}
	return rows, cols, nil
}

// notified maps the OS signals Notify delivers to their names.
var notified = map[os.Signal]Signal{
	unix.SIGWINCH: WindowChange,
	unix.SIGINT:   Interrupt,
	unix.SIGQUIT:  Quit,
	unix.SIGTERM:  Terminate,
	unix.SIGHUP:   Hangup,
	unix.SIGTSTP:  TerminalStop,
	unix.SIGCONT:  Continue,
}

// Notify delivers the signals of S05-02-exit-status-and-signals to c
// until stop is called, which gives them their default effect again.
// It also ignores SIGPIPE for the rest of the process, so a write to a
// pipe whose reader closed it returns EPIPE instead of ending wb
// (S05-02-exit-status-and-signals, "A closed output").
func Notify(c chan<- Signal) (stop func(), err error) {
	signal.Ignore(unix.SIGPIPE)
	in := make(chan os.Signal, 16)
	sigs := make([]os.Signal, 0, len(notified))
	for s := range notified {
		sigs = append(sigs, s)
	}
	signal.Notify(in, sigs...)
	done := make(chan struct{})
	go forward(in, c, done)
	return func() {
		signal.Stop(in)
		close(done)
	}, nil
}

// forward names each signal from in and sends it on c, until done.
func forward(in <-chan os.Signal, c chan<- Signal, done <-chan struct{}) {
	for {
		select {
		case s := <-in:
			select {
			case c <- notified[s]:
			case <-done:
				return
			}
		case <-done:
			return
		}
	}
}

// StopSelf stops the process with SIGSTOP, which it can't catch, so the
// user's shell has the terminal again (S05-02-exit-status-and-signals,
// "Job control"). It returns once the process continued.
func StopSelf() error {
	if err := unix.Kill(unix.Getpid(), unix.SIGSTOP); err != nil {
		return fmt.Errorf("tty: stop: %w", err)
	}
	return nil
}
