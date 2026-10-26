package platform

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// CheckOwnDir checks that path is a directory, not a symbolic link, and
// is owned by the effective user, and that neither its group nor other
// users can write it. It doesn't follow a link at path. A
// missing path gives an error that wraps fs.ErrNotExist. wb accepts a
// project directory under <data> only when it passes, so a link or a
// directory that another user made can't take the place of one
// (SEC12-least-privilege, S04-06-git-confinement).
func CheckOwnDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%q is a symbolic link, not a directory", path)
	case !info.IsDir():
		return fmt.Errorf("%q is not a directory", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("the owner of %q can't be read", path)
	}
	if owner, uid := strconv.FormatUint(uint64(st.Uid), 10), strconv.Itoa(os.Geteuid()); owner != uid {
		return fmt.Errorf("%q is owned by user ID %s, not by this user (%s)", path, owner, uid)
	}
	// A directory others can write lets them plant entries in it.
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("%q has mode %04o, which lets other users write it", path, perm)
	}
	return nil
}
