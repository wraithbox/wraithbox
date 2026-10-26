//go:build conformance && darwin

package guestusers

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// The groups opendirectoryd gives every local user without a record
// naming it: everyone and localaccounts.
var implicitGroups = []uint32{12, 61}

var discard = slog.New(slog.DiscardHandler)

// projectUsers skips the test unless it runs as root in a guest with
// at least n project users, and returns them as their records say,
// sorted by UID.
func projectUsers(t *testing.T, n int) []users.User {
	t.Helper()
	if vmm, err := unix.SysctlUint32("kern.hv_vmm_present"); err != nil || vmm != 1 {
		t.Skipf("not in a guest: kern.hv_vmm_present %d, %v", vmm, err)
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root, as wb-guestd runs")
	}
	sys := &gu.System{Log: discard}
	list, err := sys.List(t.Context(), gu.Users, []string{gu.AttrRecordName, gu.AttrUniqueID})
	if err != nil {
		t.Fatal(err)
	}
	var us []users.User
	for _, rec := range list {
		uid, err := strconv.ParseUint(first(rec[gu.AttrUniqueID]), 10, 32)
		if err != nil || !projectuser.InRange(uint32(uid)) {
			continue
		}
		us = append(us, readUser(t, sys, first(rec[gu.AttrRecordName])))
	}
	if len(us) < n {
		t.Skipf("%d project users, the check needs %d", len(us), n)
	}
	slices.SortFunc(us, func(a, b users.User) int { return int(a.UID) - int(b.UID) })
	return us
}

// readUser reads a project user's values back from its records. The
// guest doesn't keep the project id, so a fixed one stands in.
func readUser(t *testing.T, sys *gu.System, name string) users.User {
	t.Helper()
	userRec, found, err := sys.Read(t.Context(), gu.Users, name)
	if err != nil || !found {
		t.Fatalf("read user %s: found %v, %v", name, found, err)
	}
	groupRec, found, err := sys.Read(t.Context(), gu.Groups, name)
	if err != nil || !found {
		t.Fatalf("read group %s: found %v, %v", name, found, err)
	}
	uid, _ := strconv.ParseUint(first(userRec[gu.AttrUniqueID]), 10, 32)
	gid, _ := strconv.ParseUint(first(userRec[gu.AttrPrimaryGroupID]), 10, 32)
	for _, a := range []string{gu.AttrAuthAuthority, "dsAttrTypeNative:ShadowHashData"} {
		if len(userRec[a]) > 0 {
			t.Errorf("%s has %s", name, a)
		}
	}
	if !slices.Equal(userRec[gu.AttrPassword], []string{users.NoPassword}) {
		t.Errorf("%s's Password is %q, want %q", name, userRec[gu.AttrPassword], users.NoPassword)
	}
	return users.User{
		ProjectID: strings.Repeat("0", 32),
		Name:      name,
		UID:       uint32(uid),
		GID:       uint32(gid),
		GroupName: name,
		UserGUID:  first(userRec[gu.AttrGeneratedUID]),
		GroupGUID: first(groupRec[gu.AttrGeneratedUID]),
		Home:      first(userRec[gu.AttrHome]),
	}
}

func first(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return string(out)
}

// TestNoSudo: no project user may run anything with sudo
// (SEC12-least-privilege).
func TestNoSudo(t *testing.T) {
	for _, u := range projectUsers(t, 1) {
		out := run(t, "/usr/bin/sudo", "-n", "-l", "-U", u.Name)
		if !strings.Contains(out, "not allowed to run sudo") {
			t.Errorf("sudo -l -U %s: %s", u.Name, out)
		}
	}
}

// TestIDs: each user has its own UID and GID, and no group but its own
// and the implicit ones.
func TestIDs(t *testing.T) {
	for _, u := range projectUsers(t, 1) {
		if got := strings.TrimSpace(run(t, "/usr/bin/id", "-u", u.Name)); got != strconv.FormatUint(uint64(u.UID), 10) {
			t.Errorf("id -u %s: %s", u.Name, got)
		}
		if got := strings.TrimSpace(run(t, "/usr/bin/id", "-g", u.Name)); got != strconv.FormatUint(uint64(u.GID), 10) {
			t.Errorf("id -g %s: %s", u.Name, got)
		}
		for g := range strings.FieldsSeq(run(t, "/usr/bin/id", "-G", u.Name)) {
			n, err := strconv.ParseUint(g, 10, 32)
			if err != nil || (uint32(n) != u.GID && !slices.Contains(implicitGroups, uint32(n))) {
				t.Errorf("%s is in group %s", u.Name, g)
			}
		}
	}
}

