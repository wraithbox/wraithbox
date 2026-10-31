package shell

import (
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/sessionid"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termsetup"
)

// The rules of a shell's refusals and failures (S06-09-session-lifecycle,
// "Session helper"). Each is in the gRPC ErrorInfo of a refusal, in a
// failure frame, and in wb-guestd's log.
const (
	// RuleUserUnknown: the user isn't a project user wb-guestd made or
	// checked since it started.
	RuleUserUnknown = "shell-user-unknown"
	// RuleUserPrivileged: the user is root, or in admin or wheel
	// (SEC12-least-privilege).
	RuleUserPrivileged = "shell-user-privileged"
	// RuleUserLocked: the user is locked (S13-01-process-hardening,
	// "Locked users").
	RuleUserLocked = "shell-user-locked"
	// RuleEnv: an environment name outside S16-03-around-the-stream's
	// seven, a name twice, or a value outside the contract's bounds.
	RuleEnv = "shell-env"
	// RuleStartInvalid: the first request isn't a start, a window size
	// is out of range, or the shell id isn't of the S05-04-naming form.
	RuleStartInvalid = "shell-start-invalid"
	// RuleIDInUse: a shell with the id is open.
	RuleIDInUse = "shell-id-in-use"
	// RuleStartFailed: the PTY, the pipes or the helper didn't start.
	RuleStartFailed = "shell-start-failed"
	// RuleHelperReport: a helper report out of form or order
	// (SEC11-root-gains-nothing).
	RuleHelperReport = "session-helper-report"
	// RuleHelperGone: the helper ended or closed its reports without an
	// exit report.
	RuleHelperGone = "session-helper-gone"
	// RuleFrameInvalid: a client frame out of form, which wb-guestd
	// logs and drops.
	RuleFrameInvalid = "shell-frame-invalid"
	// RuleOutsideSession: a process of the user that is in no open
	// shell's POSIX session, which wb-guestd logs and leaves
	// (S13-01-process-hardening, "When a session's client goes", step 4).
	RuleOutsideSession = "shell-process-outside-session"
	// RuleKilled: a shell's processes were killed after the hangup
	// delay (S13-01-process-hardening, "When a session's client goes",
	// step 2).
	RuleKilled = "shell-hangup-kill"
)

// Refusal is a refused start. Invalid is set for a request out of form.
type Refusal struct {
	Rule    string
	Invalid bool
}

func (r *Refusal) Error() string { return "shell: refused: " + r.Rule }

// maxUserName bounds the user name of the start.
const maxUserName = 64

// checkStart checks the start's form, and returns the rule of the first
// problem, or "".
func checkStart(id, user string, setup *terminalv1.Setup) string {
	if !sessionid.Valid(id) || user == "" || len(user) > maxUserName || !termsetup.ValidSize(setup) {
		return RuleStartInvalid
	}
	if !termsetup.ValidEnv(setup.GetEnv()) {
		return RuleEnv
	}
	return ""
}

// The fixed parts of a shell's environment (S13-01-process-hardening,
// "Limits and environment").
const (
	shellPath = "/bin/zsh"
	envPath   = "/usr/bin:/bin:/usr/sbin:/sbin"
)

// environment builds a shell's whole environment from the checked host
// values and u's record. Nothing comes from wb-guestd's own
// environment. The helper adds TMPDIR, which it reads as the user.
func environment(u users.User, env []*terminalv1.EnvVar) []string {
	out := make([]string, 0, len(env)+5)
	for _, v := range env {
		out = append(out, v.GetName()+"="+v.GetValue())
	}
	return append(out,
		"HOME="+u.Home, "USER="+u.Name, "LOGNAME="+u.Name, "SHELL="+shellPath, "PATH="+envPath)
}
