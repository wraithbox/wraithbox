package hostservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// The rules of ProjectService. The identity check's are
// project.RuleURLsChanged and project.RuleRootCommitGone, and a
// session start refused for git has the rule of wb-hostd's git check
// (hostgit).
const (
	// RuleProjectOverCap: a field over its cap (S04-00-architecture,
	// "Input from `wb`").
	RuleProjectOverCap = "project-request-over-cap"
	// RuleProjectForm: a field that isn't in its form, such as a key
	// path that isn't absolute or a root commit that isn't an object ID.
	RuleProjectForm = "project-request-form"
	// RuleProjectRealm: a key in a realm other than native.
	RuleProjectRealm = "project-realm-unsupported"
	// RuleProjectIDForm: a project id that isn't 32 lowercase hex
	// characters.
	RuleProjectIDForm = "project-id-form"
	// RuleProjectNameInvalid: a name project.ValidName refuses.
	RuleProjectNameInvalid = "project-name-invalid"
	// RuleProjectPurpose: a lookup that names no purpose.
	RuleProjectPurpose = "project-purpose-unspecified"
	// RuleProjectPlacement: a placement that is neither work nor
	// isolated where one is needed.
	RuleProjectPlacement = "project-placement-unspecified"
	// RuleProjectUnknown: no project has the id or name.
	RuleProjectUnknown = "project-unknown"
	// RuleProjectNameAmbiguous: more than one project has the name.
	RuleProjectNameAmbiguous = "project-name-ambiguous"
	// RuleProjectKeyTaken: a registration of a key that has a project,
	// or a move onto a key another project has.
	RuleProjectKeyTaken = "project-key-taken"
	// RuleProjectPlaceHasHome: a change of a registered project's
	// placement, which waits for I232.
	RuleProjectPlaceHasHome = "project-place-has-home"
	// RuleGitUnchecked: a session start when wb-hostd has no result of
	// its git check.
	RuleGitUnchecked = "hostd-git-unchecked"
)

// The rules of an accepted project request, for the log.
const (
	ruleProjectFound      = "project-found"
	ruleProjectUnknownKey = "project-key-unknown"
	ruleProjectRegistered = "project-registered"
	ruleProjectGrown      = "project-identity-grown"
	ruleProjectConfirmed  = "project-confirmed"
	ruleProjectMoved      = "project-moved"
	ruleProjectRenamed    = "project-renamed"
	ruleProjectUnchanged  = "project-unchanged"
	ruleProjectRemoved    = "project-removed"
)

// idAttempts bounds the draws of a new project id. Two equal ids of 128
// random bits are not expected at all, so a second collision means a
// broken random source.
const idAttempts = 4

// ProjectStore is state.db as the service uses it. *state.DB implements
// it.
type ProjectStore interface {
	Projects(ctx context.Context, fn func(*state.ProjectTx) error) error
}

// ProjectServer serves wraithbox.hostd.v1.ProjectService: the project
// records of S05-04-naming. wb resolves the key and the identity, and
// wb-hostd checks each field of a request before it reads or writes
// state.db (S04-00-architecture, "Input from `wb`").
type ProjectServer struct {
	hostdv1.UnimplementedProjectServiceServer

	Store ProjectStore
	// Git is wb-hostd's git check at start. A session start is refused
	// while it hasn't passed, or is nil (S08-08-host-git, "Where it is
	// checked"). The purpose of a lookup comes from wb, so it gates
	// nothing on its own: the RPC that starts a session and the push
	// path check Git themselves, never trusting that a session-start
	// lookup came first (SEC09-host-policy).
	Git *hostdv1.HostGit
	// Refusing is set in refusing mode, and every call is then refused
	// with its rule.
	Refusing *startup.Refusing
	// Audit gets the 5019 record of each project change (S09-11-audit,
	// "Project settings"). Every call is refused without it, as without
	// Store.
	Audit Recorder
	// Log gets the decisions. nil takes slog.Default().
	Log *slog.Logger
	// Rand draws project ids. nil takes crypto/rand.
	Rand io.Reader
	// Now is the clock of the created time. nil takes time.Now.
	Now func() time.Time
	// UsersRemoved, when set, is called after RemoveProject committed
	// the project's user removals, to send them to the running VMs now
	// (S05-04-naming, "The project user"). wb-hostd passes
	// guestconn.Links.SendUserRemovals. It must not block.
	UsersRemoved func()
}

