package guestd

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/shell"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// WithShellHelper makes Shell start the session helper as path with the
// one argument shell.HelperArg. main passes its own executable, resolved
// once at start, after the update at start. Without it, NewServer
// resolves its own executable.
func WithShellHelper(path string) Option {
	return func(s *service) { s.helper = []string{path, shell.HelperArg} }
}

// newShells returns the shell server of svc, which runs its helpers as
// the project users.
func newShells(svc *service, watch *linkWatch, log *slog.Logger) *shell.Server {
	if svc.helper == nil {
		exe, err := os.Executable()
		if err != nil {
			// Every Shell then fails to start its helper, with
			// shell-start-failed.
			log.LogAttrs(context.Background(), slog.LevelError, "own executable not found",
				slog.String("rule", shell.RuleStartFailed), slog.String("error", err.Error()))
		}
		svc.helper = []string{exe, shell.HelperArg}
	}
	return shell.New(shell.Config{
		Log:     log,
		Users:   svc.users,
		Table:   sessionproc.System{},
		Stopper: svc.loops,
		MountCheck: func(ctx context.Context) string {
			if r := svc.dataMount.Check(ctx); r != nil {
				return r.Rule
			}
			return ""
		},
		Dropped:    watch.dropped,
		Helper:     svc.helper,
		Credential: shell.UserCredential,
		KillDelay:  shell.KillDelay,
	})
}

// Shell runs a program as a project user on the terminal stream
// (S06-09-session-lifecycle, "Session helper"; SEC08-proj-isolation). A
// refused start is INVALID_ARGUMENT for a request out of form, and
// FAILED_PRECONDITION otherwise, with the rule as the ErrorInfo reason.
func (s *service) Shell(stream grpc.BidiStreamingServer[guestv1.ShellRequest, guestv1.ShellResponse]) error {
	err := s.shells.Shell(stream)
	var r *shell.Refusal
	if !errors.As(err, &r) {
		return err
	}
	code := codes.FailedPrecondition
	if r.Invalid {
		code = codes.InvalidArgument
	}
	return withRule(code, r.Rule)
}
