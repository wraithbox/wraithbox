package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// gitEntry is the name git looks for in each directory.
const gitEntry = ".git"

// Resolve finds the repository that dir belongs to, and its project key
// (S05-04-naming, "Resolving the key"). dir is `-C <dir>` or the current
// directory; a relative one is made absolute.
//
// It walks up from dir, in canonical form, to the first directory D with
// a `.git` entry, as git does, and the key must belong to D
// (SEC08-proj-isolation):
//
//   - a `.git` directory must be both of git's git directory and common
//     directory, and its core.worktree, when set, must be D;
//   - a `.git` file is accepted only for a linked worktree whose
//     back-link names D's `.git` and whose core.worktree, when set, is
//     D, or a submodule whose core.worktree is D;
//   - a `.git` symbolic link, or any other type, is refused, because a
//     link could name another project's `.git`.
//
// A key under <data>, <config> or <logs> is refused. Every refusal is
// logged with its rule and returned as a *Refusal, before any identity
// is read.
func (r *Resolver) Resolve(ctx context.Context, dir string) (*Repo, error) {
	repo, ref := r.resolve(ctx, dir)
	if ref != nil {
		r.logRefusal(ctx, "project refused", dir, ref)
		return nil, ref
	}
	r.log().LogAttrs(ctx, slog.LevelInfo, "project resolved",
		slog.String("rule", string(RuleResolved)),
		slog.String("decision", "accept"),
		slog.String("dir", repo.Dir),
		slog.String("realm", string(repo.Key.Realm)),
		slog.String("key", repo.Key.Path),
		slog.String("kind", string(repo.Kind)))
	return repo, nil
}

func (r *Resolver) resolve(ctx context.Context, dir string) (*Repo, *Refusal) {
	if r.Own.Data == "" || r.Own.Config == "" || r.Own.Logs == "" {
		return nil, &Refusal{Rule: RuleOwnUnset, msg: "Wraith Box's own directories are not known, so no project is resolved"}
	}
	d, isFile, ref := findGitEntry(dir)
	if ref != nil {
		return nil, ref
	}
	out, ref := r.run(ctx, gitRun{
		dir: d, timeout: gitTimeout, limit: 2 * (maxPathLen + 1),
		args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir", "--git-dir"},
	})
	if ref != nil {
		return nil, ref
	}
	common, gitDir, err := parseRevParse(out)
	if err != nil {
		return nil, &Refusal{Rule: RuleGitOutput, err: err, msg: fmt.Sprintf("git rev-parse in %q printed paths that can't be read: %v", d, err)}
	}
	kind, ref := r.checkGitDir(ctx, d, isFile, common, gitDir)
	if ref != nil {
		return nil, ref
	}
	if ref := r.checkOwn(common); ref != nil {
		return nil, ref
	}
	return &Repo{
		Key:  Key{Realm: RealmNative, Path: common},
		Dir:  d,
		Kind: kind,
		Name: defaultName(kind, d, common),
	}, nil
}

// findGitEntry walks up from dir, in canonical form, to the first
// directory with a `.git` entry. It returns that directory, and whether
// the entry is a file rather than a directory. Lstat doesn't follow the
// entry, so a symbolic link is seen as one, and refused.
func findGitEntry(dir string) (string, bool, *Refusal) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false, &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the directory %q can't be made absolute: %v", dir, err)}
	}
	d, ref := canonical(abs)
	if ref != nil {
		return "", false, ref
	}
	// The directory becomes Repo.Dir and is printed, so it gets the
	// checks of the paths git prints.
	if err := checkPath(d); err != nil {
		return "", false, &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the directory %q %v", d, err)}
	}
	for {
		info, err := os.Lstat(filepath.Join(d, gitEntry))
		switch {
		case err == nil:
			isFile, ref := entryType(d, info.Mode().Type())
			return d, isFile, ref
		case !errors.Is(err, fs.ErrNotExist):
			return "", false, &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the .git entry in %q can't be read: %v", d, err)}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false, &Refusal{Rule: RuleNotInRepo, msg: fmt.Sprintf("%q is not in a git repository: no .git from there up to the root", abs)}
		}
		d = parent
	}
}

// canonical returns the canonical form of a directory
// (platform.CanonicalDir). On a host without one, the refusal says so.
func canonical(dir string) (string, *Refusal) {
	c, err := platform.CanonicalDir(dir)
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		return "", &Refusal{Rule: RuleHostUnsupported, err: err, msg: "projects are not supported on this host yet: " + err.Error()}
	case err != nil:
		return "", &Refusal{Rule: RuleDirUnreadable, err: err, msg: err.Error()}
	}
	return c, nil
}

