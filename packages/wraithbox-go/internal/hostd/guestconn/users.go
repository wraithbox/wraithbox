package guestconn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// UserRecords is the part of state.db the project user calls read and
// write (S06-03-image-updates, "Host state per project user"). *state.DB
// implements it.
type UserRecords interface {
	ProjectUser(ctx context.Context, projectID, slot string) (state.ProjectUser, bool, error)
	ProjectUsers(ctx context.Context, slot string) ([]state.ProjectUser, error)
	UserRemovals(ctx context.Context, slot string) ([]state.UserRemoval, error)
	ClearUserRemoval(ctx context.Context, slot, projectID string, uid uint32) (bool, error)
}

// The rules of the project user calls besides guestlink's.
const (
	// RuleUsersRecord: wb-hostd couldn't read or write its project user
	// records. The users stay unconfirmed and the removals stay queued.
	RuleUsersRecord = "guest-users-record"
	// RuleSessionUserPending: no answer of this VM generation has
	// confirmed the project's user yet.
	RuleSessionUserPending = "session-user-pending"
)

// genUsers is what one VM generation's guest said about the project
// users (S06-03-image-updates, "Updates"). A user is confirmed by an
// accepted OK. A user any answer refused stays refused until the next
// generation, whatever a later answer says. A user whose request went
// unanswered is neither, and the next link asks again.
type genUsers struct {
	generation uint64
	ok         map[uint32]bool
	// refused holds the rule of each refused user.
	refused map[uint32]string
}

func newGenUsers(generation uint64) *genUsers {
	return &genUsers{generation: generation, ok: map[uint32]bool{}, refused: map[uint32]string{}}
}

func (g *genUsers) refuse(uid uint32, rule string) {
	delete(g.ok, uid)
	if _, ok := g.refused[uid]; !ok {
		g.refused[uid] = rule
	}
}

func (g *genUsers) confirm(uid uint32) {
	if _, refused := g.refused[uid]; !refused {
		g.ok[uid] = true
	}
}

// protoUser is the request form of u.
func protoUser(u state.ProjectUser) *guestv1.ProjectUser {
	return &guestv1.ProjectUser{
		ProjectId: u.ProjectID, UserName: u.UserName, Uid: u.UID, Gid: u.GID, GroupName: u.GroupName,
		UserGeneratedUid: u.UserGeneratedUID, GroupGeneratedUid: u.GroupGeneratedUID, HomePath: u.HomePath,
	}
}

// users sends the data disk's users, then its queued removals, after a
// mount this link's guest answered (S06-03-image-updates, "Updates";
// S05-04-naming, "The project user"). It reports whether the link is
// kept: a call left unanswered reconnects, and the next link sends both
// again.
func (r *run) users(l *guestlink.Link) bool {
	if r.ls.cfg.Users == nil || !r.mounted() {
		return true
	}
	return r.ensureAll(l) && r.sendRemovals(l)
}

// mounted reports whether this generation's data mount succeeded.
func (r *run) mounted() bool {
	s := r.ls.Status(r.slot)
	return s.Generation == r.generation && s.DataMount.State == MountMounted
}

func (r *run) diskSlot() string { return vmbundle.Name(string(r.slot)) }

// ensureAll sends EnsureUsers with every user of the VM's data disk, in
// requests that fit wb-guestd's receive cap.
func (r *run) ensureAll(l *guestlink.Link) bool {
	rows, err := r.ls.cfg.Users.ProjectUsers(r.ctx, r.diskSlot())
	if err != nil {
		r.recordFailed("project users not read", err)
		return true
	}
	users := make([]*guestv1.ProjectUser, 0, len(rows))
	for _, u := range rows {
		users = append(users, protoUser(u))
	}
	for _, part := range guestlink.SplitUsers(users) {
		if !r.ensure(r.ctx, l, part) {
			return false
		}
	}
	return true
}

