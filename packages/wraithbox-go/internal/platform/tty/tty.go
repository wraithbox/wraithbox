// Package tty is wb's own terminal. It does two things:
//
//   - Open opens the controlling terminal of the process, for a
//     confirmation that a person must give (SEC14-no-fake-approvals).
//     It doesn't use standard input or output: a pipe there can't
//     answer, and a session's stream never reaches it
//     (S05-03-management-commands, "`wb vm reset-ca`").
//   - Terminal, Notify and StopSelf are what the terminal relay needs
//     of the OS (internal/relay): raw mode, the window size, the
//     signals of S05-02-exit-status-and-signals, and stopping wb
//     itself for job control.
//
// It is in internal/platform because terminals and signals are per OS
// (S12-01-platform-interfaces, "Rules"). darwin and linux make the same
// POSIX calls for the relay, so CI's Linux job runs the PTY tests. Open
// is built on darwin only. What isn't built returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package tty

// Signal is a signal the relay handles, named the same on every OS.
type Signal int

// The signals Notify delivers (S05-02-exit-status-and-signals).
const (
	// WindowChange is SIGWINCH.
	WindowChange Signal = iota + 1
	// Interrupt is SIGINT.
	Interrupt
	// Quit is SIGQUIT.
	Quit
	// Terminate is SIGTERM.
	Terminate
	// Hangup is SIGHUP.
	Hangup
	// TerminalStop is SIGTSTP.
	TerminalStop
	// Continue is SIGCONT.
	Continue
)

// String returns the POSIX name of s.
func (s Signal) String() string {
	switch s {
	case WindowChange:
		return "SIGWINCH"
	case Interrupt:
		return "SIGINT"
	case Quit:
		return "SIGQUIT"
	case Terminate:
		return "SIGTERM"
	case Hangup:
		return "SIGHUP"
	case TerminalStop:
		return "SIGTSTP"
	case Continue:
		return "SIGCONT"
	}
	return "unknown"
}
