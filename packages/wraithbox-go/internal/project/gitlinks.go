package project

import (
	"context"
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
// contents, from a directory the walk has already reached inside root. The resolved
// path has no link left in it, so it is inside root only when root is a
// prefix of it. A path that reaches root through another letter case or
// another mount of the volume is refused, which is the strict side.
//
// root must be in canonical form. A directory that can't be read is
// refused, because what is in it can't be checked.
func checkGitDirLinks(ctx context.Context, root string) *Refusal {
	var ref *Refusal
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == filepath.Join(root, hooksEntry) {
			// A hooks directory, or a link in its place, is skipped.
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		ref = checkGitDirLink(root, path)
		if ref != nil {
			return filepath.SkipAll
		}
		return nil
	})
	if ref != nil {
		return ref
	}
	if err != nil {
		return &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the git directory %q can't be checked for links that leave it: %v", root, err)}
	}
	return nil
}

// checkOwnGitDirLinks checks the `.git` directory in d, in canonical
// form, so the walk and the targets of its links share one spelling.
func checkOwnGitDirLinks(ctx context.Context, d string) *Refusal {
	root, ref := canonical(filepath.Join(d, gitEntry))
	if ref != nil {
		return ref
	}
	return checkGitDirLinks(ctx, root)
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
