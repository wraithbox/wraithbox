package users

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// The rules of a removal that leaves the home, or stops before the end.
const (
	// RuleHomeChild: the child that empties the home as the user didn't
	// start, or ran past ChildDeadline. The record isn't retired, and
	// wb-hostd asks again.
	RuleHomeChild = "project-home-child-failed"
	// RuleHomeLeft: the home couldn't be removed after the child ran:
	// entries the user couldn't remove stay in it, such as a file guest
	// root put there. The removal goes on, and the home stays, owned by
	// the retired UID, which wb-hostd never hands out again.
	RuleHomeLeft = "project-home-left"
)

// The deadlines of a removal (S06-08-projects-and-sessions, "Removing a
// home"). RemoveDeadline is below wb-hostd's 60 s
// wait for the answer (internal/guestlink, RemoveTimeout). The kill
// loop takes up to procs.MaxTime of it, the child up to ChildDeadline,
// and the directory calls the rest.
const (
	RemoveDeadline = 50 * time.Second
	ChildDeadline  = 15 * time.Second
)

// RetiredShell is the shell of a removed user's record, which the
// record keeps.
const RetiredShell = "/usr/bin/false"

// Removal is a RemoveUser request: the project, and the user it had.
type Removal struct {
	ProjectID string
	UID       uint32
	GID       uint32
	Home      string
}

// Outcome is the answer to a removal.
type Outcome int

// The outcomes of a removal.
const (
	Removed Outcome = iota + 1
	ProcessesLeft
	Refused
)

// RemoveResult is the answer to a removal: Left with ProcessesLeft,
// Rule with Refused.
type RemoveResult struct {
	Outcome Outcome
	Left    uint32
	Rule    string
}

// Killer runs the kill loop of a user, and keeps the stop loop off a
// user's processes while its home is emptied: *procs.Loops.
type Killer interface {
	Kill(ctx context.Context, uid uint32) procs.KillResult
	Exclude(uid uint32) (release func())
}

// Emptier removes every entry of a home as its user:
// *guestusers.System.
type Emptier interface {
	EmptyHome(ctx context.Context, uid, gid uint32, home string) error
}

// RemoveTools is what a removal runs beyond the Manager's directory and
// root: the kill loop, the child that empties the home, and the check
// that ties the root to the data mount.
type RemoveTools struct {
	Kill  Killer
	Empty Emptier
	Check RootCheck
}

// Remove removes the home of the project user of r, and retires its
// record (S06-08-projects-and-sessions, "Removing a home";
// SEC08-proj-isolation). In order, it:
//
//  1. refuses r unless it names the user wb-guestd knows with r's
//     project, GID and home, or a record with r's IDs and home, or, with no record
//     after a system disk swap, a home owned by r's UID. With no
//     record and no home it answers Removed: there is nothing left;
//  2. locks the user, and runs the kill loop. With processes left it
//     answers ProcessesLeft and changes nothing else;
//  3. starts the child that empties the home as the user, its own
//     group only, then removes the home with unlinkat. A home that
//     stays is logged with RuleHomeLeft;
//  4. retires the record: the shell RetiredShell, pwpolicy
//     -disableuser, and hidden. The record stays by design, not because
//     root can't delete it: the user's IDs are never reused
//     (S06-03-image-updates, "Allocation"), and the next system disk
//     drops it. The user leaves the known set, and Ensure refuses it
//     from then on.
//
// A removal of a retired user runs the same steps, and answers Removed
// again. One removal per user runs at a time: another gets Refused with
// RuleRemovalRunning. A request refused in step 1 locks nothing.
func (m *Manager) Remove(ctx context.Context, r Removal, tools RemoveTools) RemoveResult {
	ctx, cancel := context.WithTimeout(ctx, RemoveDeadline)
	defer cancel()
	if bad := validRemoval(r); bad != nil {
		return m.refuseRemoval(ctx, r, bad)
	}
	// The known user is compared before startRemoval, so a request for
	// another project's user leaves that user's lock as it was.
	m.mu.Lock()
	known, isKnown := m.known[r.UID]
	other := isKnown && (known.ProjectID != r.ProjectID || known.GID != r.GID || known.Home != r.Home)
	started := !other && m.startRemoval(r.UID)
	m.mu.Unlock()
	if other {
		return m.refuseRemoval(ctx, r, &refusal{rule: RuleUnknown, found: "a known user of another project"})
	}
	if !started {
		return m.refuseRemoval(ctx, r, &refusal{rule: RuleRemovalRunning, found: "a removal of the user runs"})
	}
	defer m.endRemoval(r.UID)
	m.log.LogAttrs(ctx, slog.LevelInfo, "project user locked",
		slog.Uint64("uid", uint64(r.UID)), slog.String("reason", "removal"))
	rm := &removal{m: m, ctx: ctx, r: r, name: projectuser.UserName(r.UID), tools: tools}
	defer rm.close()
	return rm.run(isKnown)
}

