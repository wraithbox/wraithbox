package guestconn

import (
	"context"
	"fmt"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// RuleUnlockFailed: wb-guestd didn't unlock the project user for a
// session start.
const RuleUnlockFailed = "session-unlock-failed"

// UnlockUser has the guest of slot's run of generation unlock project
// user u for a session start (S13-01-process-hardening, "Locked
// users"). Every error is a *SessionRefusal, with what to do
// (NFR06-explained-refusals). A request the guest leaves unanswered
// drops the link, as EnsureProjectUser's does.
func (ls *Links) UnlockUser(ctx context.Context, slot vm.Slot, generation uint64, u state.ProjectUser) error {
	_, l := ls.linkOf(slot, generation)
	if l == nil {
		return ls.refuseSession(slot, generation, noLink(slot))
	}
	err := l.UnlockUser(ctx, u.UID, ls.vm(slot).log)
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && guestlink.IsUnanswered(err) {
		// The run's loop sees the link down, logs the drop and reconnects.
		_ = l.Close()
	}
	return ls.refuseSession(slot, generation, &SessionRefusal{Rule: RuleUnlockFailed, Message: fmt.Sprintf(
		"wb-guestd in the %s VM didn't unlock the project user %s, so the session didn't start. "+
			"The wb-hostd log has the guest's answer. Try again, or stop the VM with wb vm stop %s and start it again.",
		slot, projectuser.UserName(u.UID), slot)})
}

// OpenShell opens a GuestService.Shell stream on the link of slot's run
// of generation, which ends with ctx. It also returns the link's down
// channel, closed when the link's connection drops, so the caller can
// tell a lost link from a stream the guest ended.
func (ls *Links) OpenShell(ctx context.Context, slot vm.Slot, generation uint64) (guestv1.GuestService_ShellClient, <-chan struct{}, error) {
	_, l := ls.linkOf(slot, generation)
	if l == nil {
		return nil, nil, ls.refuseSession(slot, generation, noLink(slot))
	}
	stream, err := l.Shell(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("guestconn: open a shell in the %s VM: %w", slot, err)
	}
	return stream, l.Down(), nil
}
