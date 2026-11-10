package credcmd

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"syscall"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The fixes wb prints for wb-credset's rules (S15-02-secret-store,
// "Failed store calls").
const (
	setupFix  = `Run "wb setup" to check the build's signing and its Keychain groups, then run the command again.`
	lockedFix = "Unlock the Mac's own screen, which unlocks the Keychain, then run the command again. A remote or SSH session doesn't unlock it, and wb-credset doesn't wait for the unlock."
	// refusedFix is the fix of credset-refused. wb starts wb-credset only
	// with what it takes, so a refusal means the two come from different
	// builds.
	refusedFix  = `wb and its wb-credset don't agree. Install one build of Wraith Box with "mise run app:install", and run "wb setup".`
	signaledFix = `macOS ends a helper with SIGKILL when it refuses its code signature or its provisioning profile, for example an expired profile. Run "wb setup" to check the build's signing.`
)

// fixes are the fixes of the rules from wb-credset that have one of
// their own. Every other rule gets setupFix.
var fixes = map[string]string{
	// S12-03-host-prerequisites, "Development builds on macOS".
	string(storeitem.RuleNeedsTeam): appbundle.TeamSetupSteps,
	string(storeitem.RuleLocked):    lockedFix,
	credset.RuleRefused:             refusedFix,
}

// fixOf returns the fix of a rule from wb-credset.
func fixOf(rule string) string {
	if fix, ok := fixes[rule]; ok {
		return fix
	}
	return setupFix
}

// report prints the outcome of a run of wb-credset and returns the exit
// status.
func report(ctx context.Context, env Env, c command, helper string, r result) int {
	what := c.op
	if c.binding != "" {
		what += " of the binding " + c.binding
	}
	switch {
	case r.startErr != nil:
		return fail(env, RuleHelperFailed, fmt.Sprintf("%s can't be started: %v", helperName, r.startErr), setupFix)
	case r.signaled:
		name := r.signal.String()
		if r.signal == syscall.SIGKILL {
			name = "SIGKILL"
		}
		return fail(env, RuleHelperSignaled, fmt.Sprintf("%s ended on signal %d (%s) during the %s", helperName, int(r.signal), name, what), signaledFix)
	case r.cut:
		// The decision is the last line, and a cut list could end in part
		// of a name (S04-08-records-and-bounds).
		return fail(env, RuleHelperFailed, fmt.Sprintf("%s wrote more than wb reads during the %s", helperName, what), setupFix)
	case !r.hasDecision:
		return fail(env, RuleHelperFailed, fmt.Sprintf("%s exited with %d during the %s, and logged no decision", helperName, r.exit, what), setupFix)
	case r.exit != 0 || r.decision.Decision != credset.DecisionAccept:
		reason := r.decision.Reason
		if r.decision.Status != 0 {
			reason += fmt.Sprintf(" (status %d)", r.decision.Status)
		}
		return fail(env, r.decision.Rule, fmt.Sprintf("%s refused the %s: %s", helperName, what, reason), fixOf(r.decision.Rule))
	}
	switch c.op {
	case credset.OpWrite:
		fmt.Fprintf(env.Stdout, "Set the binding %s.\n", c.binding)
	case credset.OpDelete:
		if r.decision.Rule == string(storeitem.RuleNotFound) {
			fmt.Fprintf(env.Stderr, "wb cred: the binding %s wasn't in the store, so nothing was removed.\n", c.binding)
			return exitOK
		}
		fmt.Fprintf(env.Stdout, "Removed the binding %s.\n", c.binding)
	default:
		return printList(ctx, env, helper, r.stdout)
	}
	return exitOK
}

// printList prints the binding names wb-credset listed, each checked
// against the name form, then the signing team on standard error, and
// that every program the team signs can reach Wraith Box's groups
// (S15-02-secret-store, "Backend guarantees"). A line that isn't a name
// fails the command.
func printList(ctx context.Context, env Env, helper string, stdout []byte) int {
	var names []string
	for line := range bytes.Lines(stdout) {
		name := string(bytes.TrimSuffix(line, []byte("\n")))
		if _, ok := credset.BindingItem(name); !ok {
			return fail(env, RuleHelperFailed, fmt.Sprintf("%s listed %q, which isn't a binding name", helperName, printable(name)), setupFix)
		}
		names = append(names, name)
	}
	for _, n := range names {
		fmt.Fprintln(env.Stdout, n)
	}
	// <bundle>/Contents/Helpers/wb-credset.app, from its executable's path.
	helperBundle := filepath.Dir(filepath.Dir(filepath.Dir(helper)))
	team, err := env.ReadTeam(ctx, helperBundle)
	switch {
	case err != nil:
		env.Log.LogAttrs(ctx, slog.LevelWarn, "wb cred list: the helper's signing team can't be read", slog.String("error", err.Error()))
		fmt.Fprintf(env.Stderr, "wb cred: the signing team of %s can't be read: %s\n", helperName, printable(err.Error()))
	case team == "":
		fmt.Fprintf(env.Stderr, "wb cred: %s has no team signature.\n", helperName)
	default:
		fmt.Fprintf(env.Stderr, "wb cred: this build is signed by the team %s. Every program that team signs can reach Wraith Box's Keychain groups (%s.nl.wraithbox.*), not only Wraith Box's helpers (S15-02-secret-store, \"Team-wide groups in development builds\").\n", team, team)
	}
	return exitOK
}
