package platform

import (
	"context"
	"os"
	"os/user"
	"strconv"
)

// DefaultGitPath is the git binary to run when config.toml has no git
// setting (S08-08-host-git, "Which git"). It is a fixed path, never
// looked up through PATH (SEC03-no-host-exec).
func DefaultGitPath() string { return "/usr/bin/git" }

// GitInstallHint says what to do when the default git doesn't run.
func GitInstallHint() string {
	return "install git with the distribution's package manager, or set git in config.toml to the absolute path of a git binary"
}

// HasDrivePrefix reports whether a remote URL starts with a drive
// letter, which git reads as a local path (S08-01-into-the-guest,
// "Remote URLs"). Only Windows has drive letters, so git on Linux reads
// `C:/x` as an SSH short form.
func HasDrivePrefix(string) bool { return false }

// UserHome returns the current user's home directory from the user
// database. It looks the user up by id because user.Current falls back
// to $HOME when the lookup fails on Linux without cgo, and HOME must
// never come from the environment (S05-04-naming, "Resolving the key").
func UserHome() (string, error) {
	return homeOf(user.LookupId(strconv.Itoa(os.Getuid())))
}

// LoginXDGConfigHome returns XDG_CONFIG_HOME from the login environment,
// or "" when it is not set there. Linux hosts come later (S12-platforms),
// so it is never set here.
func LoginXDGConfigHome(_ context.Context) (string, error) { return "", nil }
