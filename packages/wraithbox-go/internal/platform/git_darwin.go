package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
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
	// ruleLoginValueRejected names the rule that ignores a login value
	// that is not a clean absolute path.
	ruleLoginValueRejected = "login-env-value-rejected"
)

// LoginXDGConfigHome returns XDG_CONFIG_HOME from the login environment,
// or "" when it is not set there (S05-04-naming, "Resolving the key").
// The login environment is launchd's environment for the user's
// session. A variable exported in a shell, or set by a .envrc, doesn't
// reach it. A failed lookup is an error, not "not set". A value that
// loginValue rejects counts as not set, with a warning in the log.
func LoginXDGConfigHome(ctx context.Context) (string, error) {
	return loginEnv(ctx, launchctlPath, "XDG_CONFIG_HOME")
}

// loginEnv runs `<launchctl> getenv <name>` with an empty environment, so
// nothing from the caller's environment reaches it
// (SEC12-least-privilege). A run that fails, times out, or prints more
// than loginEnvMaxOutput bytes is an error.
func loginEnv(ctx context.Context, launchctl, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginEnvTimeout)
	defer cancel()

	out := &cappedBuffer{limit: loginEnvMaxOutput}
	cmd := exec.CommandContext(ctx, launchctl, "getenv", name)
	cmd.Env = []string{}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s getenv %s: %w", launchctl, name, err)
	}
	if out.over {
		return "", fmt.Errorf("%s getenv %s: %w", launchctl, name, errLoginEnvTooLong)
	}
	v, reason := loginValue(string(out.buf))
	if reason != "" {
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "login environment value ignored",
			slog.String("rule", ruleLoginValueRejected),
			slog.String("variable", name),
			slog.String("reason", reason),
			slog.String("value", strconv.QuoteToASCII(string(out.buf))))
	}
	return v, nil
}

var errLoginEnvTooLong = errors.New("output longer than " + strconv.Itoa(loginEnvMaxOutput) + " bytes")

// loginValue reads launchctl's output: the value and a newline, or
// nothing when the variable is not set. It accepts only an absolute path
// of printable ASCII with no `..` component. Otherwise it returns "" and
// the reason.
func loginValue(out string) (value, reason string) {
	v := strings.TrimSuffix(out, "\n")
	switch {
	case v == "":
		return "", ""
	case !printableASCII(v):
		return "", "not printable ASCII"
	case !filepath.IsAbs(v):
		return "", "not an absolute path"
	case slices.Contains(strings.Split(v, "/"), ".."):
		return "", "has a .. component"
	}
	return v, ""
}

func printableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
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
