// Package guestprocs lists and signals the guest's processes by user
// ID, and boots out a user's launchd domain, for wb-guestd's kill and
// stop loops (S13-01-process-hardening, "When the link drops",
// "Per-user launchd services"). wb-guestd runs it as root in the
// guest. The decisions are in internal/guestd/procs. This package only
// makes the calls: on darwin, sysctl kern.proc through
// golang.org/x/sys/unix, kill(2), and /bin/launchctl with an argument
// list, never a shell.
//
// Linux and Windows guests are later (S12-platforms). Until they are
// built, every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guestprocs

import (
	"context"
	"errors"
)

// State is what the process table says a process is doing.
type State int

// The states a loop tells apart.
const (
	// Running is any state other than stopped or zombie: runnable,
	// sleeping, or being created.
	Running State = iota + 1
	// Stopped is a process stopped by a signal, such as SIGSTOP or job
	// control.
	Stopped
	// Zombie has exited and waits for its parent to reap it. It runs
	// nothing.
	Zombie
)

// Signal is a signal a loop sends.
type Signal int

// The signals the loops send.
const (
	Kill Signal = iota + 1
	Stop
)

func (s Signal) String() string {
	switch s {
	case Kill:
		return "SIGKILL"
	case Stop:
		return "SIGSTOP"
	}
	return "unknown"
}

// Key names one process across a PID's reuse: its PID and its start
// time in microseconds since the Unix epoch.
type Key struct {
	PID   int32
	Start int64
}

// Proc is one process as the process table reports it.
type Proc struct {
	PID int32
	// Start is the start time in microseconds since the Unix epoch.
	Start int64
	// UID is the effective user ID, and RUID the real one.
	UID  uint32
	RUID uint32
	// Name is the command name the kernel keeps, at most 16 bytes. The
	// process chose it, so it is guest input: escape and cut it before
	// it is logged.
	Name  string
	State State
}

// Key returns p's Key.
func (p Proc) Key() Key { return Key{PID: p.PID, Start: p.Start} }

// Table reads the guest's process table and signals processes in it.
// *System implements it on darwin. Tests pass a fake.
type Table interface {
	// ByUID returns every process whose effective or real user ID is
	// uid, so a setuid program the user started counts too.
	ByUID(uid uint32) ([]Proc, error)
	// InRange returns every process whose effective or real user ID is
	// from first to last, both included.
	InRange(first, last uint32) ([]Proc, error)
	// Signal sends sig to p, only while the process with p's PID still
	// has p's start time and user IDs, so a PID the kernel gave to
	// another process meanwhile is left alone. A process that is gone is
	// no error. It refuses a PID below 2 with ErrPID: kill(2) reads 0
	// and -1 as groups of processes, and 1 is launchd.
	Signal(p Proc, sig Signal) error
	// Bootout runs `launchctl bootout user/<uid>`, which stops the
	// user's per-user launchd domain and the agents in it.
	Bootout(ctx context.Context, uid uint32) error
}

// ErrPID is Signal's error for a PID below 2.
var ErrPID = errors.New("guestprocs: refusing to signal a PID below 2")
