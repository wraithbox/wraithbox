//go:build conformance && darwin

package guestusers

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// keepAliveLabel is the label of the LaunchAgent keepAliveAgent loads.
const keepAliveLabel = "com.wraithbox.conformance.keepalive"

// comeBackWait is how long TestKillUser waits before it looks for a
// process launchd started again.
const comeBackWait = 5 * time.Second

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
// launchd domain, so launchd starts it again after each SIGKILL. A
// project user never logs in, so its domain is a Background session,
// and launchd loads only an agent limited to that session type there.
func keepAliveAgent(t *testing.T, u users.User) {
	t.Helper()
	plist := filepath.Join(t.TempDir(), keepAliveLabel+".plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + keepAliveLabel + `</string>
<key>ProgramArguments</key><array><string>/bin/sleep</string><string>3600</string></array>
<key>KeepAlive</key><true/>
<key>RunAtLoad</key><true/>
<key>LimitLoadToSessionType</key><string>Background</string>
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

// startDetached starts a sleep as u that its parent shell leaves
// behind, with SIGHUP ignored and no terminal, as nohup and a double
// fork leave one. launchd adopts it, so the test never reaps it.
// nohup itself exits at once as a project user, which has no console
// to detach from.
func startDetached(t *testing.T, u users.User) {
	t.Helper()
	if out, err := asUser(t, u, "/bin/sh", "-c", `trap "" HUP; /bin/sleep 3600 </dev/null >/dev/null 2>&1 &`); err != nil {
		t.Fatalf("detached sleep as %s: %v: %s", u.Name, err, out)
	}
}

// running returns the processes of uid that haven't exited, or fails
// the test. A zombie doesn't count, as in the kill loop: the setsid
// child is the test's own, and it reaps it only when the test ends.
func running(t *testing.T, uid uint32) []gp.Proc {
	t.Helper()
	procs, err := (&gp.System{Log: discard}).ByUID(uid)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(procs, func(p gp.Proc) bool { return p.State == gp.Zombie })
}

// waitRunning waits up to comeBackWait for at least n processes of u,
// or fails the test: launchd starts the agent after bootstrap returns.
func waitRunning(t *testing.T, u users.User, n int) {
	t.Helper()
	deadline := time.Now().Add(comeBackWait)
	for len(running(t, u.UID)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("processes of %s before the kill: %v, want at least %d", u.Name, running(t, u.UID), n)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestKillUser: the kill loop ends every process of the user, a
// KeepAlive LaunchAgent, a detached process and one in its own session
// among them, in one call. None comes back comeBackWait later, and the
// user's launchd domain no longer holds the agent
// (S06-10-leftover-processes;
// S13-01-process-hardening, "Per-user launchd services"; SEC08-proj-isolation). It
// ends the user's session: run it only with WB_CONFORMANCE_KILL_UID
// set.
func TestKillUser(t *testing.T) {
	u := chosenUser(t, killEnv)
	keepAliveAgent(t, u)
	startDetached(t, u)
	startAs(t, u, true, "/bin/sleep", "3600")
	waitRunning(t, u, 3)
	res := guestd.SystemProcesses(discard).Kill(t.Context(), u.UID)
	if !res.Clean() {
		t.Fatalf("Kill = %+v, processes left: %v", res, running(t, u.UID))
	}
	if left := running(t, u.UID); len(left) != 0 {
		t.Errorf("processes of %s after a clean kill: %v", u.Name, left)
	}
	time.Sleep(comeBackWait)
	if left := running(t, u.UID); len(left) != 0 {
		t.Errorf("processes of %s %v after a clean kill: %v", u.Name, comeBackWait, left)
	}
	// wb-guestd never runs `launchctl print` against a locked user. This
	// check isn't wb-guestd.
	domain := "user/" + strconv.FormatUint(uint64(u.UID), 10)
	out, err := exec.CommandContext(t.Context(), "/bin/launchctl", "print", domain).CombinedOutput()
	if err == nil && strings.Contains(string(out), keepAliveLabel) {
		t.Errorf("launchctl print %s still names %s:\n%s", domain, keepAliveLabel, out)
	}
}

// TestRemoveUser: a removal ends the user's processes, empties and
// removes its home with a file the user made in it and a directory the
// user set to mode 000 with a file in it, and retires the
// record, which stays with `pwpolicy -authentication-allowed` reporting
// the account disabled (S06-08-projects-and-sessions, "Removing a
// home"; SEC08-proj-isolation). It removes the user: run it only with
// WB_CONFORMANCE_REMOVE_UID set.
func TestRemoveUser(t *testing.T) {
	u := chosenUser(t, removeEnv)
	closed := filepath.Join(u.Home, "closed-by-the-user")
	for _, c := range [][]string{
		{"/usr/bin/touch", filepath.Join(u.Home, "made-by-the-user")},
		{"/bin/mkdir", closed},
		{"/usr/bin/touch", filepath.Join(closed, "inside")},
		{"/bin/chmod", "000", closed},
	} {
		if out, err := asUser(t, u, c[0], c[1:]...); err != nil {
			t.Fatalf("%v as %s: %v: %s", c, u.Name, err, out)
		}
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
