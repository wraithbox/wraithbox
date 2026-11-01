package guestd

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/cleanup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// RuleKillRequest refuses a KillUser request that isn't in the form
// wb-hostd sends: a kill ID of 0, a project ID out of its form, or a UID
// out of the project users' range.
const RuleKillRequest = "guestd-kill-invalid"

// SystemProcesses is the kill and stop loops over the guest's own
// process table, cron and at, and clock.
func SystemProcesses(log *slog.Logger) *procs.Loops {
	return procs.New(&guestprocs.System{Log: log}, guestsched.System{}, procs.SystemClock{}, log)
}

// WithProcesses makes KillUser, RemoveUser and the link drop run the
// loops of l instead of the guest's own process table. main passes the
// Loops its stop loop at start ran on, so the processes that loop
// stopped are known.
func WithProcesses(l *procs.Loops) Option { return func(s *service) { s.loops = l } }

// WithRemoval makes RemoveUser empty a home with e, and KillUser keep
// its records on the root open returns, instead of the guest's own.
func WithRemoval(e users.Emptier, open func() (guestusers.Root, error)) Option {
	return func(s *service) { s.emptier, s.openRoot = e, open }
}

// UnlockUser unlocks a project user for a session start
// (S13-01-process-hardening, "Locked users"; SEC08-proj-isolation). It
// refuses a user wb-guestd hasn't ensured since it started, or one
// whose removal or kill runs, with FAILED_PRECONDITION and the rule, and the
// user stays locked.
func (s *service) UnlockUser(ctx context.Context, req *guestv1.UnlockUserRequest) (*guestv1.UnlockUserResponse, error) {
	if rule := s.users.Unlock(ctx, req.GetUid()); rule != "" {
		return nil, withRule(codes.FailedPrecondition, rule)
	}
	return &guestv1.UnlockUserResponse{}, nil
}

// KillUser locks a project user, runs the kill loop, and keeps the
// result as the user's cleanup record before it answers
// (S06-10-leftover-processes, "Kill IDs and records";
// SEC08-proj-isolation). It refuses a request out of form with
// INVALID_ARGUMENT and RuleKillRequest, and a UID that wb-guestd knows
// for another project with FAILED_PRECONDITION and users.RuleUnknown,
// before it locks or kills anything. While a removal of the UID runs, it
// refuses with FAILED_PRECONDITION and users.RuleRemovalRunning: the
// removal runs its own kill loop, and a second one could end the
// removal's child. UnlockUser refuses the user until the kill ends. A record it couldn't keep fails
// the call with the record's rule, so wb-hostd never takes a result
// that isn't kept: the kill stays pending and fails closed.
func (s *service) KillUser(ctx context.Context, req *guestv1.KillUserRequest) (*guestv1.KillUserResponse, error) {
	uid, project := req.GetUid(), req.GetProjectId()
	if field := killRequestField(req); field != "" {
		s.log.LogAttrs(ctx, slog.LevelWarn, "kill request refused",
			slog.String("rule", RuleKillRequest), slog.String("decision", "refuse"),
			slog.String("attribute", field), slog.Uint64("uid", uint64(uid)))
		return nil, withRule(codes.InvalidArgument, RuleKillRequest)
	}
	if u, ok := s.users.Known(uid); ok && u.ProjectID != project {
		s.log.LogAttrs(ctx, slog.LevelWarn, "kill request refused",
			slog.String("rule", users.RuleUnknown), slog.String("decision", "refuse"),
			slog.String("found", "a known user of another project"), slog.Uint64("uid", uint64(uid)))
		return nil, withRule(codes.FailedPrecondition, users.RuleUnknown)
	}
	if rule := s.users.StartKill(ctx, uid); rule != "" {
		return nil, withRule(codes.FailedPrecondition, rule)
	}
	defer s.users.EndKill(uid)
	res := s.loops.Kill(ctx, uid)
	rec := cleanup.Record{KillID: req.GetKillId(), ProjectID: project, Left: res.Left}
	if rule := s.keepRecord(ctx, uid, rec); rule != "" {
		return nil, withRule(codes.FailedPrecondition, rule)
	}
	resp := &guestv1.KillUserResponse{KillId: rec.KillID, ProjectId: project, Result: guestv1.KillResult_KILL_RESULT_CLEAN}
	if !res.Clean() {
		resp.Result, resp.ProcessesLeft = guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, res.Left
	}
	return resp, nil
}

// killRequestField names the field of req out of the form wb-hostd
// sends, or returns "".
func killRequestField(req *guestv1.KillUserRequest) string {
	switch {
	case req.GetKillId() == 0:
		return "kill_id"
	case !projectuser.ValidProjectID(req.GetProjectId()):
		return "project_id"
	case !projectuser.InRange(req.GetUid()):
		return "uid"
	}
	return ""
}

// keepRecord writes rec as uid's cleanup record, in the records'
// directory on the data volume, and returns the rule that stopped it,
// or "". The data mount is checked first and the root tied to it, as
// for a user (S06-05-disks, "Before every session start").
func (s *service) keepRecord(ctx context.Context, uid uint32, rec cleanup.Record) string {
	if r := s.dataMount.Check(ctx); r != nil {
		return r.Rule
	}
	root, err := s.openRoot()
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "cleanup record not written",
			slog.String("rule", users.RuleHomeRoot), slog.String("error", err.Error()))
		return users.RuleHomeRoot
	}
	defer func() { _ = root.Close() }()
	if rule, _ := s.sameMount(ctx, root); rule != "" {
		return rule
	}
	store, err := cleanup.Open(ctx, root, 0, s.log)
	if err != nil {
		return cleanup.RuleDir
	}
	defer func() { _ = store.Close() }()
	if err := store.Write(ctx, uid, rec); err != nil {
		return cleanup.RuleWrite
	}
	return ""
}

// RemoveUser removes a project user's home and retires its record,
// which stays locked (S06-08-projects-and-sessions, "Removing a home";
// SEC08-proj-isolation). The data mount is checked first, and when that
// fails the removal is refused with the check's rule: no home is read or
// removed without the volume at HomePath. users.Manager.Remove does the
// rest.
func (s *service) RemoveUser(ctx context.Context, req *guestv1.RemoveUserRequest) (*guestv1.RemoveUserResponse, error) {
	resp := &guestv1.RemoveUserResponse{ProjectId: req.GetProjectId(), Uid: req.GetUid()}
	// A refused removal is an answer, not a failed call: wb-hostd keeps
	// the pending removal and logs the rule.
	var res users.RemoveResult
	if r := s.dataMount.Check(ctx); r != nil {
		res = users.RemoveResult{Outcome: users.Refused, Rule: r.Rule}
	} else {
		res = s.users.Remove(ctx, users.Removal{
			ProjectID: req.GetProjectId(), UID: req.GetUid(), GID: req.GetGid(), Home: req.GetHomePath(),
		}, users.RemoveTools{Kill: s.loops, Empty: s.emptier, Check: s.sameMount})
	}
	switch res.Outcome {
	case users.Removed:
		resp.Result = guestv1.RemoveResult_REMOVE_RESULT_REMOVED
	case users.ProcessesLeft:
		resp.Result, resp.ProcessesLeft = guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, res.Left
	case users.Refused:
		resp.Result, resp.Rule = guestv1.RemoveResult_REMOVE_RESULT_REFUSED, res.Rule
	}
	return resp, nil
}
