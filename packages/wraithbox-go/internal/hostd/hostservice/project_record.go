package hostservice

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// projectRefusal is a refused project request: the status code, the
// rule, the message for the user, and the IdentityMismatch of a refused
// identity check.
type projectRefusal struct {
	code     codes.Code
	rule     string
	msg      string
	mismatch *hostdv1.IdentityMismatch
}

func (r *projectRefusal) Error() string { return r.msg }

// status builds the refusal's status: msg, the rule as the ErrorInfo
// reason, and the mismatch.
func (r *projectRefusal) status() error {
	info := &errdetails.ErrorInfo{Reason: r.rule, Domain: Domain}
	st := status.New(r.code, r.msg)
	detailed, err := st.WithDetails(info)
	if r.mismatch != nil {
		detailed, err = st.WithDetails(info, r.mismatch)
	}
	if err != nil {
		return withRule(r.code, r.rule, r.msg)
	}
	return detailed.Err()
}

// refuse logs a refusal with its rule and the calling wb, and returns
// its status. An error that isn't a *projectRefusal, such as one of
// state.db, is refused with RuleInternal.
func (s *ProjectServer) refuse(ctx context.Context, method string, err error) error {
	r, ok := errors.AsType[*projectRefusal](err)
	if !ok {
		r = &projectRefusal{code: codes.Internal, rule: RuleInternal, msg: "wb-hostd failed: " + err.Error()}
	}
	attrs := append([]slog.Attr{
		slog.String("rule", r.rule), slog.String("decision", "refuse"),
		slog.String("method", method), slog.String("reason", r.msg),
	}, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelWarn, "project request refused", attrs...)
	return r.status()
}

// accept logs an accepted request with the rule that decided it.
func (s *ProjectServer) accept(ctx context.Context, method, rule string, attrs ...slog.Attr) {
	attrs = append(append([]slog.Attr{
		slog.String("rule", rule), slog.String("decision", "accept"), slog.String("method", method),
	}, attrs...), peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelInfo, "project request", attrs...)
}

// change is one change of a project, recorded once its transaction has
// committed. after is nil for a removal.
type change struct {
	command       string
	rule          string
	before, after *state.Project
}

// record logs a change and writes its Device Config State Change (5019)
// audit record: the project, the command, its values before and after,
// and the calling wb (S09-11-audit, "Project settings"). It is the one
// place a project change is recorded.
func (s *ProjectServer) record(ctx context.Context, c *change) {
	if c == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("decision", "accept"), slog.String("rule", c.rule),
		slog.String("command", c.command), slog.String("project", c.before.ID),
		slog.Any("before", projectGroup(c.before)),
	}
	if c.after != nil {
		attrs = append(attrs, slog.Any("after", projectGroup(c.after)))
	}
	attrs = append(attrs, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelInfo, "project changed", attrs...)
	pc := audit.ProjectChange{ID: c.before.ID, Before: projectState(c.before)}
	if c.after != nil {
		pc.After = projectState(c.after)
	}
	s.Audit.Record(audit.ProjectChanged(c.command, c.rule, pc, auditPeer(ctx)))
}

// projectState is a project's settings as its audit record holds them.
func projectState(p *state.Project) *audit.ProjectState {
	return &audit.ProjectState{
		Realm: p.Realm, Location: p.KeyPath, Name: p.Name, Placement: p.Placement,
		ConfigTrust: p.ConfigTrust, URLs: nonNilList(p.URLs), RootCommits: nonNilList(p.RootCommits),
	}
}

func projectGroup(p *state.Project) slog.Value {
	return slog.GroupValue(
		slog.String("realm", p.Realm), slog.String("location", p.KeyPath), slog.String("name", p.Name),
		slog.String("placement", p.Placement), slog.Bool("config_trust", p.ConfigTrust),
		slog.Any("urls", nonNilList(p.URLs)), slog.Any("root_commits", nonNilList(p.RootCommits)),
	)
}

// nonNilList logs a missing list as [], not null.
func nonNilList(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func identityOf(p state.Project) project.Identity {
	return project.Identity{URLs: p.URLs, RootCommits: p.RootCommits}
}

func identityProto(id project.Identity) *hostdv1.ProjectIdentity {
	return &hostdv1.ProjectIdentity{Urls: id.URLs, RootCommits: id.RootCommits}
}

func placementProto(p string) hostdv1.Placement {
	switch p {
	case state.PlacementWork:
		return hostdv1.Placement_PLACEMENT_WORK
	case state.PlacementIsolated:
		return hostdv1.Placement_PLACEMENT_ISOLATED
	}
	return hostdv1.Placement_PLACEMENT_UNSPECIFIED
}

func keyProto(p state.Project) *hostdv1.ProjectKey {
	return &hostdv1.ProjectKey{Realm: p.Realm, Path: p.KeyPath}
}

func projectProto(p state.Project) *hostdv1.Project {
	return &hostdv1.Project{
		Id: p.ID, Name: p.Name, Key: keyProto(p), Placement: placementProto(p.Placement),
		ConfigTrust: p.ConfigTrust, Identity: identityProto(identityOf(p)), CreatedUnix: p.Created.Unix(),
	}
}

// maxMatchesShown bounds the matches a response lists. match_count has
// the number.
const maxMatchesShown = 64

// matchesOf lists the projects whose identity matches current, oldest
// first (S05-04-naming, "Registration"), at most maxMatchesShown of
// them, with their number, and whether any of them is isolated.
func matchesOf(all []state.Project, current project.Identity) ([]*hostdv1.ProjectMatch, uint32, bool) {
	var (
		shown    []*hostdv1.ProjectMatch
		count    uint32
		isolated bool
	)
	for _, p := range all {
		if !project.Matches(identityOf(p), current) {
			continue
		}
		count++
		isolated = isolated || p.Placement == state.PlacementIsolated
		if len(shown) < maxMatchesShown {
			shown = append(shown, &hostdv1.ProjectMatch{
				Id: p.ID, Name: p.Name, Key: keyProto(p), Placement: placementProto(p.Placement),
			})
		}
	}
	return shown, count, isolated
}
