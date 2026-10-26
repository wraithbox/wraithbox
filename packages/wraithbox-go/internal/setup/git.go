package setup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// RuleGitUnchecked: Env has no git check, so wb setup can't tell that
// the host's git is one Wraith Box runs.
const RuleGitUnchecked Rule = "setup-git-unchecked"

// CheckGitAt is the git check of wb setup: it reads the git setting of
// <configDir>/config.toml, and checks the version of the git binary it
// names, or of the platform default (S08-08-host-git, "Where it is
// checked"). The CheckGit of SystemEnv calls it with <config>.
func CheckGitAt(ctx context.Context, configDir string) error {
	cfg, err := config.Load(ctx, configDir)
	if err != nil {
		return err
	}
	_, err = hostgit.Check(ctx, cfg.Git)
	return err
}

// checkGit runs the git check before wb setup writes anything, so a
// host whose git Wraith Box doesn't run gets no agent and no link
// (S05-03-management-commands, "`wb setup`"). A refused settings file
// or git is a refusal with that rule. An Env without a check fails
// closed.
func checkGit(ctx context.Context, env *Env) error {
	if env.CheckGit == nil {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleGitUnchecked, Msg: "this build of wb setup has no git check", Fix: "report this as a bug in Wraith Box",
		})
	}
	err := env.CheckGit(ctx)
	if err == nil {
		return nil
	}
	file := filepath.Join(env.Paths.Config, config.FileName)
	if r, ok := errors.AsType[*config.Refusal](err); ok {
		return refuse(ctx, env.Log, &Refusal{
			Rule: Rule(r.Rule), Msg: r.Error(), Fix: fmt.Sprintf("Fix %s, then run wb setup again.", file),
		})
	}
	rule := hostgit.RuleRunFailed
	if r, ok := errors.AsType[*hostgit.Refusal](err); ok {
		rule = r.Rule
	}
	return refuse(ctx, env.Log, &Refusal{Rule: Rule(rule), Msg: err.Error(), Fix: gitFix(rule, file)})
}

// gitFix says what to do about a refused git, by the rule that refused
// it (NFR06-explained-refusals).
func gitFix(rule hostgit.Rule, file string) string {
	switch rule {
	case hostgit.RulePathNotAbsolute:
		return fmt.Sprintf("Set git in %s to an absolute path, or remove it to use the default. Then run wb setup again.", file)
	case hostgit.RuleEnvUnavailable:
		return "Fix what keeps git's environment from being built, as the message says. Then run wb setup again."
	case hostgit.RuleVersionTooOld, hostgit.RuleVersionUnreadable:
		return fmt.Sprintf("Wraith Box needs a release of git %s or later: %s, or set git in %s to one. Then run wb setup again.",
			hostgit.MinVersion, platform.GitInstallHint(), file)
	case hostgit.RuleRunFailed, hostgit.RuleVersionAccepted:
		// A failed run, and any rule this switch doesn't name, get the
		// advice below.
	}
	return fmt.Sprintf("Check that the git binary runs, or set git in %s to one that does: %s. Then run wb setup again.",
		file, platform.GitInstallHint())
}
