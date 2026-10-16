package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultGitPath is the git binary to run when config.toml has no git
// setting (S08-08-host-git, "Which git"). It is a fixed path, never
// looked up through PATH (SEC03-no-host-exec).
func DefaultGitPath() string { return "/usr/bin/git" }

// GitInstallHint says what to do when the default git doesn't run.
// Without the Command Line Tools, /usr/bin/git is a stub that doesn't
// run git (S08-08-host-git, "Which git").
func GitInstallHint() string {
	return "install the Command Line Tools (xcode-select --install), or set git in config.toml to the absolute path of a git binary"
}

// UserHome returns the current user's home directory from the user
// database. On darwin os/user asks libSystem, also without cgo. It looks
// the user up by id, like the other Unix files, and never reads $HOME
// (S05-04-naming, "Resolving the key").
func UserHome() (string, error) {
	return homeOf(user.LookupId(strconv.Itoa(os.Getuid())))
}

const (
	// launchctlPath is a fixed path, never looked up through PATH
	// (SEC03-no-host-exec).
	launchctlPath = "/bin/launchctl"
	// loginEnvTimeout bounds one launchctl run.
	loginEnvTimeout = 5 * time.Second
	// loginEnvMaxOutput bounds what is read from launchctl: a path of
	// PATH_MAX (1024 on macOS) and its newline fit.
	loginEnvMaxOutput = 4096
)

// LoginXDGConfigHome returns XDG_CONFIG_HOME from the login environment,
// or "" when it is not set there or is not an absolute path
// (S05-04-naming, "Resolving the key"). The login environment is
// launchd's environment for the user's session. A variable exported in a
// shell, or set by a .envrc, doesn't reach it. A failed lookup is an
// error, not "not set".
func LoginXDGConfigHome(ctx context.Context) (string, error) {
	return loginEnv(ctx, "XDG_CONFIG_HOME")
}

// loginEnv runs `launchctl getenv <name>` with an empty environment, so
// nothing from the caller's environment reaches it (SEC12-least-privilege).
func loginEnv(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginEnvTimeout)
	defer cancel()

	out := &cappedBuffer{limit: loginEnvMaxOutput}
	cmd := exec.CommandContext(ctx, launchctlPath, "getenv", name)
	cmd.Env = []string{}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s getenv %s: %w", launchctlPath, name, err)
	}
	if out.over {
		return "", fmt.Errorf("%s getenv %s: %w", launchctlPath, name, errLoginEnvTooLong)
	}
	return loginValue(string(out.buf)), nil
}

var errLoginEnvTooLong = errors.New("output longer than " + strconv.Itoa(loginEnvMaxOutput) + " bytes")

// loginValue reads launchctl's output: the value and a newline, or
// nothing when the variable is not set. A value that is not a single
// absolute path counts as not set.
func loginValue(out string) string {
	v := strings.TrimSuffix(out, "\n")
	if strings.ContainsAny(v, "\n\x00") || !filepath.IsAbs(v) {
		return ""
	}
	return v
}

// cappedBuffer keeps the first limit bytes written to it and drops the
// rest, so a child process can't make the caller hold unbounded output.
type cappedBuffer struct {
	buf   []byte
	limit int
	over  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - len(c.buf)
	if len(p) > room {
		c.over = true
		c.buf = append(c.buf, p[:room]...)
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}
