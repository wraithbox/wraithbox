package setup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
)

// linkAction is what wb setup does at ~/.local/bin/wb.
type linkAction int

const (
	// linkKeep: the link already points to the bundle's wb.
	linkKeep linkAction = iota
	// linkCreate: there is no file.
	linkCreate
	// linkReplace: the file is a link into a Wraith Box bundle.
	linkReplace
)

// link is the plan for ~/.local/bin/wb.
type link struct {
	path   string
	target string
	action linkAction
}

// linkFix is the fix of a RuleLinkExists refusal.
const linkFix = "Move the file at ~/.local/bin/wb out of the way, then run wb setup again. wb setup replaces only a link into a Wraith Box bundle."

// planLink decides what to do at ~/.local/bin/wb for the bundle's wb
// (S17-02-setup, "The link"). It refuses with RuleLinkExists any file
// there that isn't a link into a Wraith Box bundle. It changes nothing.
func planLink(ctx context.Context, env *Env, bundle string) (link, error) {
	l := link{
		path:   filepath.Join(env.Home, ".local", "bin", "wb"),
		target: filepath.Join(bundle, "Contents", "MacOS", "wb"),
	}
	action, err := checkLink(ctx, env, l)
	if err != nil {
		return link{}, err
	}
	decide(ctx, env.Log, RuleLinkExists, "accept", fmt.Sprintf("%q: %s", l.path, [...]string{"keep", "create", "replace"}[action]))
	l.action = action
	return l, nil
}

// checkLink returns what to do at l.path, or refuses with
// RuleLinkExists a file there that wb setup may not replace.
func checkLink(ctx context.Context, env *Env, l link) (linkAction, error) {
	action, why, err := linkState(l.path, l.target)
	if err != nil {
		return 0, err
	}
	if why != "" {
		return 0, refuse(ctx, env.Log, &Refusal{
			Rule: RuleLinkExists,
			Msg:  fmt.Sprintf("%q %s, so it isn't a link into a Wraith Box bundle", l.path, why),
			Fix:  linkFix,
		})
	}
	return action, nil
}

// linkState tells what is at path. A file there is a link into a Wraith
// Box bundle when all of these hold (S17-02-setup, "The link"):
//
//   - lstat shows a symbolic link;
//   - its target ends in /Contents/MacOS/wb;
//   - the Contents/Info.plist of that target's bundle has a
//     WBBuildVersion string.
//
// For any other file, why says what it is, and wb setup refuses it.
func linkState(path, target string) (action linkAction, why string, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return linkCreate, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("lstat %s: %w", path, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return 0, fmt.Sprintf("is a file of type %q, not a symbolic link", fi.Mode().Type().String()), nil
	}
	to, err := os.Readlink(path)
	if err != nil {
		return 0, "", fmt.Errorf("readlink %s: %w", path, err)
	}
	if to == target {
		return linkKeep, "", nil
	}
	if !filepath.IsAbs(to) {
		to = filepath.Join(filepath.Dir(path), to)
	}
	other, ok := bundleOf(filepath.Clean(to))
	if !ok {
		return 0, fmt.Sprintf("links to %q, which is not Contents/MacOS/wb of a bundle", to), nil
	}
	if _, err := appbundle.ReadBuildVersion(filepath.Join(other, filepath.FromSlash(appbundle.InfoPlistPath))); err != nil {
		return 0, fmt.Sprintf("links into %q, whose Info.plist has no %s (%v)", other, appbundle.BuildVersionKey, err), nil
	}
	return linkReplace, "", nil
}

// applyLink makes ~/.local/bin, with mode 0700 when it is missing, and
// links ~/.local/bin/wb to the bundle's wb: a new link at a random name
// next to it, renamed over the old one. Right before the rename it
// checks the file there again, and refuses one that changed since
// planLink into something it may not replace.
func applyLink(ctx context.Context, env *Env, l link, out io.Writer) error {
	if l.action == linkKeep {
		fmt.Fprintf(out, "wb setup: %s already links to %s\n", l.path, l.target)
		return nil
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}
	tmp := filepath.Join(dir, ".wb.setup-"+rand.Text())
	if err := os.Symlink(l.target, tmp); err != nil {
		return fmt.Errorf("make the link %s: %w", tmp, err)
	}
	if _, err := checkLink(ctx, env, l); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s over %s: %w", tmp, l.path, err)
	}
	decide(ctx, env.Log, RuleLinkExists, "link", fmt.Sprintf("%q -> %q", l.path, l.target))
	fmt.Fprintf(out, "wb setup: linked %s to %s\n", l.path, l.target)
	return nil
}

// onPATH reports whether dir is one of the directories in pathList.
func onPATH(pathList, dir string) bool {
	for _, d := range filepath.SplitList(pathList) {
		if d != "" && filepath.Clean(d) == dir {
			return true
		}
	}
	return false
}