// validRemoval checks r against the form wb-hostd allocates, as
// validate does for a user.
func validRemoval(r Removal) *refusal {
	field := ""
	switch {
	case !projectuser.ValidProjectID(r.ProjectID):
		field = "project_id"
	case !projectuser.InRange(r.UID):
		field = "uid"
	case r.GID != r.UID:
		field = "gid"
	case r.Home != projectuser.HomePath(projectuser.UserName(r.UID)):
		field = "home_path"
	default:
		return nil
	}
	return &refusal{rule: RuleRequestInvalid, attr: field, found: "not in the form wb-hostd allocates"}
}

// refuseRemoval logs a refused removal with its rule.
func (m *Manager) refuseRemoval(ctx context.Context, r Removal, ref *refusal) RemoveResult {
	m.log.LogAttrs(ctx, slog.LevelWarn, "project user removal refused",
		slog.Uint64("uid", uint64(r.UID)), slog.String("project_id", cut(r.ProjectID)),
		slog.String("rule", ref.rule), slog.String("decision", "refuse"),
		slog.String("attribute", ref.attr), slog.String("found", ref.found))
	return RemoveResult{Outcome: Refused, Rule: ref.rule}
}

// removal is one running removal.
type removal struct {
	m     *Manager
	ctx   context.Context
	r     Removal
	name  string
	tools RemoveTools
	root  gu.Root
}

func (rm *removal) close() {
	if rm.root != nil {
		_ = rm.root.Close()
	}
}

// run runs the steps of Remove after the request's checks.
func (rm *removal) run(isKnown bool) RemoveResult {
	hasRecord, ref := rm.record()
	if ref != nil {
		return rm.m.refuseRemoval(rm.ctx, rm.r, ref)
	}
	home, hasHome, ref := rm.home()
	if ref != nil {
		return rm.m.refuseRemoval(rm.ctx, rm.r, ref)
	}
	if !isKnown && !hasRecord && !hasHome {
		return rm.done("absent")
	}
	if !isKnown && !hasRecord && home.UID != rm.r.UID {
		return rm.m.refuseRemoval(rm.ctx, rm.r, &refusal{rule: RuleUnknown, attr: "home", found: describe(home)})
	}
	if res := rm.tools.Kill.Kill(rm.ctx, rm.r.UID); !res.Clean() {
		rm.m.log.LogAttrs(rm.ctx, slog.LevelWarn, "project user removal stopped",
			slog.Uint64("uid", uint64(rm.r.UID)), slog.String("rule", procs.RuleKillIncomplete),
			slog.String("decision", "processes-left"), slog.Uint64("left", uint64(res.Left)))
		return RemoveResult{Outcome: ProcessesLeft, Left: res.Left}
	}
	if hasHome {
		if ref := rm.removeHome(home); ref != nil {
			return rm.m.refuseRemoval(rm.ctx, rm.r, ref)
		}
	}
	if hasRecord {
		if ref := rm.retire(); ref != nil {
			return rm.m.refuseRemoval(rm.ctx, rm.r, ref)
		}
	}
	return rm.done("removed")
}

// record reads the user's record, and refuses one whose IDs or home
// aren't the request's: retiring it would disable another account.
func (rm *removal) record() (bool, *refusal) {
	rec, found, err := rm.m.dir.Read(rm.ctx, gu.Users, rm.name)
	if err != nil {
		return false, &refusal{rule: RuleDirectory, attr: "user", found: err.Error()}
	}
	if !found {
		return false, nil
	}
	id := strconv.FormatUint(uint64(rm.r.UID), 10)
	for _, a := range []attr{
		{gu.AttrRecordName, rm.name},
		{gu.AttrUniqueID, id},
		{gu.AttrPrimaryGroupID, strconv.FormatUint(uint64(rm.r.GID), 10)},
		{gu.AttrHome, rm.r.Home},
	} {
		if got := rec[a.name]; !slices.Equal(got, []string{a.value}) {
			return true, &refusal{rule: RuleUnknown, attr: "user " + a.name, found: values(got)}
		}
	}
	return true, nil
}

