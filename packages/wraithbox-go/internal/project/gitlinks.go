package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// hooksEntry is the git directory's hooks directory. wb runs git with
// core.hooksPath set to /dev/null (S05-04-naming, "Resolving the key"),
// so git reads nothing in it, and a hook linked to a script in the
// working tree is common.
const hooksEntry = "hooks"

// The bounds of the walk of a git directory (S05-04-naming, "Which
// directory"; SEC13-bounded-resources). A downloaded `.git` with very
// many entries would otherwise hold up every command that resolves a
// project. A packed repository has a few hundred to a few thousand entries, and git packs
// loose objects once there are about 6700 of them (gc.auto), so a normal
// large repository with refs, logs and Git LFS objects stays far under
// the cap.
const (
	maxGitDirEntries  = 500_000
	gitDirWalkTimeout = gitTimeout
)

// errWalkCap stops the walk at the entry cap.
var errWalkCap = errors.New("too many entries")

// checkGitDirLinks refuses a git directory that holds a symbolic link
// which resolves outside it (S05-04-naming, "Which directory").
// git follows such a link, so a downloaded archive could ship
// `objects`, `refs` or `config` as links into another project's `.git`
// and let git read that project's objects and remotes
// (SEC08-proj-isolation).
//
// It walks the whole directory but the top-level hooks directory,
// because git reads far more than the entries that select the
// repository: `config`, `config.worktree`, `commondir`, `HEAD`,
// `objects`, `refs` and `worktrees`, but also `packed-refs`,
// `info/grafts`, `shallow`, `logs` and the git directories of
// submodules in `modules`. A link at any depth is checked, such as
// `refs/heads/main` or `objects/pack`. A link that stays inside the
// directory is accepted.
//
// The walk doesn't follow links. Each link is resolved with
// filepath.EvalSymlinks, which reads link targets and file types, never
// contents, from a directory the walk has already reached inside root.
// The resolved path has no link left in it, so it is inside root only
// when root is a prefix of it. A path that reaches root through another
// letter case or another mount of the volume is refused, which is the
// strict side.
//
// root must be in canonical form. A directory that can't be read is
// refused, because what is in it can't be checked. A walk past
// maxGitDirEntries entries or gitDirWalkTimeout, or one whose request is
// canceled, is refused with RuleGitDirWalkIncomplete.
func (r *Resolver) checkGitDirLinks(ctx context.Context, root string) *Refusal {
	ctx, cancel := context.WithTimeout(ctx, gitDirWalkTimeout)
	defer cancel()
	w := &linkWalk{ctx: ctx, root: root, hooks: filepath.Join(root, hooksEntry), limit: maxGitDirEntries}
	if r.gitDirEntries > 0 {
		w.limit = r.gitDirEntries
	}
	err := filepath.WalkDir(root, w.visit)
	if w.ref != nil {
		return w.ref
	}
	return walkRefusal(ctx, root, w.limit, err)
}

// linkWalk is one walk of a git directory for links that leave it.
type linkWalk struct {
	ctx     context.Context
	root    string
	hooks   string // the top-level hooks directory, which is skipped
	limit   int    // the entry cap
	entries int    // entries visited so far
	ref     *Refusal
}

// visit checks one entry of the walk, and stops the walk at the first
// refusal, the entry cap, or the end of ctx.
func (w *linkWalk) visit(path string, e fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.entries++; w.entries > w.limit {
		return errWalkCap
	}
	// The top-level hooks directory, or a link in its place, is skipped.
	if path == w.hooks && e.IsDir() {
		return filepath.SkipDir
	}
	if path == w.hooks || e.Type()&fs.ModeSymlink == 0 {
		return nil
	}
	w.ref = checkGitDirLink(w.root, path)
	if w.ref != nil {
		return filepath.SkipAll
	}
	return nil
}

// walkRefusal says why the walk of root stopped with err, or returns nil
// when it finished.
func walkRefusal(ctx context.Context, root string, limit int, err error) *Refusal {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errWalkCap):
		return &Refusal{Rule: RuleGitDirWalkIncomplete, err: err, msg: fmt.Sprintf("the git directory %q has more than %d entries, so Wraith Box can't check all of them for links that leave it", root, limit)}
	case errors.Is(err, context.DeadlineExceeded):
		return &Refusal{Rule: RuleGitDirWalkIncomplete, err: err, msg: fmt.Sprintf("the git directory %q could not be checked for links that leave it within %s", root, gitDirWalkTimeout)}
	case ctx.Err() != nil:
		return &Refusal{Rule: RuleGitDirWalkIncomplete, err: err, msg: fmt.Sprintf("the check of the git directory %q for links that leave it was canceled", root)}
	}
	return &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the git directory %q can't be checked for links that leave it: %v", root, err)}
}

// checkOwnGitDirLinks checks the `.git` directory in d, in canonical
// form, so the walk and the targets of its links share one spelling.
func (r *Resolver) checkOwnGitDirLinks(ctx context.Context, d string) *Refusal {
	root, ref := canonical(filepath.Join(d, gitEntry))
	if ref != nil {
		return ref
	}
	return r.checkGitDirLinks(ctx, root)
}

// checkGitDirLink checks one symbolic link, at path in root.
func checkGitDirLink(root, path string) *Refusal {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return &Refusal{
			Rule: RuleGitDirLinkUnresolved, err: err,
			msg: fmt.Sprintf("%q is a symbolic link in the git directory %q that can't be resolved (%v), so Wraith Box can't check that it stays in that directory", path, root, err),
		}
	}
	if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
		return nil
	}
	return &Refusal{
		Rule: RuleGitDirLinkOutside,
		msg:  fmt.Sprintf("%q is a symbolic link to %q, outside the git directory %q. git would follow it, and a repository from another place, such as a downloaded archive, can't read another project's repository through a link", path, target, root),
	}
}