// entryType accepts a `.git` directory or file, before git runs. git
// would follow a symbolic link, and a link could name another project's
// `.git` (SEC08-proj-isolation, S05-04-naming, "Which directory"), so
// it is refused, as is any other type.
func entryType(d string, mode fs.FileMode) (isFile bool, ref *Refusal) {
	entry := filepath.Join(d, gitEntry)
	switch {
	case mode.IsDir():
		return false, nil
	case mode.IsRegular():
		return true, nil
	case mode == fs.ModeSymlink:
		return false, &Refusal{Rule: RuleGitLink, msg: fmt.Sprintf("%q is a symbolic link, which could name another project's repository. Wraith Box accepts a .git directory, or the .git file of a linked worktree or submodule", entry)}
	}
	return false, &Refusal{Rule: RuleGitEntryType, msg: fmt.Sprintf("%q is neither a directory nor a file", entry)}
}

// checkGitDir checks that the repository git resolved in d is d's own
// (S05-04-naming, "Which directory" and "A `.git` file"). It is where
// SEC08-proj-isolation is enforced for a directory.
func (r *Resolver) checkGitDir(ctx context.Context, d string, isFile bool, common, gitDir string) (Kind, *Refusal) {
	if isFile {
		return r.checkGitFile(ctx, d, common, gitDir)
	}
	if ref := checkMain(d, common, gitDir); ref != nil {
		return "", ref
	}
	return KindMain, r.checkCoreWorktree(ctx, d, gitDir, gitDir)
}

// checkMain: a `.git` directory must be git's git directory and common
// directory. When git found no repository there and walked on to a
// parent's, they differ.
func checkMain(d, common, gitDir string) *Refusal {
	own, ref := canonical(filepath.Join(d, gitEntry))
	if ref != nil {
		return ref
	}
	for _, p := range []string{gitDir, common} {
		c, ref := canonical(p)
		if ref != nil {
			return ref
		}
		if c != own {
			return &Refusal{
				Rule: RuleGitDirElsewhere,
				msg:  fmt.Sprintf("git in %q resolved the repository %q, not %q. Check that %q is a valid repository", d, c, own, own),
			}
		}
	}
	return nil
}

// checkGitFile accepts a `.git` file only for a linked worktree or a
// submodule whose repository names d back.
func (r *Resolver) checkGitFile(ctx context.Context, d, common, gitDir string) (Kind, *Refusal) {
	cg, ref := canonical(gitDir)
	if ref != nil {
		return "", ref
	}
	cc, ref := canonical(common)
	if ref != nil {
		return "", ref
	}
	switch {
	case cg == cc:
		return KindSubmodule, r.checkSubmodule(ctx, d, gitDir)
	case filepath.Dir(cg) == filepath.Join(cc, "worktrees"):
		if ref := checkWorktree(d, cg); ref != nil {
			return "", ref
		}
		return KindWorktree, r.checkCoreWorktree(ctx, d, common, gitDir)
	}
	return "", elsewhere(d, gitDir, "is neither a linked worktree's nor a submodule's")
}

func elsewhere(d, gitDir, why string) *Refusal {
	return &Refusal{
		Rule: RuleGitFileElsewhere,
		msg:  fmt.Sprintf("%q names the git directory %q, which %s. A .git file from another place, such as a downloaded archive, can't make this directory another project", filepath.Join(d, gitEntry), gitDir, why),
	}
}

// checkWorktree: the back-link `<common>/worktrees/<name>/gitdir` must
// name d's `.git`. The file is read only when it is a regular file, and
// at most maxPathLen bytes and a line end of it.
func checkWorktree(d, cg string) *Refusal {
	link := filepath.Join(cg, "gitdir")
	content, err := readSmallFile(link)
	if err != nil {
		return elsewhere(d, cg, fmt.Sprintf("has no back-link that can be read (%v)", err))
	}
	target, err := parseBackLink(content, cg)
	if err != nil {
		return elsewhere(d, cg, fmt.Sprintf("has a back-link that %v", err))
	}
	if filepath.Base(target) != gitEntry {
		return elsewhere(d, cg, "has a back-link to another place")
	}
	if c, ref := canonical(filepath.Dir(target)); ref != nil || c != d {
		return elsewhere(d, cg, "belongs to another worktree")
	}
	return nil
}

// readSmallFile reads a regular file of at most maxPathLen+1 bytes.
func readSmallFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxPathLen+2))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPathLen+1 {
		return nil, errLongPath
	}
	return b, nil
}

// checkCoreWorktree: the core.worktree of a `.git` directory or a linked
// worktree, when it is set, must be d in canonical form. git runs its
// worktree commands in the directory core.worktree names, so a
// downloaded repository whose core.worktree names another project's
// tree would have git read and write that tree under this repository's
// configuration (SEC08-proj-isolation, S05-04-naming, "Which
// directory"). For a `.git` directory, common and gitDir are the same.
func (r *Resolver) checkCoreWorktree(ctx context.Context, d, common, gitDir string) *Refusal {
	values, ref := r.coreWorktrees(ctx, d, common, gitDir)
	if ref != nil {
		return ref
	}
	if why := notAllIn(values, d); why != "" {
		return &Refusal{
			Rule: RuleWorktreeElsewhere,
			msg:  fmt.Sprintf("the repository %q %s. A repository from another place, such as a downloaded archive, can't make git work in another directory", gitDir, why),
		}
	}
	return nil
}