var _ hostdv1.ProjectServiceServer = (*ProjectServer)(nil)

// LookupProject finds the project of a key and runs the identity check,
// or lists the matches of an unknown key (S05-04-naming, "Identity
// check" and "Registration").
func (s *ProjectServer) LookupProject(ctx context.Context, req *hostdv1.LookupProjectRequest) (*hostdv1.LookupProjectResponse, error) {
	const method = "LookupProject"
	current, err := s.lookupInput(req)
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	sessionStart := req.GetPurpose() == hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START
	out := &hostdv1.LookupProjectResponse{Git: s.Git}
	var grown *change
	err = s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		row, ok, err := tx.ByKey(req.GetKey().GetRealm(), req.GetKey().GetPath())
		if err != nil {
			return err
		}
		if !ok {
			all, err := tx.All()
			if err != nil {
				return err
			}
			matches, count, _ := matchesOf(all, current)
			out.Result = &hostdv1.LookupProjectResponse_Unknown{Unknown: &hostdv1.UnknownKey{Matches: matches, MatchCount: count}}
			return nil
		}
		out.Outcome, grown, err = checkRecorded(tx, row, current, sessionStart)
		if grown != nil {
			row = *grown.after
		}
		out.Result = &hostdv1.LookupProjectResponse_Project{Project: projectProto(row)}
		return err
	})
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	s.record(ctx, grown)
	if p := out.GetProject(); p != nil {
		s.accept(ctx, method, ruleProjectFound, slog.String("project", p.GetId()), slog.String("outcome", out.GetOutcome().String()))
	} else {
		s.accept(ctx, method, ruleProjectUnknownKey, slog.Uint64("matches", uint64(out.GetUnknown().GetMatchCount())))
	}
	return out, nil
}

// lookupInput admits a lookup and checks its fields. A session start
// is refused while the git check hasn't passed.
func (s *ProjectServer) lookupInput(req *hostdv1.LookupProjectRequest) (project.Identity, error) {
	if err := s.admit(); err != nil {
		return project.Identity{}, err
	}
	if err := checkKey(req.GetKey()); err != nil {
		return project.Identity{}, err
	}
	current, err := checkIdentity(req.GetIdentity())
	if err != nil {
		return project.Identity{}, err
	}
	switch req.GetPurpose() {
	case hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START:
		if err := s.gitPassed(); err != nil {
			return project.Identity{}, err
		}
		return current, nil
	case hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND:
		return current, nil
	case hostdv1.LookupPurpose_LOOKUP_PURPOSE_UNSPECIFIED:
	}
	// Unspecified, or a value the enum doesn't have.
	return project.Identity{}, &projectRefusal{
		code: codes.InvalidArgument, rule: RuleProjectPurpose,
		msg: "the lookup names no purpose: name a session start or a command",
	}
}

// checkRecorded runs the identity check on row (S05-04-naming,
// "Identity check"). A refusal holds both identities. At a session
// start an empty recorded identity is replaced with a non-empty current
// one (S05-04-naming, "Grow-only"), and the change is returned to be
// recorded. Without this check a different repository cloned into the
// project's directory would inherit its approvals and policy
// (SEC08-proj-isolation).
func checkRecorded(tx *state.ProjectTx, row state.Project, current project.Identity, sessionStart bool) (hostdv1.IdentityOutcome, *change, error) {
	recorded := identityOf(row)
	if rule := project.CheckIdentity(recorded, current); rule != project.RuleIdentityMatched {
		return 0, nil, mismatch(row, rule, current)
	}
	if !recorded.Empty() {
		return hostdv1.IdentityOutcome_IDENTITY_OUTCOME_MATCHED, nil, nil
	}
	if !sessionStart || current.Empty() {
		return hostdv1.IdentityOutcome_IDENTITY_OUTCOME_EMPTY_RECORDED, nil, nil
	}
	after := row
	after.URLs, after.RootCommits = current.URLs, current.RootCommits
	if err := tx.Update(after); err != nil {
		return 0, nil, err
	}
	c := &change{command: "wb claude", rule: ruleProjectGrown, before: &row, after: &after}
	return hostdv1.IdentityOutcome_IDENTITY_OUTCOME_GROWN, c, nil
}