// home opens the volume's root, checks it is on the data mount, and
// reports the home's entry, or that there is none.
func (rm *removal) home() (gu.Entry, bool, *refusal) {
	root, err := rm.m.openRoot()
	if err != nil {
		return gu.Entry{}, false, &refusal{rule: RuleHomeRoot, attr: "root", found: err.Error()}
	}
	rm.root = root
	if rm.tools.Check == nil {
		return gu.Entry{}, false, &refusal{rule: RuleHomeRoot, attr: "root", found: "no check of the root"}
	}
	if rule, found := rm.tools.Check(rm.ctx, root); rule != "" {
		return gu.Entry{}, false, &refusal{rule: rule, attr: "root", found: found}
	}
	e, err := root.Stat(rm.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return gu.Entry{}, false, nil
	case err != nil:
		return gu.Entry{}, false, &refusal{rule: RuleHomeRoot, attr: "home", found: err.Error()}
	}
	return e, true, nil
}

// removeHome empties the home as the user, then removes it as root
// with unlinkat on the root's descriptor. It refuses a home that isn't
// a real directory of the user's, and starts no child for it: the
// child would act on another UID's files, or through a link.
func (rm *removal) removeHome(e gu.Entry) *refusal {
	switch {
	case e.UID != rm.r.UID:
		return &refusal{rule: RuleHomeOtherOwner, attr: "home", found: describe(e)}
	case e.IsLink || !e.IsDir:
		return &refusal{rule: RuleHomeInvalid, attr: "home", found: describe(e)}
	}
	child, cancel := context.WithTimeout(rm.ctx, ChildDeadline)
	defer cancel()
	release := rm.tools.Kill.Exclude(rm.r.UID)
	err := rm.tools.Empty.EmptyHome(child, rm.r.UID, rm.r.GID, rm.r.Home)
	release()
	// A child that ran and exited nonzero left entries it couldn't
	// remove. unlinkat then fails on them, and that is logged below. A
	// child a signal ended, ExitCode -1, didn't finish its run: someone
	// killed it, and the removal is refused.
	exit, exited := errors.AsType[ExitError](err)
	if err != nil && (!exited || exit.ExitCode() < 0 || child.Err() != nil) {
		return &refusal{rule: RuleHomeChild, attr: "home", found: err.Error()}
	}
	if err := rm.root.Rmdir(rm.name); err != nil {
		rm.m.log.LogAttrs(rm.ctx, slog.LevelWarn, "project home left",
			slog.Uint64("uid", uint64(rm.r.UID)), slog.String("rule", RuleHomeLeft),
			slog.String("decision", "keep-home"), slog.String("found", err.Error()))
	}
	return nil
}

// ExitError is the error of a child that ran and exited nonzero, or
// that a signal ended, with ExitCode -1: *exec.ExitError.
type ExitError interface {
	error
	ExitCode() int
}

// retire gives the user's record RetiredShell, then disables it and
// hides it. The shell goes first: Ensure refuses a record whose shell
// differs, so a record a failed step left half retired never becomes a
// known user again, and the next removal finishes it.
func (rm *removal) retire() *refusal {
	if err := rm.m.dir.Create(rm.ctx, gu.Users, rm.name, gu.AttrShell, RetiredShell); err != nil {
		return &refusal{rule: RuleDirectory, attr: "user " + gu.AttrShell, found: err.Error()}
	}
	if err := rm.m.dir.Disable(rm.ctx, rm.name); err != nil {
		return &refusal{rule: RuleDirectory, attr: "user disable", found: err.Error()}
	}
	if err := rm.m.dir.Create(rm.ctx, gu.Users, rm.name, gu.AttrIsHidden, Hidden); err != nil {
		return &refusal{rule: RuleDirectory, attr: "user " + gu.AttrIsHidden, found: err.Error()}
	}
	return nil
}

// done drops the user from the known set, and answers Removed.
func (rm *removal) done(outcome string) RemoveResult {
	rm.m.mu.Lock()
	rm.m.forget(rm.r.UID)
	rm.m.mu.Unlock()
	rm.m.log.LogAttrs(rm.ctx, slog.LevelInfo, "project user removed",
		slog.Uint64("uid", uint64(rm.r.UID)), slog.String("project_id", cut(rm.r.ProjectID)),
		slog.String("decision", "remove"), slog.String("outcome", outcome))
	return RemoveResult{Outcome: Removed}
}