// ensure sends one EnsureUsers request and keeps its results for the
// generation (SEC11-root-gains-nothing). Every user of the request
// without an accepted OK is refused for the generation, unless the call
// went unanswered: then it reports false, and the user stays
// unconfirmed until a later link asks again.
func (r *run) ensure(ctx context.Context, l *guestlink.Link, part []*guestv1.ProjectUser) bool {
	results, err := l.EnsureUsers(ctx, part, r.vc.log)
	if err != nil {
		if ctx.Err() != nil {
			return true
		}
		if guestlink.IsUnanswered(err) || isDown(l) {
			return false
		}
		rule := guestlink.RuleUsersFailed
		if ur, ok := errors.AsType[*guestlink.UsersRefusal](err); ok {
			rule = ur.Rule
		}
		r.ls.withUsers(r.slot, r.generation, func(g *genUsers) {
			for _, u := range part {
				g.refuse(u.GetUid(), rule)
			}
		})
		return true
	}
	r.ls.withUsers(r.slot, r.generation, func(g *genUsers) { g.take(part, results) })
	return true
}

// take keeps the accepted results of a request of part: OK confirms a
// user, and a refusal or no result refuses it.
func (g *genUsers) take(part []*guestv1.ProjectUser, results map[uint32]guestlink.UserResult) {
	for _, u := range part {
		res, ok := results[u.GetUid()]
		if ok && res.OK {
			g.confirm(u.GetUid())
			continue
		}
		g.refuse(u.GetUid(), guestlink.RuleUserRefused)
	}
}

// sendRemovals sends each of the data disk's queued removals, and clears
// a removal only when the guest answers REMOVED for its project and UID.
// Any other answer keeps it, and the next link sends it again
// (S05-04-naming, "The project user").
func (r *run) sendRemovals(l *guestlink.Link) bool {
	removals, err := r.ls.cfg.Users.UserRemovals(r.ctx, r.diskSlot())
	if err != nil {
		r.recordFailed("user removals not read", err)
		return true
	}
	for _, rm := range removals {
		req := &guestv1.RemoveUserRequest{ProjectId: rm.ProjectID, Uid: rm.UID, Gid: rm.GID, HomePath: rm.HomePath}
		out, err := l.RemoveUser(r.ctx, req, r.vc.log)
		switch {
		case r.ctx.Err() != nil:
			return true
		case err != nil && (guestlink.IsUnanswered(err) || isDown(l)):
			return false
		case err != nil, out.Result != guestv1.RemoveResult_REMOVE_RESULT_REMOVED:
			continue
		}
		// CheckRemoveUser took the answer only for req's project and UID.
		if _, err := r.ls.cfg.Users.ClearUserRemoval(r.ctx, rm.Slot, rm.ProjectID, rm.UID); err != nil {
			r.recordFailed("user removal not cleared", err)
		}
	}
	return true
}

// recordFailed logs a project user record that couldn't be read or
// written.
func (r *run) recordFailed(what string, err error) {
	r.vc.log.LogAttrs(r.ctx, slog.LevelError, what,
		slog.Uint64("generation", r.generation), slog.String("rule", RuleUsersRecord),
		slog.String("decision", "keep"), slog.String("error", err.Error()))
}

// isDown reports whether l's connection has dropped.
func isDown(l *guestlink.Link) bool {
	select {
	case <-l.Down():
		return true
	default:
		return false
	}
}

// withUsers changes slot's project user state when it is still of
// generation.
func (ls *Links) withUsers(slot vm.Slot, generation uint64, change func(*genUsers)) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if g := ls.users[slot]; g != nil && g.generation == generation {
		change(g)
	}
}

// SendUserRemovals asks every linked VM to send its queued user removals
// now, as `wb project rm` does after it queued them. A VM without a link
// sends them at its next connection. It doesn't block.
func (ls *Links) SendUserRemovals() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, r := range ls.active {
		select {
		case r.kick <- struct{}{}:
		default:
		}
	}
}