func mismatch(row state.Project, rule project.Rule, current project.Identity) *projectRefusal {
	what := "its remote URLs changed"
	if rule == project.RuleRootCommitGone {
		what = "a recorded root commit is gone"
	}
	return &projectRefusal{
		code: codes.FailedPrecondition, rule: string(rule),
		msg: fmt.Sprintf("the repository of project %s is not the one recorded: %s. If it is the same repository, run wb project confirm", row.Name, what),
		mismatch: &hostdv1.IdentityMismatch{
			ProjectId: row.ID, ProjectName: row.Name,
			Recorded: identityProto(identityOf(row)), Current: identityProto(current),
		},
	}
}

// RegisterProject registers an unknown key with a new random id
// (S05-04-naming, "Registration"). The matching runs again in the
// insert's transaction. The project starts isolated when any match is
// isolated or the request asks for it, and copies nothing else from a
// match, never config_trust (T13-new-project-grants).
func (s *ProjectServer) RegisterProject(ctx context.Context, req *hostdv1.RegisterProjectRequest) (*hostdv1.RegisterProjectResponse, error) {
	const method = "RegisterProject"
	row, err := s.registerInput(req)
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	out := &hostdv1.RegisterProjectResponse{}
	reason := "requested"
	if req.GetPlacement() == hostdv1.Placement_PLACEMENT_UNSPECIFIED {
		reason = "default"
	}
	err = s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		if err := keyFree(tx, row.Realm, row.KeyPath, ""); err != nil {
			return err
		}
		all, err := tx.All()
		if err != nil {
			return err
		}
		var isolated bool
		out.Matches, out.MatchCount, isolated = matchesOf(all, identityOf(row))
		if isolated && row.Placement != state.PlacementIsolated {
			row.Placement, reason = state.PlacementIsolated, "isolated-match"
		}
		return s.insert(tx, &row)
	})
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	s.accept(ctx, method, ruleProjectRegistered, slog.String("project", row.ID), slog.String("location", row.KeyPath),
		slog.String("placement", row.Placement), slog.String("placement_reason", reason),
		slog.Uint64("matches", uint64(out.GetMatchCount())))
	out.Project = projectProto(row)
	return out, nil
}

func (s *ProjectServer) registerInput(req *hostdv1.RegisterProjectRequest) (state.Project, error) {
	if err := s.admit(); err != nil {
		return state.Project{}, err
	}
	if err := checkKey(req.GetKey()); err != nil {
		return state.Project{}, err
	}
	current, err := checkIdentity(req.GetIdentity())
	if err != nil {
		return state.Project{}, err
	}
	if err := checkName(req.GetName()); err != nil {
		return state.Project{}, err
	}
	placement, err := placementOf(req.GetPlacement(), state.PlacementWork)
	if err != nil {
		return state.Project{}, err
	}
	return state.Project{
		Realm: req.GetKey().GetRealm(), KeyPath: req.GetKey().GetPath(), Name: req.GetName(),
		Placement: placement, URLs: current.URLs, RootCommits: current.RootCommits, Created: s.now(),
	}, nil
}

// insert inserts row with a new id. The id is drawn again when another
// project has it.
func (s *ProjectServer) insert(tx *state.ProjectTx, row *state.Project) error {
	for range idAttempts {
		id, err := s.newID()
		if err != nil {
			return err
		}
		row.ID = id
		err = tx.Insert(*row)
		if !errors.Is(err, state.ErrProjectIDTaken) {
			return err
		}
	}
	return fmt.Errorf("no free project id in %d draws", idAttempts)
}

