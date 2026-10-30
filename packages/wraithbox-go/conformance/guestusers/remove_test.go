//go:build conformance && darwin

package guestusers

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// The variables that name the project user a check that writes may
// kill or remove. A check that writes skips without its variable, so
// running the suite never ends a session or removes a home by
// accident.
const (
	killEnv   = "WB_CONFORMANCE_KILL_UID"
	removeEnv = "WB_CONFORMANCE_REMOVE_UID"
)

// chosenUser returns the project user whose UID the variable env names,
// or skips the test.
func chosenUser(t *testing.T, env string) users.User {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("set %s to the UID of a project user this check may end", env)
	}
	for _, u := range projectUsers(t, 1) {
		if strconv.FormatUint(uint64(u.UID), 10) == v {
			return u
		}
	}
	t.Fatalf("%s=%s names no project user", env, v)
	return users.User{}
}

// startAs starts name with args as u, in its own session when setsid
// is set, and reaps it when the test ends.
func startAs(t *testing.T, u users.User, setsid bool, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: u.UID, Gid: u.GID}, Setsid: setsid}
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s as %s: %v", name, u.Name, err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
}

// keepAliveAgent loads a LaunchAgent with KeepAlive into u's per-user
// launchd domain, so launchd starts it again after each SIGKILL.
func keepAliveAgent(t *testing.T, u users.User) {
	t.Helper()
	plist := filepath.Join(t.TempDir(), "com.wraithbox.conformance.keepalive.plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.wraithbox.conformance.keepalive</string>
<key>ProgramArguments</key><array><string>/bin/sleep</string><string>3600</string></array>
<key>KeepAlive</key><true/>
<key>RunAtLoad</key><true/>
</dict></plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	domain := "user/" + strconv.FormatUint(uint64(u.UID), 10)
	if out, err := exec.CommandContext(t.Context(), "/bin/launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
		t.Fatalf("launchctl bootstrap %s: %v: %s", domain, err, out)
	}
}

// running returns the processes of uid, or fails the test.
func running(t *testing.T, uid uint32) []gp.Proc {
	t.Helper()
	procs, err := (&gp.System{Log: discard}).ByUID(uid)
	if err != nil {
		t.Fatal(err)
	}
	return procs
}

// TestKillUser: the kill loop ends every process of the user, a
// KeepAlive LaunchAgent, a nohup process and one in its own session
// among them, in one call, and none comes back (S06-10-leftover-processes;
// S13-01-process-hardening, "The kill loop"; SEC08-proj-isolation). It
// ends the user's session: run it only with WB_CONFORMANCE_KILL_UID
// set.
func TestKillUser(t *testing.T) {
	u := chosenUser(t, killEnv)
	keepAliveAgent(t, u)
	startAs(t, u, false, "/usr/bin/nohup", "/bin/sleep", "3600")
	startAs(t, u, true, "/bin/sleep", "3600")
	if n := len(running(t, u.UID)); n < 3 {
		t.Fatalf("%d processes of %s before the kill, want at least 3", n, u.Name)
	}
	res := guestd.SystemProcesses(discard).Kill(t.Context(), u.UID)
	if !res.Clean() {
		t.Fatalf("Kill = %+v, processes left: %v", res, running(t, u.UID))
	}
	if left := running(t, u.UID); len(left) != 0 {
		t.Errorf("processes of %s after a clean kill: %v", u.Name, left)
	}
}

// TestRemoveUser: a removal ends the user's processes, empties and
// removes its home with a file the user made in it, and retires the
// record, which stays with `pwpolicy -authentication-allowed` reporting
// the account disabled (S06-08-projects-and-sessions, "Removing a
// home"; SEC08-proj-isolation). It removes the user: run it only with
// WB_CONFORMANCE_REMOVE_UID set.
func TestRemoveUser(t *testing.T) {
	u := chosenUser(t, removeEnv)
	if out, err := asUser(t, u, "/usr/bin/touch", filepath.Join(u.Home, "made-by-the-user")); err != nil {
		t.Fatalf("touch as %s: %v: %s", u.Name, err, out)
	}
	startAs(t, u, true, "/bin/sleep", "3600")
	sys := &gu.System{Log: discard}
	m := users.New(sys, func() (gu.Root, error) { return gu.OpenRoot(guestd.HomePath) }, discard)
	res := m.Remove(t.Context(), users.Removal{ProjectID: u.ProjectID, UID: u.UID, GID: u.GID, Home: u.Home},
		users.RemoveTools{Kill: guestd.SystemProcesses(discard), Empty: sys, Check: onHomePath})
	if res.Outcome != users.Removed {
		t.Fatalf("Remove = %+v", res)
	}
	if _, err := os.Lstat(u.Home); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the home %s: %v, want it gone", u.Home, err)
	}
	if _, found, err := sys.Read(t.Context(), gu.Users, u.Name); err != nil || !found {
		t.Errorf("the record of %s: found %v, %v, want it kept", u.Name, found, err)
	}
	if out := run(t, "/usr/bin/pwpolicy", "-u", u.Name, "-authentication-allowed"); !strings.Contains(out, "disabled") {
		t.Errorf("pwpolicy -authentication-allowed %s: %s", u.Name, out)
	}
	if again := m.Remove(t.Context(), users.Removal{ProjectID: u.ProjectID, UID: u.UID, GID: u.GID, Home: u.Home},
		users.RemoveTools{Kill: guestd.SystemProcesses(discard), Empty: sys, Check: onHomePath}); again.Outcome != users.Removed {
		t.Errorf("Remove again = %+v", again)
	}
}