// checkSubmodule: the submodule's core.worktree, relative to its git
// directory, must be set, and be d.
func (r *Resolver) checkSubmodule(ctx context.Context, d, gitDir string) *Refusal {
	values, ref := r.coreWorktrees(ctx, d, gitDir, gitDir)
	if ref != nil {
		return ref
	}
	if len(values) == 0 {
		return elsewhere(d, gitDir, "is a repository's own git directory without core.worktree")
	}
	if why := notAllIn(values, d); why != "" {
		return elsewhere(d, gitDir, why)
	}
	return nil
}

// worktreeValue is core.worktree as one config file sets it: the path,
// made absolute against the git directory, or why it can't be read.
type worktreeValue struct {
	file string
	path string
	err  error
}

// notAllIn says why one of values isn't the directory d in canonical
// form, or returns "" when all are. A path that can't be put in
// canonical form, such as one that doesn't exist, isn't d.
func notAllIn(values []worktreeValue, d string) string {
	for _, v := range values {
		if v.err != nil {
			return fmt.Sprintf("has a core.worktree in %q that %v", v.file, v.err)
		}
		c, ref := canonical(v.path)
		if ref != nil {
			return fmt.Sprintf("has a core.worktree in %q that names %q, which can't be put in canonical form: %v", v.file, v.path, ref)
		}
		if c != d {
			return fmt.Sprintf("has a core.worktree in %q that names %q, not %q", v.file, v.path, d)
		}
	}
	return ""
}

// coreWorktrees reads core.worktree, with includes off, as git reads
// it, from the files git takes it from: the common directory's config,
// and the git directory's config.worktree when
// extensions.worktreeConfig is on. For a linked worktree they are in two
// directories. Both are read whether the extension is on or not, and
// the common config is read for a linked worktree too, so no value git
// takes is missed. A value git ignores can then refuse a directory,
// which is the strict side. A relative value is relative to gitDir.
//
// A file that doesn't exist gives no value, without a git run. One that
// exists but isn't a readable regular file is refused, because git's
// exit code for "unset" is also the one for a file it can't read.
func (r *Resolver) coreWorktrees(ctx context.Context, d, common, gitDir string) ([]worktreeValue, *Refusal) {
	var values []worktreeValue
	for _, file := range []string{filepath.Join(common, "config"), filepath.Join(gitDir, "config.worktree")} {
		present, ref := checkConfigFile(file)
		if ref != nil {
			return nil, ref
		}
		if !present {
			continue
		}
		out, ref := r.run(ctx, gitRun{
			dir: d, timeout: gitTimeout, limit: maxPathLen + 1,
			args: []string{"config", "--file", file, "--no-includes", "--null", "--get", "core.worktree"},
		})
		if ref != nil && ref.exitCode == 1 {
			continue
		}
		if ref != nil {
			return nil, ref
		}
		p, err := parseWorktreeValue(out, gitDir)
		values = append(values, worktreeValue{file: file, path: p, err: err})
	}
	return values, nil
}

// checkConfigFile reports whether a git config file exists, and refuses
// one that exists but isn't a regular file that can be opened.
func checkConfigFile(file string) (bool, *Refusal) {
	info, err := os.Lstat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the git config file %q can't be read: %v", file, err)}
	case !info.Mode().IsRegular():
		return false, &Refusal{Rule: RuleDirUnreadable, msg: fmt.Sprintf("the git config file %q is not a regular file", file)}
	}
	f, err := os.Open(file)
	if err != nil {
		return false, &Refusal{Rule: RuleDirUnreadable, err: err, msg: fmt.Sprintf("the git config file %q can't be read: %v", file, err)}
	}
	_ = f.Close()
	return true, nil
}

// checkOwn refuses a key under <data>, <config> or <logs>, compared by
// path component in canonical form, so another letter case or a link
// to the same directory still matches (S05-04-naming, "Wraith Box's own
// directories"). A root that doesn't exist holds no repository.
func (r *Resolver) checkOwn(common string) *Refusal {
	key, ref := canonical(common)
	if ref != nil {
		return ref
	}
	for _, root := range []string{r.Own.Data, r.Own.Config, r.Own.Logs} {
		c, err := platform.CanonicalDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return &Refusal{
				Rule: RuleDirUnreadable, err: err,
				msg: fmt.Sprintf("Wraith Box's directory %q can't be compared with the repository: %v", root, err),
			}
		}
		if key == c || strings.HasPrefix(key, c+string(filepath.Separator)) {
			return &Refusal{
				Rule: RuleOwnDirectory,
				msg:  fmt.Sprintf("the repository %q is in Wraith Box's own directory %q, which is not a project", common, root),
			}
		}
	}
	return nil
}
