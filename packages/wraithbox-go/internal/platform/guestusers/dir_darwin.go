package guestusers

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// Rmdir runs unlinkat with AT_REMOVEDIR on the root's descriptor. It
// removes neither a link nor a file: unlinkat fails with ENOTDIR.
func (r *dirRoot) Rmdir(name string) error {
	if !validEntryName(name) || len(name) > maxNameLen {
		return &fs.PathError{Op: "unlinkat", Path: name, Err: errName}
	}
	if err := unix.Unlinkat(r.fd(), name, unix.AT_REMOVEDIR); err != nil {
		return &fs.PathError{Op: "unlinkat", Path: r.path + "/" + name, Err: err}
	}
	return nil
}

// OpenDir runs openat with O_DIRECTORY and O_NOFOLLOW on the root's
// descriptor.
func (r *dirRoot) OpenDir(name string) (Dir, error) {
	if !validEntryName(name) || len(name) > maxNameLen {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: errName}
	}
	path := r.path + "/" + name
	fd, err := unix.Openat(r.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: path, Err: err}
	}
	return &dirHandle{f: os.NewFile(uintptr(fd), path), path: path}, nil
}

// dirHandle is a Dir on an open directory descriptor.
type dirHandle struct {
	f    *os.File
	path string
}

func (d *dirHandle) fd() int { return int(d.f.Fd()) }

// Self runs fstat on the directory's descriptor.
func (d *dirHandle) Self() (Entry, error) {
	var st unix.Stat_t
	if err := unix.Fstat(d.fd(), &st); err != nil {
		return Entry{}, &fs.PathError{Op: "fstat", Path: d.path, Err: err}
	}
	return entryOf(".", &st), nil
}

// Names reads the names from a second descriptor of the directory.
func (d *dirHandle) Names(limit int) ([]string, error) {
	fd, err := unix.Openat(d.fd(), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: d.path, Err: err}
	}
	dir := os.NewFile(uintptr(fd), d.path)
	defer func() { _ = dir.Close() }()
	var names []string
	for {
		batch, err := dir.Readdirnames(256)
		names = append(names, batch...)
		if len(names) > limit {
			return nil, fmt.Errorf("%s: over %d entries: %w", d.path, limit, ErrTooManyEntries)
		}
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, &fs.PathError{Op: "readdir", Path: d.path, Err: err}
		}
	}
}

// ReadFile opens name with O_NOFOLLOW, so a link fails with ELOOP, and
// O_NONBLOCK, so a FIFO doesn't block the open. fstat on the open
// descriptor decides, so an entry swapped after the open is never read.
// It reads at most limit+1 bytes, in case the file grew after fstat.
func (d *dirHandle) ReadFile(name string, limit int) ([]byte, Entry, error) {
	path := d.path + "/" + name
	if !validEntryName(name) || len(name) > maxNameLen {
		return nil, Entry{}, &fs.PathError{Op: "openat", Path: name, Err: errName}
	}
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Entry{}, &fs.PathError{Op: "openat", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, Entry{}, &fs.PathError{Op: "fstat", Path: path, Err: err}
	}
	e := entryOf(name, &st)
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		return nil, e, &fs.PathError{Op: "read", Path: path, Err: ErrNotRegular}
	}
	if st.Size > int64(limit) {
		return nil, e, &fs.PathError{Op: "read", Path: path, Err: ErrTooBig}
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, e, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	if len(data) > limit {
		return nil, e, &fs.PathError{Op: "read", Path: path, Err: ErrTooBig}
	}
	return data, e, nil
}

// WriteFile writes name.tmp, then renames it over name with renameat,
// so a reader sees the old file or the new one, never part of one. A
// name.tmp left by a crash is removed first: only the directory's
// owner writes there.
func (d *dirHandle) WriteFile(name string, data []byte) error {
	if !validEntryName(name) || len(name)+len(".tmp") > maxNameLen {
		return &fs.PathError{Op: "openat", Path: name, Err: errName}
	}
	tmp := name + ".tmp"
	path := d.path + "/" + tmp
	if err := unix.Unlinkat(d.fd(), tmp, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "unlinkat", Path: path, Err: err}
	}
	fd, err := unix.Openat(d.fd(), tmp,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return &fs.PathError{Op: "openat", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = unix.Renameat(d.fd(), tmp, d.fd(), name)
	}
	if err != nil {
		_ = unix.Unlinkat(d.fd(), tmp, 0)
		return &fs.PathError{Op: "write", Path: d.path + "/" + name, Err: err}
	}
	return nil
}

func (d *dirHandle) Close() error { return d.f.Close() }
