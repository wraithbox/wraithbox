//go:build darwin || linux

package shell

import (
	"slices"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// sid returns the open shell's POSIX session ID.
func (h *harness) sid() int32 {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return h.srv.open[testShellID].sid.Load()
}

// The processes otherUserTable adds to the shell's POSIX session.
const (
	otherPID = 999_991
	mixedPID = 999_992
)

// otherUserTable lists, next to the real processes, one process of
// another user and one with the user's real but not effective user ID,
// both in each open shell's POSIX session, and records each kill.
type otherUserTable struct {
	inner sessionproc.Table
	srv   *Server
	uid   uint32

	mu     sync.Mutex
	killed []int32
}

func (o *otherUserTable) ByUID(uid uint32) ([]sessionproc.Proc, error) {
	ps, err := o.inner.ByUID(uid)
	for sid := range o.srv.openSessions() {
		ps = append(ps,
			sessionproc.Proc{PID: otherPID, Start: 1, UID: o.uid + 1, RUID: o.uid + 1, SID: sid, Name: "other"},
			sessionproc.Proc{PID: mixedPID, Start: 1, UID: o.uid + 1, RUID: o.uid, SID: sid, Name: "setuid"})
	}
	return ps, err
}

func (o *otherUserTable) Kill(p sessionproc.Proc) error {
	o.mu.Lock()
	o.killed = append(o.killed, p.PID)
	o.mu.Unlock()
	if p.PID == otherPID || p.PID == mixedPID {
		return nil
	}
	return o.inner.Kill(p)
}

func (o *otherUserTable) killedPIDs() []int32 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.killed)
}
