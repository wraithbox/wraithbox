package platform

import (
	"context"
	"os/user"
)

// DefaultGitPath is the git binary to run when config.toml has no git
// setting (S08-08-host-git, "Which git"). It is a fixed path, never
// looked up through PATH (SEC03-no-host-exec).
func DefaultGitPath() string { return `C:\Program Files\Git\cmd\git.exe` }

// GitInstallHint says what to do when the default git doesn't run.
func GitInstallHint() string {
	return "install Git for Windows, or set git in config.toml to the absolute path of a git binary"
}

// UserHome returns the current user's profile directory. On Windows
// user.Current asks the process token for it and reads no environment
// variable (S05-04-naming, "Resolving the key").
func UserHome() (string, error) {
	return homeOf(user.Current())
}

// LoginXDGConfigHome returns XDG_CONFIG_HOME from the login environment,
// or "" when it is not set there. Windows hosts come later
// (S12-platforms), so it is never set here.
func LoginXDGConfigHome(_ context.Context) (string, error) { return "", nil }
