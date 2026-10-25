package guestmount_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// TestStatfsRoot: statfs reports the file system that holds a path, with
// the system volume's mount point for /.
func TestStatfsRoot(t *testing.T) {
	m, err := (&guestmount.System{}).Statfs("/")
	if err != nil {
		t.Fatal(err)
	}
	if m.On != "/" || m.From == "" {
		t.Fatalf("statfs / = %+v", m)
	}
}

// TestLstat: lstat reports a directory's owner and mode, a link as a
// link, and a missing path as fs.ErrNotExist.
func TestLstat(t *testing.T) {
	s := &guestmount.System{}
	dir := t.TempDir()
	sub := filepath.Join(dir, "d")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "l")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lstat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsDir || got.IsLink || got.UID != uint32(os.Getuid()) || got.Perm != 0o750 || got.Special {
		t.Errorf("lstat dir = %+v", got)
	}
	if got, err := s.Lstat(link); err != nil || !got.IsLink || got.IsDir {
		t.Errorf("lstat link = %+v, %v", got, err)
	}
	if _, err := s.Lstat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lstat missing = %v, want fs.ErrNotExist", err)
	}
}

// TestClaimNoFollow: Claim never follows a link in the place of the
// mount root, so guest root's link can't hand a file to root:wheel.
func TestClaimNoFollow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := (&guestmount.System{}).Claim(link)
	// With O_DIRECTORY, darwin fails on the link with ENOTDIR. open(2)
	// documents ELOOP for O_NOFOLLOW alone.
	if !errors.Is(err, unix.ENOTDIR) && !errors.Is(err, unix.ELOOP) {
		t.Fatalf("Claim(link) = %v, want ENOTDIR or ELOOP", err)
	}
	if info, err := os.Lstat(target); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the link's target changed: %v, %v", info, err)
	}
}

// TestMkdirClaims: Mkdir makes a directory owned by root:wheel with
// mode 755. A user who isn't root can't give it to root, and Mkdir says
// so.
func TestMkdirClaims(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	s := &guestmount.System{}
	err := s.Mkdir(dir)
	if os.Geteuid() != 0 {
		if !errors.Is(err, unix.EPERM) {
			t.Fatalf("Mkdir as a user = %v, want EPERM", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Lstat(dir)
	if err != nil || got.UID != 0 || got.GID != 0 || got.Perm != 0o755 {
		t.Fatalf("lstat = %+v, %v; want root:wheel 755", got, err)
	}
}
