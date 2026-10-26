package guestusers

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// errName is the error for a name that isn't one path component.
var errName = errors.New("guestusers: not one path component")

// maxNameLen is the longest name Stat and Mkdir take. It bounds the
// names the caller passes, all of the form wbp<uid>. Names that readdir
// returns aren't held to it: APFS counts 255 characters, which can be
// more bytes in UTF-8.
const maxNameLen = 255

// validEntryName reports whether name is one path component other than
// "." and "..".
func validEntryName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i := range len(name) {
		if name[i] == '/' || name[i] == 0 {
			return false
		}
	}
	return true
}

// dirRoot is a Root on an open directory descriptor.
type dirRoot struct {
	f    *os.File
	path string
}

// OpenRoot opens the directory at path with O_NOFOLLOW, so a link at the
// mount root is refused rather than followed. Every later call is
// relative to that descriptor.
func OpenRoot(path string) (Root, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return &dirRoot{f: os.NewFile(uintptr(fd), path), path: path}, nil
}

func (r *dirRoot) fd() int { return int(r.f.Fd()) }

// Stat runs fstatat with AT_SYMLINK_NOFOLLOW on the root's descriptor.
func (r *dirRoot) Stat(name string) (Entry, error) {
	if len(name) > maxNameLen {
		return Entry{}, &fs.PathError{Op: "fstatat", Path: name, Err: errName}
	}
	return r.stat(name)
}

// stat is Stat without the length bound, for the names of Entries.
func (r *dirRoot) stat(name string) (Entry, error) {
	if !validEntryName(name) {
		return Entry{}, &fs.PathError{Op: "fstatat", Path: name, Err: errName}
	}
	var st unix.Stat_t
	if err := unix.Fstatat(r.fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Entry{}, &fs.PathError{Op: "fstatat", Path: r.path + "/" + name, Err: err}
	}
	return entryOf(name, &st), nil
}

func entryOf(name string, st *unix.Stat_t) Entry {
	kind := uint32(st.Mode) & unix.S_IFMT
	return Entry{
		Name: name, IsDir: kind == unix.S_IFDIR, IsLink: kind == unix.S_IFLNK,
		UID: st.Uid, GID: st.Gid,
		Perm:    fs.FileMode(st.Mode) & fs.ModePerm,
		Special: uint32(st.Mode)&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0,
	}
}

// Mkdir runs mkdirat with mode 700, which fails on any existing entry, a
// link included. It then opens the new directory with O_NOFOLLOW and
// gives it to uid and gid with fchown and fchmod, so a link put in its
// place meanwhile is never followed (S06-05-disks, "Owners and modes").
// When a step after mkdirat fails, it removes the directory again with
// unlinkat on the root's descriptor, so no home owned by root stays to
// refuse the user at the next request. An error of the removal joins
// the step's.
func (r *dirRoot) Mkdir(name string, uid, gid uint32) error {
	if !validEntryName(name) || len(name) > maxNameLen {
		return &fs.PathError{Op: "mkdirat", Path: name, Err: errName}
	}
	path := r.path + "/" + name
	if err := unix.Mkdirat(r.fd(), name, uint32(HomeMode)); err != nil {
		return &fs.PathError{Op: "mkdirat", Path: path, Err: err}
	}
	if err := r.own(name, uid, gid); err != nil {
		if rmErr := unix.Unlinkat(r.fd(), name, unix.AT_REMOVEDIR); rmErr != nil {
			return errors.Join(err, &fs.PathError{Op: "unlinkat", Path: path, Err: rmErr})
		}
		return err
	}
	return nil
}

// own opens the directory Mkdir made and gives it to uid and gid, mode
// 700.
func (r *dirRoot) own(name string, uid, gid uint32) error {
	path := r.path + "/" + name
	fd, err := unix.Openat(r.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return &fs.PathError{Op: "openat", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Fchown(fd, int(uid), int(gid)); err != nil {
		return &fs.PathError{Op: "fchown", Path: path, Err: err}
	}
	// The umask may have cleared bits of the mode mkdirat asked for.
	if err := unix.Fchmod(fd, uint32(HomeMode)); err != nil {
		return &fs.PathError{Op: "fchmod", Path: path, Err: err}
	}
	return nil
}

// Entries reads the root's names from a second descriptor of it, then
// runs fstatat on each. An entry removed between the two is skipped.
func (r *dirRoot) Entries(limit int) ([]Entry, error) {
	fd, err := unix.Openat(r.fd(), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: r.path, Err: err}
	}
	dir := os.NewFile(uintptr(fd), r.path)
	defer func() { _ = dir.Close() }()
	var names []string
	for {
		batch, err := dir.Readdirnames(256)
		names = append(names, batch...)
		if len(names) > limit {
			return nil, fmt.Errorf("%s: over %d entries: %w", r.path, limit, ErrTooManyEntries)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &fs.PathError{Op: "readdir", Path: r.path, Err: err}
		}
	}
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		e, err := r.stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Mount runs fstatfs on the root's descriptor.
func (r *dirRoot) Mount() (guestmount.Mount, error) { return guestmount.Fstatfs(r.f.Fd()) }

func (r *dirRoot) Close() error { return r.f.Close() }
