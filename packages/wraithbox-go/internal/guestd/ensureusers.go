package guestd

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// WithUsers makes EnsureUsers make and check users with m instead of
// the guest's own directory and volume.
func WithUsers(m *users.Manager) Option { return func(s *service) { s.users = m } }

// systemUsers is the Manager of the guest's own directory and the data
// volume at HomePath.
func systemUsers(log *slog.Logger) *users.Manager {
	return users.New(&guestusers.System{Log: log},
		func() (guestusers.Root, error) { return guestusers.OpenRoot(HomePath) }, log)
}

// EnsureUsers makes each project user of the request exist with its
// values, or refuses it with a rule (S06-03-image-updates, "Creation").
// A malformed request is refused as a whole with INVALID_ARGUMENT and
// users.RuleRequest. Otherwise the data mount is checked first, and
// when that fails every user is refused with the check's rule: a user
// is never made or checked without the volume at HomePath
// (S06-05-disks, "Before every session start"; SEC08-proj-isolation).
func (s *service) EnsureUsers(ctx context.Context, req *guestv1.EnsureUsersRequest) (*guestv1.EnsureUsersResponse, error) {
	us := make([]users.User, 0, len(req.GetUsers()))
	for _, p := range req.GetUsers() {
		us = append(us, users.User{
			ProjectID: p.GetProjectId(),
			Name:      p.GetUserName(),
			UID:       p.GetUid(),
			GID:       p.GetGid(),
			GroupName: p.GetGroupName(),
			UserGUID:  p.GetUserGeneratedUid(),
			GroupGUID: p.GetGroupGeneratedUid(),
			Home:      p.GetHomePath(),
		})
	}
	if err := users.CheckRequest(us); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn, "project users request refused",
			slog.String("rule", users.RuleRequest), slog.String("decision", "refuse"),
			slog.String("found", err.Error()))
		return nil, withRule(codes.InvalidArgument, users.RuleRequest)
	}
	var results []users.Result
	if r := s.dataMount.Check(ctx); r != nil {
		results = s.users.Refuse(ctx, us, r.Rule)
	} else {
		results = s.users.Ensure(ctx, us)
	}
	resp := &guestv1.EnsureUsersResponse{Results: make([]*guestv1.EnsureUserResult, 0, len(results))}
	for _, r := range results {
		res := &guestv1.EnsureUserResult{Uid: r.UID, Result: guestv1.EnsureResult_ENSURE_RESULT_OK}
		if r.Rule != "" {
			res.Result, res.Rule = guestv1.EnsureResult_ENSURE_RESULT_REFUSED, r.Rule
		}
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}