// TestNotAdmin: dsmemberutil says no project user is in admin.
func TestNotAdmin(t *testing.T) {
	for _, u := range projectUsers(t, 1) {
		out := run(t, "/usr/bin/dsmemberutil", "checkmembership", "-U", u.Name, "-G", "admin")
		if !strings.Contains(out, "user is not a member of the group") {
			t.Errorf("dsmemberutil checkmembership -U %s -G admin: %s", u.Name, out)
		}
	}
}

// TestNoSecureToken: no project user has a secure token, so none can
// unlock FileVault or approve a change that needs one.
func TestNoSecureToken(t *testing.T) {
	for _, u := range projectUsers(t, 1) {
		out := run(t, "/usr/sbin/sysadminctl", "-secureTokenStatus", u.Name)
		if !strings.Contains(out, "DISABLED") {
			t.Errorf("sysadminctl -secureTokenStatus %s: %s", u.Name, out)
		}
	}
}

// TestHomes: each home is a real directory under the mount point,
// owned by the user and its group, mode 700 (S06-05-disks, "Owners and
// modes").
func TestHomes(t *testing.T) {
	for _, u := range projectUsers(t, 1) {
		if u.Home != filepath.Join(guestd.HomePath, u.Name) {
			t.Errorf("%s's home is %s", u.Name, u.Home)
			continue
		}
		fi, err := os.Lstat(u.Home)
		if err != nil {
			t.Errorf("%s: %v", u.Name, err)
			continue
		}
		st, _ := fi.Sys().(*syscall.Stat_t)
		if !fi.IsDir() || fi.Mode()&os.ModeType != os.ModeDir || fi.Mode().Perm() != 0o700 ||
			fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || st.Uid != u.UID || st.Gid != u.GID {
			t.Errorf("%s's home: mode %v, uid %d, gid %d", u.Name, fi.Mode(), st.Uid, st.Gid)
		}
	}
}

// TestHomesApart: a project user lists its own home and can't list
// another's (SEC08-proj-isolation).
func TestHomesApart(t *testing.T) {
	us := projectUsers(t, 2)
	a, b := us[0], us[1]
	if out, err := asUser(t, a, "/bin/ls", a.Home); err != nil {
		t.Fatalf("%s can't list its own home: %v: %s", a.Name, err, out)
	}
	out, err := asUser(t, a, "/bin/ls", b.Home)
	if err == nil || !strings.Contains(out, "Permission denied") {
		t.Errorf("%s listed %s: %v: %s", a.Name, b.Home, err, out)
	}
}

func asUser(t *testing.T, u users.User, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: u.UID, Gid: u.GID}}
	cmd.Dir = "/"
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// errNoWrites is the error of every write of readOnly and readOnlyRoot.
var errNoWrites = errors.New("conformance: no writes")

// readOnly is the guest's directory with every write refused, so a
// check of EnsureUsers' code can't change a record.
type readOnly struct{ *gu.System }

func (readOnly) Create(context.Context, gu.Kind, string, string, string) error { return errNoWrites }

// readOnlyRoot is the volume's root with Mkdir refused.
type readOnlyRoot struct{ gu.Root }

func (readOnlyRoot) Mkdir(string, uint32, uint32) error { return errNoWrites }

// TestEnsureKeepsAndRefuses: EnsureUsers' code keeps each user as it is,
// with nothing to write, and refuses one whose GeneratedUID differs from
// its record before any write.
func TestEnsureKeepsAndRefuses(t *testing.T) {
	us := projectUsers(t, 1)
	m := users.New(readOnly{&gu.System{Log: discard}},
		func() (gu.Root, error) {
			root, err := gu.OpenRoot(guestd.HomePath)
			if err != nil {
				return nil, err
			}
			return readOnlyRoot{root}, nil
		}, discard)
	for i, r := range m.Ensure(t.Context(), us) {
		if r.Rule != "" {
			t.Errorf("%s refused with %s", us[i].Name, r.Rule)
		}
	}
	changed := us[0]
	changed.UserGUID = projectuser.NewGeneratedUID()
	if got := m.Ensure(t.Context(), []users.User{changed}); got[0].Rule != users.RuleConflict {
		t.Errorf("%s with another GeneratedUID: rule %q, want %q", changed.Name, got[0].Rule, users.RuleConflict)
	}
}
