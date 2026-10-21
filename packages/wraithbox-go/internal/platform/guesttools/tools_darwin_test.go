package guesttools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func ruleOf(err error) string {
	if r, ok := errors.AsType[*Refusal](err); ok {
		return r.Rule
	}
	return ""
}

// statfs returns what Disk Arbitration's mount of the tools disk looks
// like, changed by f.
func statfs(f func(*unix.Statfs_t)) *unix.Statfs_t {
	var st unix.Statfs_t
	copy(st.Mntonname[:], MountPoint)
	copy(st.Fstypename[:], "apfs")
	st.Flags = unix.MNT_RDONLY | unix.MNT_LOCAL
	f(&st)
	return &st
}

// TestCheckStatfs: only an APFS volume mounted read-only by root at the
// mount point passes.
func TestCheckStatfs(t *testing.T) {
	cases := []struct {
		name string
		f    func(*unix.Statfs_t)
		rule string
	}{
		{"the tools disk", func(*unix.Statfs_t) {}, ""},
		{"nothing mounted there", func(st *unix.Statfs_t) {
			st.Mntonname = [1024]byte{}
			copy(st.Mntonname[:], "/System/Volumes/Data")
		}, RuleNotMounted},
		{"read-write", func(st *unix.Statfs_t) { st.Flags = unix.MNT_LOCAL }, RuleUntrusted},
		{"mounted by a user", func(st *unix.Statfs_t) { st.Owner = 502 }, RuleUntrusted},
		{"not apfs", func(st *unix.Statfs_t) {
			st.Fstypename = [16]byte{}
			copy(st.Fstypename[:], "hfs")
		}, RuleUntrusted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ruleOf(checkStatfs(MountPoint, statfs(tc.f))); got != tc.rule {
				t.Fatalf("rule %q, want %q", got, tc.rule)
			}
		})
	}
}

// TestOpenDisk: a path with no volume mounted on it is refused, and the
// sealed system volume, read-only and mounted by root, passes.
func TestOpenDisk(t *testing.T) {
	for _, p := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if root, err := OpenDisk(p); ruleOf(err) != RuleNotMounted || root != nil {
			t.Fatalf("OpenDisk(%s) = %v, %v, want %s", p, root, err, RuleNotMounted)
		}
	}
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil || st.Flags&unix.MNT_RDONLY == 0 {
		t.Skipf("/ isn't a read-only volume here: %v", err)
	}
	root, err := OpenDisk("/")
	if err != nil {
		t.Fatalf("OpenDisk(/) = %v", err)
	}
	defer root.Close()
	if _, err := root.Stat("System"); err != nil {
		t.Fatalf("read through the root: %v", err)
	}
}

// TestCheckDir: a directory passes only when it isn't a link, belongs to
// the owner, and its group and others can't write it. A path that can't
// be looked at fails.
func TestCheckDir(t *testing.T) {
	me := uint32(os.Getuid())
	dir := t.TempDir()
	mk := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := mk("good", 0o755)
	link := filepath.Join(dir, "link")
	file := filepath.Join(dir, "file")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		path  string
		owner uint32
		rule  string
	}{
		{"owned, 0755", good, me, ""},
		{"owned, 0700", mk("private", 0o700), me, ""},
		{"group can write", mk("group", 0o775), me, RuleDirUnsafe},
		{"others can write", mk("others", 0o757), me, RuleDirUnsafe},
		{"another owner", good, me + 1, RuleDirUnsafe},
		{"a symbolic link", link, me, RuleDirUnsafe},
		{"a file", file, me, RuleDirUnsafe},
		{"missing", filepath.Join(dir, "missing"), me, RuleDirFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDir(tc.path, tc.owner)
			if ruleOf(err) != tc.rule || (tc.rule == "") != (err == nil) {
				t.Fatalf("checkDir = %v, want rule %q", err, tc.rule)
			}
		})
	}
}

// TestPrepareDirRefuses: a relative path is refused, and so is a path
// with a parent that is a link or that root doesn't own, before anything
// is made under it.
func TestPrepareDirRefuses(t *testing.T) {
	if err := PrepareDir("Library/WraithBox"); ruleOf(err) != RuleDirUnsafe {
		t.Fatalf("PrepareDir(relative) = %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("runs as root, which owns the temporary directory")
	}
	dir := filepath.Join(t.TempDir(), "libexec")
	if err := PrepareDir(dir); ruleOf(err) != RuleDirUnsafe {
		t.Fatalf("PrepareDir = %v, want %s", err, RuleDirUnsafe)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PrepareDir made %s: %v", dir, err)
	}
}

// TestPathAndParents: "/" first, the path itself last.
func TestPathAndParents(t *testing.T) {
	got := pathAndParents("/Library/WraithBox/libexec/")
	want := []string{"/", "/Library", "/Library/WraithBox", "/Library/WraithBox/libexec"}
	if len(got) != len(want) {
		t.Fatalf("pathAndParents = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pathAndParents = %q, want %q", got, want)
		}
	}
}

// TestExecFails: Exec returns the error of an execve that fails.
func TestExecFails(t *testing.T) {
	if err := Exec(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, unix.ENOENT) {
		t.Fatalf("Exec = %v, want ENOENT", err)
	}
}
