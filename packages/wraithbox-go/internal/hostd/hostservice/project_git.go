package hostservice

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
)

// CheckGit resolves wb-hostd's git from setting, the git key of
// config.toml, and checks its version (S08-08-host-git, "Where it is
// checked"). hostgit logs the decision with its rule. A failed check
// doesn't put wb-hostd in refusing mode: it refuses only a session start
// (ProjectServer.Git).
func CheckGit(ctx context.Context, setting string) *hostdv1.HostGit {
	path, err := hostgit.Resolve(setting)
	if err != nil {
		return gitRefused(hostgit.Result{Rule: hostgit.RulePathNotAbsolute}, err)
	}
	res, err := hostgit.CheckVersion(ctx, path)
	if err != nil {
		return gitRefused(res, err)
	}
	return &hostdv1.HostGit{Binary: res.Binary, Version: res.Version, Rule: string(res.Rule)}
}

// gitRefused builds the result of a failed check. The rule is the
// refusal's. An error without one, which hostgit doesn't return, takes
// fallback's.
func gitRefused(fallback hostgit.Result, err error) *hostdv1.HostGit {
	res := fallback
	if r, ok := errors.AsType[*hostgit.Refusal](err); ok {
		res = r.Result
	}
	if res.Rule == "" || res.Rule == hostgit.RuleVersionAccepted {
		res.Rule = hostgit.RuleRunFailed
	}
	return &hostdv1.HostGit{Binary: res.Binary, Version: res.Version, Rule: string(res.Rule), Message: err.Error()}
}

// gitPassed refuses a session start while wb-hostd's git check hasn't
// passed, with the check's rule and message (S08-08-host-git, "Where it
// is checked").
func (s *ProjectServer) gitPassed() error {
	g := s.Git
	switch {
	case g == nil:
		return &projectRefusal{
			code: codes.FailedPrecondition, rule: RuleGitUnchecked,
			msg: "wb-hostd hasn't checked its git, so no session starts",
		}
	case g.GetRule() != string(hostgit.RuleVersionAccepted):
		return &projectRefusal{
			code: codes.FailedPrecondition, rule: g.GetRule(),
			msg: "wb-hostd's git check failed, so no session starts: " + g.GetMessage(),
		}
	}
	return nil
}
