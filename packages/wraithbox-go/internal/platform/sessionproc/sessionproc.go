// Package sessionproc makes the process calls of a session: the PTY,
// the start of the session helper as a project user, the helper's start
// of its program, its wait, the signals to the program's process group,
// and the listing of a user's processes with their POSIX session IDs
// (S06-09-session-lifecycle, "Session helper";
// S13-01-process-hardening, "When a session's client goes"). The
// decisions are in internal/guestd/shell. This package only makes the
// calls.
//
// darwin and linux make the same POSIX calls, so CI's Linux job runs
// the tests. The process listing reads sysctl kern.proc and getsid(2)
// on darwin, and /proc on linux. Windows guests are later
// (S12-platforms): every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package sessionproc

import (
	"errors"
	"os"

	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// Cred is the user a process runs as: its user ID, its primary group,
// and its supplementary groups.
type Cred struct {
	UID    uint32
	GID    uint32
	Groups []uint32
}

// HelperSpec is how wb-guestd starts a session helper.
type HelperSpec struct {
	// Path is the program, and Args its whole argument list, Args[0]
	// included.
	Path string
	Args []string
	// Env is the helper's whole environment.
	Env []string
	// Files are the helper's descriptors 0 and up, in order. No other
	// descriptor reaches it.
	Files []*os.File
	// Cred is the user the helper runs as. nil runs it as the caller,
	// which only tests do.
	Cred *Cred
	// Ctty makes descriptor 0, the PTY's replica, the helper's
	// controlling terminal.
	Ctty bool
}

// ProgramSpec is how the helper starts its program. The program gets
// the helper's descriptors 0, 1 and 2, and none other, and leads a
// process group of its own.
type ProgramSpec struct {
	Path string
	// Args is the whole argument list, Args[0] included.
	Args []string
	Env  []string
	// Foreground makes the program's group the foreground group of the
	// controlling terminal on descriptor 0.
	Foreground bool
}

// Event is what a wait saw happen to the program.
type Event int

// The events of Wait.
const (
	Stopped Event = iota + 1
	Continued
	Exited
	Signaled
)

// Status is one wait's result.
type Status struct {
	Event Event
	// Code is the exit code, for Exited.
	Code int
	// Signal is the signal number that ended the program, for Signaled.
	Signal int
}

// Signal is a signal the helper sends its program's process group.
type Signal int

// The signals of a session.
const (
	SigHup Signal = iota + 1
	SigInt
	SigQuit
	SigTerm
	SigTstp
	SigCont
	SigStop
)

func (s Signal) String() string {
	switch s {
	case SigHup:
		return "SIGHUP"
	case SigInt:
		return "SIGINT"
	case SigQuit:
		return "SIGQUIT"
	case SigTerm:
		return "SIGTERM"
	case SigTstp:
		return "SIGTSTP"
	case SigCont:
		return "SIGCONT"
	case SigStop:
		return "SIGSTOP"
	}
	return "unknown"
}

// Proc is one process as the process table reports it, with its POSIX
// session ID.
type Proc struct {
	PID int32
	// Start names the process across a PID's reuse, as guestprocs.Key
	// does: microseconds since the Unix epoch on darwin, clock ticks
	// since boot on linux.
	Start int64
	// UID is the effective user ID, and RUID the real one.
	UID  uint32
	RUID uint32
	// SID is the POSIX session ID.
	SID int32
	// Name is the command name. The process chose it, so it is guest
	// input: escape and cut it before it is logged.
	Name string
}

// Key returns p's guestprocs.Key, which the stop loop keeps.
func (p Proc) Key() gp.Key { return gp.Key{PID: p.PID, Start: p.Start} }

// Table lists a user's processes with their POSIX session IDs, and
// kills one. *System implements it. Tests pass a fake.
type Table interface {
	// ByUID returns every process whose effective or real user ID is
	// uid, with its POSIX session ID. A process that exits during the
	// listing is left out.
	ByUID(uid uint32) ([]Proc, error)
	// Kill sends SIGKILL to p, only while the process with p's PID still
	// has p's start time. A process that is gone is no error. It refuses
	// a PID below 2 with ErrPID.
	Kill(p Proc) error
}

// ErrPID is the error for a PID or process group below 2: kill(2) reads
// 0 and -1 as groups of processes, and 1 is launchd or init.
var ErrPID = errors.New("sessionproc: refusing to signal a PID below 2")
