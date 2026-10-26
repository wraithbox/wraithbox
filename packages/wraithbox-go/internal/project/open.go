package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/status"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// The rules of wb's side of a project lookup.
const (
	// RuleGitMismatch: the git binary wb-hostd reports isn't the one wb
	// resolved (S08-08-host-git, "Which git").
	RuleGitMismatch Rule = "project-git-mismatch"
	// RuleMatchMoved: the repository matches a project whose location
	// no longer holds its repository, so that project may have moved
	// here (S05-04-naming, "Registration").
	RuleMatchMoved Rule = "project-match-moved"
	// RuleNotRegistered: the directory's key has no project, and the
	// command doesn't register one.
	RuleNotRegistered Rule = "project-not-registered"
	// RuleHostdAnswer: wb-hostd's answer lacks a part it must have.
	RuleHostdAnswer Rule = "project-hostd-answer"
	// RuleOpened: a session start or wb project place found or
	// registered its project.
	RuleOpened Rule = "project-opened"
)

// Opener is wb's way into a project: the session-start entry of
// S05-04-naming, and the lookups of the `wb project` commands. It
// resolves the directory and the identity with wb's own git, and asks
// wb-hostd for the records. It never runs git on a path wb-hostd
// reports, nor a binary wb-hostd names (S04-06-git-confinement).
type Opener struct {
	// Paths are wb's <config>, <data> and <logs>.
	Paths paths.Paths
	// Hostd is wb-hostd's ProjectService.
	Hostd hostdv1.ProjectServiceClient
	// Log gets every decision. Nil is slog.Default().
	Log *slog.Logger
}

// Local is a directory's repository as wb read it: wb's checked git,
// the resolved repository, and its current identity.
type Local struct {
	Git      string
	Repo     *Repo
	Identity Identity
}

// Opened is the project a session start or wb project place opened.
type Opened struct {
	*Local
	Project *hostdv1.Project
	// Dir is the project directory <data>/projects/<id>.
	Dir string
	// Registered is set when this call registered the project. Matches
	// and MatchCount then list the projects whose identity matches, for
	// the registration notice.
	Registered bool
	Matches    []*hostdv1.ProjectMatch
	MatchCount uint32
	// Outcome is the identity check's outcome for a project that was
	// registered already.
	Outcome hostdv1.IdentityOutcome
}

func (o *Opener) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

// Git resolves wb's git from the git key of config.toml and checks its
// version (S08-08-host-git, "Where it is checked"). It returns the
// binary's absolute path. A refused settings file is a *config.Refusal,
// and a refused git a *hostgit.Refusal, each logged with its rule.
func (o *Opener) Git(ctx context.Context) (string, error) {
	cfg, err := config.Load(ctx, o.Paths.Config)
	if err != nil {
		return "", err
	}
	res, err := hostgit.Check(ctx, cfg.Git)
	if err != nil {
		return "", err
	}
	return res.Binary, nil
}

