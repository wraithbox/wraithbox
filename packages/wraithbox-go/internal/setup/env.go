package setup

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// SystemEnv returns the Env of this process: the home directory and
// paths from the user database, never HOME (S12-01-platform-interfaces,
// "Paths"), the PATH variable, os.Executable, the stamped version,
// launchctl through service.Exec, and the wb-hostd client. An error
// along the way goes into Env.Unavailable.
func SystemEnv(log *slog.Logger) Env {
	env := Env{
		Host:               platform.Detect(),
		ReadRelease:        platform.ReadRelease,
		ReadTeam:           codesign.ReadTeam,
		ReadProfile:        codesign.ReadProfile,
		UID:                os.Getuid(),
		PATH:               os.Getenv("PATH"),
		Version:            version.Build(),
		SystemApplications: "/Applications",
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		Log:                log,
	}
	// errors.Join drops the nil errors.
	errs := make([]error, 4)
	env.Home, errs[0] = platform.UserHome()
	env.Paths, errs[1] = paths.Current(log)
	env.Executable, errs[2] = os.Executable()
	env.Service, errs[3] = service.New(service.Config{Home: env.Home, UID: env.UID, Log: log})
	env.Hostd = NewHostd(log, env.Paths.Run, env.Version)
	env.CheckGit = func(ctx context.Context) error { return CheckGitAt(ctx, env.Paths.Config) }
	env.Unavailable = errors.Join(errs...)
	return env
}