// EnsureProjectUser gets projectID's user on the data disk of slot's VM,
// or makes one, which state.db commits before the guest hears of it.
// Then it asks the guest of generation to make the user, and returns
// CheckUser's answer (S06-03-image-updates, "Allocation"). A session
// start calls it, then starts only on a nil error. Every error is a
// *SessionRefusal, with what to do (NFR06-explained-refusals).
func (ls *Links) EnsureProjectUser(ctx context.Context, slot vm.Slot, generation uint64, projectID string) (state.ProjectUser, error) {
	r, l := ls.linkOf(slot, generation)
	if l == nil || ls.cfg.Users == nil {
		return state.ProjectUser{}, ls.refuseSession(slot, generation, noLink(slot))
	}
	u, _, err := ls.cfg.Users.ProjectUser(ctx, projectID, vmbundle.Name(string(slot)))
	if err != nil {
		return state.ProjectUser{}, ls.refuseSession(slot, generation, allocationRefusal(slot, err))
	}
	r.ensure(ctx, l, []*guestv1.ProjectUser{protoUser(u)})
	return u, ls.CheckUser(slot, generation, u.UID)
}

// allocationRefusal says why a project's user couldn't be had.
func allocationRefusal(slot vm.Slot, err error) *SessionRefusal {
	switch {
	case errors.Is(err, state.ErrUserIDsExhausted):
		return &SessionRefusal{Rule: projectuser.RuleIDsExhausted, Message: fmt.Sprintf(
			"wb-hostd has handed out every project user ID, up to %d, so no new project gets a user in the %s VM.",
			projectuser.LastID, slot)}
	case errors.Is(err, state.ErrProjectNotFound):
		return &SessionRefusal{Rule: RuleUsersRecord, Message: "the project isn't registered. Start the session again."}
	}
	return &SessionRefusal{Rule: RuleUsersRecord, Message: fmt.Sprintf(
		"wb-hostd couldn't read or write the project's user on the %s VM's data disk. The wb-hostd log has the cause.", slot)}
}

// CheckUser refuses a session as project user uid in slot's run of
// generation unless an accepted answer of that generation's guest
// confirmed the user, and none refused it (S06-03-image-updates,
// "Updates"; SEC08-proj-isolation).
func (ls *Links) CheckUser(slot vm.Slot, generation uint64, uid uint32) error {
	ls.mu.Lock()
	g := ls.users[slot]
	var (
		current         = g != nil && g.generation == generation
		rule, isRefused = "", false
		ok              = false
	)
	if current {
		rule, isRefused = g.refused[uid]
		ok = g.ok[uid]
	}
	ls.mu.Unlock()
	name := projectuser.UserName(uid)
	switch {
	case !current:
		return ls.refuseSession(slot, generation, noLink(slot))
	case isRefused:
		return ls.refuseSession(slot, generation, &SessionRefusal{Rule: rule, Message: fmt.Sprintf(
			"wb-guestd in the %s VM didn't make the project user %s (%s), so no session of the project starts in it "+
				"until the VM starts again. The wb-hostd log has the guest's answer. Stop the VM with wb vm stop %s and start it again.",
			slot, name, rule, slot)})
	case !ok:
		return ls.refuseSession(slot, generation, &SessionRefusal{Rule: RuleSessionUserPending, Message: fmt.Sprintf(
			"wb-guestd in the %s VM hasn't confirmed the project user %s yet. Try again in a minute.", slot, name)})
	}
	return nil
}

func noLink(slot vm.Slot) *SessionRefusal {
	return &SessionRefusal{Rule: RuleSessionNoLink, Message: fmt.Sprintf(
		"wb-hostd has no link to wb-guestd in the %s VM. Wait for it, or stop and start the VM.", slot)}
}

// refuseSession logs a refused session start, and returns r.
func (ls *Links) refuseSession(slot vm.Slot, generation uint64, r *SessionRefusal) error {
	ls.vm(slot).log.LogAttrs(context.Background(), slog.LevelWarn, "session start refused",
		slog.Uint64("generation", generation), slog.String("rule", r.Rule), slog.String("decision", "refuse"))
	return r
}

// linkOf returns slot's run of generation and its link, while the link
// is up and has sent the data disk's users.
func (ls *Links) linkOf(slot vm.Slot, generation uint64) (*run, *guestlink.Link) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	r := ls.active[slot]
	if r == nil || r.generation != generation || r.link == nil {
		return nil, nil
	}
	return r, r.link
}

// setLink records l as the run's link, or nil when it is gone.
func (r *run) setLink(l *guestlink.Link) {
	r.ls.mu.Lock()
	defer r.ls.mu.Unlock()
	r.link = l
}
