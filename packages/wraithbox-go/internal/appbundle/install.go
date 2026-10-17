package appbundle

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Install installs the bundle at src as dest with the four steps of
// S17-05-upgrades-and-migrations, "Upgrades":
//
//  1. copy the new bundle next to the old one;
//  2. rename the old one aside;
//  3. rename the new one in;
//  4. delete the old one.
//
// It never copies over the files of an installed bundle, so nothing
// starts a program from a bundle that is half old and half new. The copy
// and the old bundle are in a directory next to dest, on the same
// volume, so both renames stay on one volume. log gets one line per step.
func Install(src, dest string, log io.Writer) (err error) {
	src, dest = filepath.Clean(src), filepath.Clean(dest)
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("the bundle to install: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the bundle to install, %s, is not a directory", src)
	}
	if src == dest || strings.HasPrefix(dest, src+string(filepath.Separator)) {
		return fmt.Errorf("can't install %s into itself as %s", src, dest)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // G301: ~/Applications is world-readable, as macOS makes it
		return err
	}
	stage, err := os.MkdirTemp(parent, ".wraithbox-install-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()

	fmt.Fprintf(log, "install: 1. copy %s next to %s\n", src, dest)
	if err := copyTree(src, stage, "new"); err != nil {
		return fmt.Errorf("copying the new bundle: %w", err)
	}
	return swapIn(filepath.Join(stage, "new"), dest, filepath.Join(stage, "old"), log)
}

// swapIn does steps 2 to 4: it renames dest aside to old when there is
// one, renames next in as dest, and deletes old. When the rename in
// fails, it renames old back.
func swapIn(next, dest, old string, log io.Writer) error {
	_, err := os.Lstat(dest)
	installed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if installed {
		fmt.Fprintf(log, "install: 2. rename the installed %s aside\n", dest)
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("renaming the installed bundle aside: %w", err)
		}
	}
	fmt.Fprintf(log, "install: 3. rename the new bundle in as %s\n", dest)
	if err := os.Rename(next, dest); err != nil {
		if installed {
			err = errors.Join(err, os.Rename(old, dest))
		}
		return fmt.Errorf("renaming the new bundle in: %w", err)
	}
	if installed {
		fmt.Fprintln(log, "install: 4. delete the old bundle")
		return os.RemoveAll(old)
	}
	return nil
}

// copyTree copies the directory src to the new directory name in the
// directory dir: the directories, the regular files with their permission
// bits, and the symbolic links as links. Any other file type is an
// error. It reads and writes through os.Root, so a link can't lead
// either side out of its directory.
func copyTree(src, dir, name string) error {
	from, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	return fs.WalkDir(from.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		dst := path.Join(name, rel)
		switch mode := info.Mode(); {
		case mode.IsDir():
			return to.Mkdir(dst, mode.Perm())
		case mode&fs.ModeSymlink != 0:
			target, err := from.Readlink(rel)
			if err != nil {
				return err
			}
			return to.Symlink(target, dst)
		case mode.IsRegular():
			return copyFile(from, to, rel, dst, mode.Perm())
		default:
			return fmt.Errorf("%s is neither a file, a directory nor a link (%s)", rel, mode)
		}
	})
}

func copyFile(from, to *os.Root, src, dst string, perm fs.FileMode) (err error) {
	in, err := from.Open(src)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := to.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}
