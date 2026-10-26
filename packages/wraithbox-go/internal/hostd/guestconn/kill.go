package guestconn

import (
	"context"
	"errors"
	"fmt"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// RuleSessionKillsPending: the kills wb-hostd sends at a VM generation's
// first connection haven't all been sent yet.
const RuleSessionKillsPending = "session-kills-pending"

// ErrKillNotSent is returned by KillUser when the kill couldn't reach the
// guest. The kill stays pending, and fails closed at its deadline.
var ErrKillNotSent = errors.New("guestconn: kill not sent")

// firstKills sends a kill for each project user of the VM's data disk,
// once per VM generation, after the users (S06-10-leftover-processes,
// "Kill IDs and records"). The spec sends them after the reconnect list
// is answered; that list comes with the sessions, and so does the rule
// that skips a user with a lost session. Until then every project user of
// the VM gets the kill. It reports whether the link is kept: a call left
// unanswered reconnects, and the next link sends the kills not sent yet.
// A kill that was sent stays pending until its result or its deadline.
func (r *run) firstKills(l *guestlink.Link) bool {
	if r.ls.cfg.Kills == nil || r.killsDone() {
		return true
	}
	if r.ls.cfg.Users == nil {
		r.setKillsDone()
		return true
	}
	if !r.mounted() {
		return true
	}
	rows, err := r.ls.cfg.Users.ProjectUsers(r.ctx, r.diskSlot())
	if err != nil {
		// Sessions stay refused, and the next time call tries again.
		r.recordFailed("project users not read", err)
		return true
	}
	if r.killed == nil {
		r.killed = map[string]bool{}
	}
	for _, u := range rows {
		if r.killed[u.ProjectID] {
			continue
		}
		r.killed[u.ProjectID] = true
		if !r.sendKill(r.ctx, l, u) {
			return false
		}
	}
	r.setKillsDone()
	return true
}

// sendKill records a kill of u with the tracker, sends it, and gives
// the tracker the answer. It reports false when the call went unanswered
// or the link dropped under it.
func (r *run) sendKill(ctx context.Context, l *guestlink.Link, u state.ProjectUser) bool {
	k := r.ls.cfg.Kills
	id, err := k.Begin(r.slot, r.generation, u.ProjectID)
	if err != nil {
		// The generation ended, and the run with it.
		return true
	}
	out, err := l.KillUser(ctx, &guestv1.KillUserRequest{KillId: id, ProjectId: u.ProjectID, Uid: u.UID}, r.vc.log)
	switch {
	case ctx.Err() != nil:
		return true
	case err != nil && (guestlink.IsUnanswered(err) || isDown(l)):
		return false
	case err != nil:
		// guestlink logged the refused answer with its rule. The kill fails
		// closed at its deadline.
		return true
	}
	k.AcceptReply(r.slot, r.generation, id, u.ProjectID, out)
	return true
}

func (r *run) killsDone() bool {
	r.ls.mu.Lock()
	defer r.ls.mu.Unlock()
	g := r.ls.users[r.slot]
	return g != nil && g.generation == r.generation && g.killsDone
}

func (r *run) setKillsDone() {
	r.ls.withUsers(r.slot, r.generation, func(g *genUsers) { g.killsDone = true })
}

// KillUser sends a kill of project user u in slot's VM generation, as at
// the end of a project's last session there (S06-10-leftover-processes,
// "Kill IDs and records"). The kill is recorded before it is sent, so a
// kill of the running generation that can't reach the guest,
// ErrKillNotSent, still fails closed at its deadline. A refused or late answer is logged with its rule, and
// counts the same way.
func (ls *Links) KillUser(ctx context.Context, slot vm.Slot, generation uint64, u state.ProjectUser) error {
	if ls.cfg.Kills == nil {
		return fmt.Errorf("%w: wb-hostd has no kill tracker", ErrKillNotSent)
	}
	r, l := ls.linkOf(slot, generation)
	if l == nil {
		// Only the slot's running generation has a guest to kill in.
		if s := ls.Status(slot); s.Generation == generation && s.State != StateDown {
			_, _ = ls.cfg.Kills.Begin(slot, generation, u.ProjectID)
		}
		return fmt.Errorf("%w: no link to wb-guestd in the %s VM", ErrKillNotSent, slot)
	}
	if !r.sendKill(ctx, l, u) {
		// The run's loop sees the link down, logs the drop and reconnects.
		_ = l.Close()
		return fmt.Errorf("%w: wb-guestd in the %s VM didn't answer", ErrKillNotSent, slot)
	}
	return nil
}

// checkKills refuses a session start in slot's VM generation until its
// first-connection kills are sent, and after a result of processes left
// in it (S06-10-leftover-processes, "A result of processes left"). nil
// when wb-hostd has no kill tracker.
func (ls *Links) checkKills(slot vm.Slot, generation uint64) *SessionRefusal {
	if ls.cfg.Kills == nil {
		return nil
	}
	ls.mu.Lock()
	g := ls.users[slot]
	done := g != nil && g.generation == generation && g.killsDone
	ls.mu.Unlock()
	if !done {
		return &SessionRefusal{Rule: RuleSessionKillsPending, Message: fmt.Sprintf(
			"wb-hostd is still stopping leftover processes of the project users in the %s VM. Try again in a minute.", slot)}
	}
	if r := ls.cfg.Kills.CheckAdmission(slot, generation); r != nil {
		return &SessionRefusal{Rule: r.Rule, Message: r.Message}
	}
	return nil
}

// checkProjectKill refuses a session of projectID while a kill of its
// user is pending in slot's VM generation.
func (ls *Links) checkProjectKill(slot vm.Slot, generation uint64, projectID string) *SessionRefusal {
	if ls.cfg.Kills == nil {
		return nil
	}
	if r := ls.cfg.Kills.CheckProject(slot, generation, projectID); r != nil {
		return &SessionRefusal{Rule: r.Rule, Message: r.Message}
	}
	return nil
}
