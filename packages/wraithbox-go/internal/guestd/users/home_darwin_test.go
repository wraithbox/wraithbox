package users_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// mapRoot is a real Root on a temporary directory that shows the test's
// own UID and GID as the project user's 10000, so the checks run on real
// fstatat, mkdirat and fchown calls without root.
type mapRoot struct {
	gu.Root
	uid, gid uint32
}

const mapped = 10000

func (r *mapRoot) toUser(e gu.Entry) gu.Entry {
	if e.UID == r.uid {
		e.UID = mapped
	}
	if e.GID == r.gid {
		e.GID = mapped
	}
	return e
}

func (r *mapRoot) Stat(name string) (gu.Entry, error) {
	e, err := r.Root.Stat(name)
	return r.toUser(e), err
}

func (r *mapRoot) Mkdir(name string, uid, gid uint32) error {
	if uid == mapped {
		uid = r.uid
	}
	if gid == mapped {
		gid = r.gid
	}
	return r.Root.Mkdir(name, uid, gid)
}

func (r *mapRoot) Entries(limit int) ([]gu.Entry, error) {
	es, err := r.Root.Entries(limit)
	for i := range es {
		es[i] = r.toUser(es[i])
	}
	return es, err
}

// realRoot returns a Manager over a fake directory and a mapRoot on a
// new temporary directory.
func realRoot(t *testing.T) (*users.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	open := func() (gu.Root, error) {
		root, err := gu.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		return &mapRoot{Root: root, uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}, nil
	}
	return users.New(newFakeDir(), open, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))), dir
}

// TestRealHomeMade: the new home is a real directory, mode 700 under any
// umask, owned by the user.
func TestRealHomeMade(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	m, dir := realRoot(t)
	u := user(mapped)
	if got := m.Ensure(t.Context(), []users.User{u}, rootOK); got[0].Rule != "" {
		t.Fatalf("refused: %s", got[0].Rule)
	}
	fi, err := os.Lstat(filepath.Join(dir, u.Name))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 || st.Uid != uint32(os.Getuid()) || st.Gid != uint32(os.Getgid()) {
		t.Errorf("home mode %v uid %d gid %d", fi.Mode(), st.Uid, st.Gid)
	}
}

// TestRealHomeLink: a link at the home path is refused, and neither it
// nor its target changes.
func TestRealHomeLink(t *testing.T) {
	m, dir := realRoot(t)
	u := user(mapped)
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, u.Name)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := m.Ensure(t.Context(), []users.User{u}, rootOK); got[0].Rule != users.RuleHomeInvalid {
		t.Errorf("rule %q, want %q", got[0].Rule, users.RuleHomeInvalid)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link now %v, %v", fi, err)
	}
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("target now %v, %v", fi, err)
	}
}

// TestRealHomeMode: an existing home with mode 755 is refused and kept.
func TestRealHomeMode(t *testing.T) {
	m, dir := realRoot(t)
	u := user(mapped)
	home := filepath.Join(dir, u.Name)
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := m.Ensure(t.Context(), []users.User{u}, rootOK); got[0].Rule != users.RuleHomeInvalid {
		t.Errorf("rule %q, want %q", got[0].Rule, users.RuleHomeInvalid)
	}
	if fi, err := os.Stat(home); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("home now %v, %v", fi, err)
	}
}

// TestRealIDsOwnEntry: a file the user's UID owns at the root refuses
// the new user, and no home is made.
func TestRealIDsOwnEntry(t *testing.T) {
	m, dir := realRoot(t)
	u := user(mapped)
	if err := os.WriteFile(filepath.Join(dir, "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.Ensure(t.Context(), []users.User{u}, rootOK); got[0].Rule != users.RuleIDsOwnEntry {
		t.Errorf("rule %q, want %q", got[0].Rule, users.RuleIDsOwnEntry)
	}
	if _, err := os.Lstat(filepath.Join(dir, u.Name)); !os.IsNotExist(err) {
		t.Errorf("home made: %v", err)
	}
}
