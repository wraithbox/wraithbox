package sessionproc

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"

	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// System reads the process table of the guest it runs in: sysctl
// kern.proc through guestprocs, and getsid(2) for each process.
type System struct{}

// ByUID lists uid's processes with guestprocs, and reads each one's
// POSIX session ID with getsid(2). A process gone in between is left
// out.
func (System) ByUID(uid uint32) ([]Proc, error) {
	procs, err := (&gp.System{}).ByUID(uid)
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(procs))
	for _, p := range procs {
		if p.State == gp.Zombie {
			continue
		}
		sid, err := unix.Getsid(int(p.PID))
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("sessionproc: getsid %d: %w", p.PID, err)
		}
		out = append(out, Proc{PID: p.PID, Start: p.Start, UID: p.UID, RUID: p.RUID, SID: PID(sid), Name: p.Name})
	}
	return out, nil
}

// Kill sends SIGKILL with guestprocs, which checks the start time
// right before kill(2).
func (System) Kill(p Proc) error {
	if p.PID < 2 {
		return ErrPID
	}
	return (&gp.System{}).Signal(gp.Proc{PID: p.PID, Start: p.Start, UID: p.UID, RUID: p.RUID}, gp.Kill)
}