// Read checks wb's git, resolves dir, and reads the repository's
// identity: the first two steps of a session start.
func (o *Opener) Read(ctx context.Context, dir string) (*Local, error) {
	git, err := o.Git(ctx)
	if err != nil {
		return nil, err
	}
	r := &Resolver{Git: git, Own: o.Paths, Log: o.Log}
	repo, err := r.Resolve(ctx, dir)
	if err != nil {
		return nil, err
	}
	id, err := r.Identity(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &Local{Git: git, Repo: repo, Identity: id}, nil
}

// Lookup asks wb-hostd for the project of l's key, with the identity
// check, and compares wb-hostd's git with wb's: a different binary is
// refused with RuleGitMismatch (S08-08-host-git, "Which git"). A
// refusal of wb-hostd is its gRPC status, with the rule as the
// ErrorInfo reason.
func (o *Opener) Lookup(ctx context.Context, l *Local, purpose hostdv1.LookupPurpose) (*hostdv1.LookupProjectResponse, error) {
	res, err := o.Hostd.LookupProject(ctx, &hostdv1.LookupProjectRequest{
		Key: KeyProto(l.Repo.Key), Identity: IdentityProto(l.Identity), Purpose: purpose,
	})
	if err != nil {
		return nil, err
	}
	if g := res.GetGit(); g.GetRule() != string(hostgit.RuleVersionAccepted) {
		ref := &Refusal{Rule: Rule(g.GetRule()), msg: "wb-hostd's git check failed: " + g.GetMessage()}
		if ref.Rule == "" {
			ref.Rule, ref.msg = RuleHostdAnswer, "wb-hostd sent no result of its git check"
		}
		o.refused(ctx, l.Repo.Dir, ref)
		return nil, ref
	}
	if theirs := res.GetGit().GetBinary(); theirs != l.Git {
		ref := &Refusal{
			Rule: RuleGitMismatch,
			msg: fmt.Sprintf("wb runs git at %q, and wb-hostd runs git at %q. Both take it from the git setting in %q: "+
				"when it changed, restart wb-hostd with wb setup", l.Git, theirs, filepath.Join(o.Paths.Config, config.FileName)),
		}
		o.refused(ctx, l.Repo.Dir, ref)
		return nil, ref
	}
	if res.GetProject() == nil && res.GetUnknown() == nil {
		return nil, o.badAnswer(ctx, l.Repo.Dir, "a lookup answer with neither a project nor a list of matches")
	}
	return res, nil
}

// Open is the session-start entry of S05-04-naming. In order, it:
//
//  1. resolves wb's git from the git key and checks its version;
//  2. resolves dir's key and reads its identity;
//  3. asks wb-hostd for the key's project, for a session start;
//  4. refuses when wb-hostd's git isn't wb's;
//  5. for an unknown key, refuses when a matching project moved away,
//     and otherwise registers the key and creates the project
//     directory.
//
// A registered project whose directory is gone gets it again.
func (o *Opener) Open(ctx context.Context, dir string) (*Opened, error) {
	return o.open(ctx, dir, hostdv1.LookupPurpose_LOOKUP_PURPOSE_SESSION_START, hostdv1.Placement_PLACEMENT_UNSPECIFIED)
}

// OpenToPlace is Open for wb project place: a lookup for a command, and
// an unknown key registered with placement
// (S05-03-management-commands, "`wb project place`"). The caller sets
// the placement of a project that was registered already.
func (o *Opener) OpenToPlace(ctx context.Context, dir string, placement hostdv1.Placement) (*Opened, error) {
	return o.open(ctx, dir, hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND, placement)
}

func (o *Opener) open(ctx context.Context, dir string, purpose hostdv1.LookupPurpose, placement hostdv1.Placement) (*Opened, error) {
	l, err := o.Read(ctx, dir)
	if err != nil {
		return nil, err
	}
	res, err := o.Lookup(ctx, l, purpose)
	if err != nil {
		return nil, err
	}
	out := &Opened{Local: l, Project: res.GetProject(), Outcome: res.GetOutcome()}
	if out.Project == nil {
		if err := o.register(ctx, out, res.GetUnknown(), placement); err != nil {
			return nil, err
		}
	}
	out.Dir, err = EnsureDir(o.Paths.Data, out.Project.GetId())
	if err != nil {
		o.refused(ctx, l.Repo.Dir, refusalOf(err))
		return nil, err
	}
	o.log().LogAttrs(ctx, slog.LevelInfo, "project opened",
		slog.String("rule", string(RuleOpened)), slog.String("decision", "accept"),
		slog.String("project", out.Project.GetId()), slog.Bool("registered", out.Registered),
		slog.String("dir", l.Repo.Dir), slog.String("purpose", purpose.String()))
	return out, nil
}

// register refuses when a matching project moved away, and otherwise
// registers the key (S05-04-naming, "Registration").
func (o *Opener) register(ctx context.Context, out *Opened, unknown *hostdv1.UnknownKey, placement hostdv1.Placement) error {
	if ref := movedMatch(out.Repo.Dir, unknown.GetMatches()); ref != nil {
		o.refused(ctx, out.Repo.Dir, ref)
		return ref
	}
	reg, err := o.Hostd.RegisterProject(ctx, &hostdv1.RegisterProjectRequest{
		Key: KeyProto(out.Repo.Key), Identity: IdentityProto(out.Identity), Name: out.Repo.Name, Placement: placement,
	})
	if err != nil {
		return err
	}
	if reg.GetProject() == nil {
		return o.badAnswer(ctx, out.Repo.Dir, "a registration answer without the project")
	}
	if ref := badMatch(reg.GetMatches()); ref != nil {
		o.refused(ctx, out.Repo.Dir, ref)
		return ref
	}
	out.Project, out.Registered = reg.GetProject(), true
	out.Matches, out.MatchCount = reg.GetMatches(), reg.GetMatchCount()
	return nil
}

// movedMatch returns a refusal for the first match whose location no
// longer holds its repository, or nil when every match is in place. The
// check runs no git: the location wb-hostd reports is only given to
// Lstat (S04-06-git-confinement). A location that is gone, isn't a
// directory, or has no HEAD means that project moved, and the refusal
// names it with wb project move and wb project rm written out
// (S05-04-naming, "Registration", "Moved").
func movedMatch(dir string, matches []*hostdv1.ProjectMatch) *Refusal {
	if ref := badMatch(matches); ref != nil {
		return ref
	}
	for _, m := range matches {
		why := movedAway(m.GetKey())
		if why == "" {
			continue
		}
		return &Refusal{
			Rule: RuleMatchMoved,
			msg: fmt.Sprintf("this repository has the identity of project %s (%s), whose location %q %s, so the project may have moved here. "+
				"To keep its state, run: wb project move %s --project %s. To drop it, run: wb project rm --project %s",
				m.GetName(), m.GetId(), m.GetKey().GetPath(), why, quoteArg(dir), m.GetId(), m.GetId()),
		}
	}
	return nil
}

// badMatch refuses the first match whose id isn't an id or whose name
// isn't a name, with RuleHostdAnswer, or returns nil. Both go into the
// commands a refusal writes out for the user to paste, and into the
// registration notice, so wb-hostd's answer is checked as it would be
// from a compromised process (SEC03-no-host-exec, SEC12-least-privilege).
func badMatch(matches []*hostdv1.ProjectMatch) *Refusal {
	for _, m := range matches {
		if CheckID(m.GetId()) != nil || ValidName(m.GetName()) != nil {
			return &Refusal{
				Rule: RuleHostdAnswer,
				msg:  fmt.Sprintf("wb-hostd sent a matching project with id %q and name %q, which aren't an id and a name", m.GetId(), m.GetName()),
			}
		}
	}
	return nil
}

// movedAway says why a match's location no longer holds its
// repository, or returns "" when it does. A location this realm can't
// check counts as moved, the strict side.
func movedAway(k *hostdv1.ProjectKey) string {
	loc := k.GetPath()
	switch {
	case k.GetRealm() != string(RealmNative):
		return "is in another realm, which this host can't check"
	case !filepath.IsAbs(loc) || hasControl(loc):
		return "is not a path this host can check"
	}
	info, err := os.Lstat(loc)
	switch {
	case err != nil:
		return "is gone"
	case !info.IsDir():
		return "is not a directory"
	}
	if _, err := os.Lstat(filepath.Join(loc, "HEAD")); err != nil {
		return "holds no HEAD"
	}
	return ""
}

// quoteArg puts a path in single quotes for a command line the user
// copies, when it holds a character a POSIX shell reads.
func quoteArg(s string) string {
	for _, c := range s {
		if !isNameChar(c) && c != '/' {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// Find resolves dir and returns its registered project, after the
// identity check. A key without a project is refused with
// RuleNotRegistered, which names the commands that register one. The
// `wb project` commands but confirm and rm use it.
func (o *Opener) Find(ctx context.Context, dir string) (*Local, *hostdv1.Project, error) {
	l, err := o.Read(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	res, err := o.Lookup(ctx, l, hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND)
	if err != nil {
		return nil, nil, err
	}
	if res.GetProject() == nil {
		return nil, nil, o.notRegistered(ctx, l)
	}
	return l, res.GetProject(), nil
}

// FindID resolves dir and returns its project's id without the identity
// check, for wb project confirm and wb project rm (S05-04-naming,
// "Identity check"). A refused identity check names the project in its
// IdentityMismatch detail, and FindID takes the id from there.
func (o *Opener) FindID(ctx context.Context, dir string) (*Local, string, error) {
	l, err := o.Read(ctx, dir)
	if err != nil {
		return nil, "", err
	}
	res, err := o.Lookup(ctx, l, hostdv1.LookupPurpose_LOOKUP_PURPOSE_COMMAND)
	var id string
	switch m := Mismatch(err); {
	case m != nil:
		id = m.GetProjectId()
	case err != nil:
		return nil, "", err
	case res.GetProject() == nil:
		return nil, "", o.notRegistered(ctx, l)
	default:
		id = res.GetProject().GetId()
	}
	if err := CheckID(id); err != nil {
		o.refused(ctx, l.Repo.Dir, refusalOf(err))
		return nil, "", err
	}
	return l, id, nil
}

// Mismatch returns the IdentityMismatch detail of a refused identity
// check, or nil.
func Mismatch(err error) *hostdv1.IdentityMismatch {
	st, ok := status.FromError(err)
	if !ok || err == nil {
		return nil
	}
	for _, d := range st.Details() {
		if m, ok := d.(*hostdv1.IdentityMismatch); ok {
			return m
		}
	}
	return nil
}

func (o *Opener) notRegistered(ctx context.Context, l *Local) *Refusal {
	ref := &Refusal{
		Rule: RuleNotRegistered,
		msg:  fmt.Sprintf("the repository in %q is not a Wraith Box project yet: wb claude or wb project place registers it", l.Repo.Dir),
	}
	o.refused(ctx, l.Repo.Dir, ref)
	return ref
}

func (o *Opener) badAnswer(ctx context.Context, dir, what string) *Refusal {
	ref := &Refusal{Rule: RuleHostdAnswer, msg: "wb-hostd sent " + what}
	o.refused(ctx, dir, ref)
	return ref
}

func (o *Opener) refused(ctx context.Context, dir string, ref *Refusal) {
	if ref == nil {
		return
	}
	(&Resolver{Log: o.Log}).logRefusal(ctx, "project refused", dir, ref)
}

func refusalOf(err error) *Refusal {
	r, _ := errors.AsType[*Refusal](err)
	return r
}

// Notice is the registration notice, one line per entry, for standard
// error: the new project, and each project whose identity matches with
// its placement (S05-04-naming, "Registration", "Second clone"). It is
// empty when this call registered nothing. The lines hold values from
// the repository and from wb-hostd, which the caller strips before it
// prints them.
func (op *Opened) Notice() []string {
	if !op.Registered {
		return nil
	}
	lines := []string{fmt.Sprintf("registered project %s (%s) for %s, placement %s",
		op.Project.GetName(), op.Project.GetId(), op.Repo.Dir, PlacementName(op.Project.GetPlacement()))}
	for _, m := range op.Matches {
		lines = append(lines, fmt.Sprintf("the same repository is project %s (%s) at %s, placement %s",
			m.GetName(), m.GetId(), m.GetKey().GetPath(), PlacementName(m.GetPlacement())))
	}
	if more := int(op.MatchCount) - len(op.Matches); more > 0 {
		lines = append(lines, fmt.Sprintf("and %d more projects of the same repository", more))
	}
	return lines
}

// PlacementName is a placement as the commands print it: work,
// isolated, or unset.
func PlacementName(p hostdv1.Placement) string {
	switch p {
	case hostdv1.Placement_PLACEMENT_WORK:
		return "work"
	case hostdv1.Placement_PLACEMENT_ISOLATED:
		return "isolated"
	case hostdv1.Placement_PLACEMENT_UNSPECIFIED:
		return "unset"
	}
	return "unset"
}

// KeyProto is a key as the contract carries it.
func KeyProto(k Key) *hostdv1.ProjectKey {
	return &hostdv1.ProjectKey{Realm: string(k.Realm), Path: k.Path}
}

// IdentityProto is an identity as the contract carries it.
func IdentityProto(id Identity) *hostdv1.ProjectIdentity {
	return &hostdv1.ProjectIdentity{Urls: id.URLs, RootCommits: id.RootCommits}
}

// IdentityOf is the identity a contract message carries.
func IdentityOf(id *hostdv1.ProjectIdentity) Identity {
	return Identity{URLs: id.GetUrls(), RootCommits: id.GetRootCommits()}
}
