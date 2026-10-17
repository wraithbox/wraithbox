package setup

import (
	"context"
	"log/slog"
)

// Rule names the rule behind a refusal (NFR06-explained-refusals).
type Rule string

// The rules of wb setup. Each refusal has its own.
const (
	// RuleHostLater: the host OS is later in the platform matrix
	// (S12-01-platform-interfaces, "Unimplemented platforms fail
	// clearly").
	RuleHostLater Rule = "setup-host-later"
	// RuleHostUnsupported: the Mac doesn't meet the prerequisites of
	// S12-03-host-prerequisites: Apple Silicon and the minimum macOS.
	RuleHostUnsupported Rule = "setup-host-unsupported"
	// RuleNotInBundle: wb's real path isn't Contents/MacOS/wb of a bundle
	// whose Info.plist has wb's own build version (S17-02-setup, "The
	// bundle check").
	RuleNotInBundle Rule = "setup-not-in-bundle"
	// RuleBundleLocation: the bundle isn't directly in ~/Applications or
	// /Applications (S17-02-setup, "The bundle check").
	RuleBundleLocation Rule = "setup-bundle-location"
	// RuleLinkExists: ~/.local/bin/wb is a file that isn't a link into a
	// Wraith Box bundle (S17-02-setup, "The link").
	RuleLinkExists Rule = "setup-link-exists"
	// RuleVMsRunning: replacing the agent would stop wb-hostd while a VM
	// may run (S17-02-setup, "The agent").
	RuleVMsRunning Rule = "setup-vms-running"
	// RuleKeychainNeedsTeam: the build has no Keychain backend, because
	// no team signed it (S15-02-secret-store, "Ad-hoc signed builds").
	RuleKeychainNeedsTeam Rule = "keychain-needs-team"
)

// Refusal is the error of every refusal of wb setup. It says what was
// refused and the fix (NFR06-explained-refusals).
type Refusal struct {
	Rule Rule
	// Msg says what is wrong, and Fix what the user can do about it.
	Msg string
	Fix string
}

func (r *Refusal) Error() string { return string(r.Rule) + ": " + r.Msg }

// refuse logs r with its rule and returns it.
func refuse(ctx context.Context, log *slog.Logger, r *Refusal) *Refusal {
	log.LogAttrs(ctx, slog.LevelWarn, "wb setup refused",
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
		slog.String("reason", r.Msg))
	return r
}

// decide logs a decision that lets wb setup go on, with the rule that
// made it.
func decide(ctx context.Context, log *slog.Logger, rule Rule, decision, reason string) {
	log.LogAttrs(ctx, slog.LevelInfo, "wb setup decided",
		slog.String("rule", string(rule)),
		slog.String("decision", decision),
		slog.String("reason", reason))
}