// newID draws a project id: 128 bits from the random source, never
// derived from the key (S05-04-naming, "Project id").
func (s *ProjectServer) newID() (string, error) {
	r := s.Rand
	if r == nil {
		r = rand.Reader
	}
	var b [projectIDLen / 2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("drawing a project id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// keyFree refuses a key that a project other than self has, and names
// that project (S05-04-naming, "Moving").
func keyFree(tx *state.ProjectTx, realm, path, self string) error {
	other, ok, err := tx.ByKey(realm, path)
	if err != nil || !ok || other.ID == self {
		return err
	}
	return &projectRefusal{
		code: codes.FailedPrecondition, rule: RuleProjectKeyTaken,
		msg: fmt.Sprintf("project %s (%s) has the key %s. wb project move never replaces a project: remove it with wb project rm first",
			other.Name, other.ID, path),
	}
}

// GetProject returns a project by id or name, without the identity
// check. A wb command given --project uses it.
func (s *ProjectServer) GetProject(ctx context.Context, req *hostdv1.GetProjectRequest) (*hostdv1.GetProjectResponse, error) {
	const method = "GetProject"
	if err := s.admit(); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	if err := checkGet(req); err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	var row state.Project
	err := s.Store.Projects(ctx, func(tx *state.ProjectTx) error {
		var err error
		row, err = byIDOrName(tx, req)
		return err
	})
	if err != nil {
		return nil, s.refuse(ctx, method, err)
	}
	s.accept(ctx, method, ruleProjectFound, slog.String("project", row.ID))
	return &hostdv1.GetProjectResponse{Project: projectProto(row)}, nil
}

// checkGet checks the id or name a GetProject request names, before
// the transaction takes state.db's write lock.
func checkGet(req *hostdv1.GetProjectRequest) error {
	switch by := req.GetBy().(type) {
	case *hostdv1.GetProjectRequest_Id:
		return checkID(by.Id)
	case *hostdv1.GetProjectRequest_Name:
		return checkName(by.Name)
	}
	return badForm("the request names neither a project id nor a name")
}

// byIDOrName returns the project of a request that checkGet passed.
func byIDOrName(tx *state.ProjectTx, req *hostdv1.GetProjectRequest) (state.Project, error) {
	if by, ok := req.GetBy().(*hostdv1.GetProjectRequest_Name); ok {
		return byName(tx, by.Name)
	}
	return byID(tx, req.GetId())
}

// byID returns the project of an id that checkID passed.
func byID(tx *state.ProjectTx, id string) (state.Project, error) {
	row, ok, err := tx.ByID(id)
	if err != nil {
		return state.Project{}, err
	}
	if !ok {
		return state.Project{}, unknown("id " + id)
	}
	return row, nil
}

// byName returns the one project named name, a name checkName passed.
func byName(tx *state.ProjectTx, name string) (state.Project, error) {
	rows, err := tx.ByName(name)
	switch {
	case err != nil:
		return state.Project{}, err
	case len(rows) == 0:
		return state.Project{}, unknown("name " + name)
	case len(rows) > 1:
		ids := make([]string, 0, len(rows))
		for _, r := range rows[:min(len(rows), 8)] {
			ids = append(ids, r.ID)
		}
		return state.Project{}, &projectRefusal{
			code: codes.FailedPrecondition, rule: RuleProjectNameAmbiguous,
			msg: fmt.Sprintf("%d projects are named %s: name one by its id (%s)", len(rows), name, strings.Join(ids, ", ")),
		}
	}
	return rows[0], nil
}

func unknown(what string) *projectRefusal {
	return &projectRefusal{code: codes.NotFound, rule: RuleProjectUnknown, msg: "no project has the " + what}
}

// admit refuses every call in refusing mode, and without a store or an
// audit writer.
func (s *ProjectServer) admit() error {
	if r := s.Refusing; r != nil {
		return &projectRefusal{
			code: codes.FailedPrecondition, rule: r.Rule,
			msg: "wb-hostd is in refusing mode, and answers only wb status: " + r.Detail,
		}
	}
	if s.Store == nil {
		return &projectRefusal{code: codes.Internal, rule: RuleInternal, msg: "wb-hostd has no state database"}
	}
	if s.Audit == nil {
		return &projectRefusal{code: codes.Internal, rule: RuleInternal, msg: "wb-hostd has no audit log"}
	}
	return nil
}

func (s *ProjectServer) logger() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *ProjectServer) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}
