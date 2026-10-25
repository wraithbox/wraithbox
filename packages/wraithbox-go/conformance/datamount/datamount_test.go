//go:build conformance && darwin

package datamount

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// nobody is the uid and gid of macOS's nobody, -2.
const nobody = 4294967294

// inGuest skips the test unless it runs in a VM with a file system
// mounted at guestd.HomePath, and returns that mount and its volume.
func inGuest(t *testing.T) (guestmount.Mount, guestmount.Volume) {
	t.Helper()
	if vmm, err := unix.SysctlUint32("kern.hv_vmm_present"); err != nil || vmm != 1 {
		t.Skipf("not in a guest: kern.hv_vmm_present %d, %v", vmm, err)
	}
	sys := &guestmount.System{Log: slog.New(slog.DiscardHandler)}
	m, err := sys.Statfs(guestd.HomePath)
	if err != nil || m.On != guestd.HomePath {
		t.Skipf("no data volume mounted at %s: %+v, %v", guestd.HomePath, m, err)
	}
	vol, found, err := sys.Info(t.Context(), strings.TrimPrefix(m.From, "/dev/"))
	if err != nil || !found {
		t.Fatalf("diskutil info %s: found %v, %v", m.From, found, err)
	}
	return m, vol
}

func asRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root, as wb-guestd runs")
	}
}

// checked returns a DataMount that took the mounted volume, as
// wb-guestd's does after MountData.
func checked(t *testing.T, vol guestmount.Volume) *guestd.DataMount {
	t.Helper()
	d := guestd.NewDataMount(&guestmount.System{Log: slog.New(slog.DiscardHandler)}, nil, slog.New(slog.DiscardHandler))
	if r := d.Mount(t.Context(), vol.UUID, false); r != nil {
		t.Fatalf("mount of the mounted volume refused: %v", r)
	}
	return d
}

// TestMounted: the data volume carries every flag of the mount, isn't
// encrypted, and passes the check before a session. It runs as any
// user.
func TestMounted(t *testing.T) {
	m, vol := inGuest(t)
	if !m.NoSUID || !m.NoDev || !m.DontBrowse || m.IgnoreOwnership {
		t.Errorf("mount %+v, want nosuid, nodev, nobrowse and owners", m)
	}
	if vol.Encrypted || !vol.EncryptionReported {
		t.Errorf("volume %+v, want it unencrypted, as diskutil reports", vol)
	}
	if r := checked(t, vol).Check(t.Context()); r != nil {
		t.Errorf("check before a session refused: %v", r)
	}
}

// TestSetuidIgnored: a setuid-root copy of id on the data volume runs
// as the user who started it (SEC11-root-gains-nothing). The same copy
// in a temporary directory in /private/var/tmp runs as root,
// so the probe gains root where setuid counts.
func TestSetuidIgnored(t *testing.T) {
	inGuest(t)
	asRoot(t)
	uid := standardUser(t)
	if got := probe(t, tempDir(t, "/private/var/tmp"), uid); got != "0" {
		t.Skipf("the probe runs as %s outside the data volume, not as root, so it proves nothing", got)
	}
	if got, want := probe(t, tempDir(t, guestd.HomePath), uid), strconv.FormatUint(uint64(uid), 10); got != want {
		t.Fatalf("setuid-root probe on the data volume runs as uid %s, want %s", got, want)
	}
}

// TestRemountRefused: after root remounts the data volume without
// nosuid, the check before a session refuses with data-mount-flags. The
// test sets nosuid back, and the check passes again.
func TestRemountRefused(t *testing.T) {
	_, vol := inGuest(t)
	asRoot(t)
	d := checked(t, vol)
	remount(t, "suid")
	t.Cleanup(func() { remount(t, "nosuid") })
	if r := d.Check(t.Context()); r == nil || r.Rule != guestd.RuleMountFlags {
		t.Fatalf("check after a remount without nosuid = %v, want %s", r, guestd.RuleMountFlags)
	}
	remount(t, "nosuid")
	if r := d.Check(t.Context()); r != nil {
		t.Fatalf("check after nosuid was set back = %v", r)
	}
}

// standardUser is the uid sudo ran the test for, or nobody's.
func standardUser(t *testing.T) uint32 {
	t.Helper()
	if s := os.Getenv("SUDO_UID"); s != "" {
		uid, err := strconv.ParseUint(s, 10, 32)
		if err != nil || uid == 0 {
			t.Fatalf("SUDO_UID %q", s)
		}
		return uint32(uid)
	}
	return nobody
}

// tempDir makes a directory in parent that the test removes. Unlike
// t.TempDir's, the directories above it let every user through.
func tempDir(t *testing.T, parent string) string {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "conformance-") //nolint:usetesting // t.TempDir can't take a parent such as the data volume
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// probe copies /usr/bin/id into dir as a setuid-root file, runs it as
// uid, and returns the effective uid it prints.
func probe(t *testing.T, dir string, uid uint32) string {
	t.Helper()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id")
	copyFile(t, "/usr/bin/id", path)
	if err := os.Chown(path, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o4755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), path, "-u")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: nobody}}
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s -u as uid %d: %v", path, uid, err)
	}
	return strings.TrimSpace(string(out))
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// remount changes one option of the data volume's mount, as guest root
// can.
func remount(t *testing.T, option string) {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "/sbin/mount", "-u", "-o", option, guestd.HomePath).CombinedOutput()
	if err != nil {
		t.Fatalf("mount -u -o %s %s: %v\n%s", option, guestd.HomePath, err, out)
	}
}
