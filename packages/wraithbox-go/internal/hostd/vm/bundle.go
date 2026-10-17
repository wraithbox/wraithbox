package vm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The guest tools disk's names in a VM bundle. The host writes the copy,
// and wb-vmd attaches it read-only, so it keeps .img (S06-05-disks,
// "Names on the host"). The temporary name is the copy while it is
// cloned.
const (
	ToolsDiskName = "tools.img"
	toolsDiskTemp = "tools.img.partial"
)

// ensureBundle creates the slot's bundle directory when it is missing.
// It refuses a path that is a symbolic link or not a directory, checked
// with lstat and never followed, so a slot never uses another's bundle
// (S04-04-vm-processes, "The slot", SEC08-proj-isolation).
func ensureBundle(slot Slot, path string) (string, *Refusal) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", bundleCreateFailed(slot, path, err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Mkdir fails on anything at the path, a link included, so a
		// link made since the lstat is refused too.
		if err := os.Mkdir(path, 0o700); err != nil {
			return "", bundleCreateFailed(slot, path, err)
		}
		return path, nil
	}
	if err != nil {
		return "", bundleCreateFailed(slot, path, err)
	}
	if !info.IsDir() {
		return "", &Refusal{
			Rule: RuleBundleNotDirectory, Slot: slot, Phase: PhaseStopped,
			Message: fmt.Sprintf("the %s VM can't start: its bundle %s is a %s, not a directory. Move it away", slot, path, kindOf(info.Mode())),
		}
	}
	return path, nil
}

func bundleCreateFailed(slot Slot, path string, err error) *Refusal {
	return &Refusal{
		Rule: RuleBundleCreateFailed, Slot: slot, Phase: PhaseStopped, err: err,
		Message: fmt.Sprintf("the %s VM can't start: its bundle %s couldn't be created: %v", slot, path, err),
	}
}

func kindOf(mode fs.FileMode) string {
	if mode&fs.ModeSymlink != 0 {
		return "symbolic link"
	}
	if mode.IsRegular() {
		return "file"
	}
	return "special file"
}

// replaceToolsDisk replaces the bundle's tools.img with a clone of the
// installed guest tools disk, before every cold start
// (S06-12-guest-agent, "Guest tools disk"). The bundle's contents are
// written by a possibly compromised wb-vmd (S04-04-vm-processes,
// "Reaching an instance"), so:
//
//   - it clones to a temporary name, created exclusively, then renames
//     that over tools.img;
//   - it follows no symbolic link at either name: a link at tools.img is
//     replaced, and its target is left alone;
//   - it never opens or reads the old copy (SEC15-no-guest-disk-mount);
//   - it deletes a temporary file an earlier crash left, without
//     following it;
//   - after a failure it leaves no temporary file.
func replaceToolsDisk(c Cloner, src, bundle string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("the installed guest tools disk: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the installed guest tools disk %s is a %s, not a file", src, kindOf(info.Mode()))
	}
	tmp := filepath.Join(bundle, toolsDiskTemp)
	if err := removeName(tmp); err != nil {
		return fmt.Errorf("the leftover %s: %w", tmp, err)
	}
	if err := c.Clone(src, tmp); err != nil {
		return errors.Join(fmt.Errorf("clone %s to %s: %w", src, tmp, err), removeName(tmp))
	}
	// rename(2) replaces the name tools.img, whatever is there, without
	// opening it.
	if err := os.Rename(tmp, filepath.Join(bundle, ToolsDiskName)); err != nil {
		return errors.Join(fmt.Errorf("rename %s to %s: %w", tmp, ToolsDiskName, err), removeName(tmp))
	}
	return nil
}

// removeName deletes the name path. It removes a symbolic link, never
// what the link points at, and a missing name is no error.
func removeName(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
