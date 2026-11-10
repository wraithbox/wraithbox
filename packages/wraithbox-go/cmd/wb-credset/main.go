// Command wb-credset is the helper that `wb cred set`, `wb cred rm` and
// `wb cred list` run as the user: it writes, lists and deletes the
// binding secrets in the secret store. It is the main executable of a
// helper bundle of its own, which holds the `binding` access group in a
// build that a team signs. See
// docs/spec/S15-least-privilege/S15-06-binding-commands.md and
// docs/spec/S10-tech-stack/S10-02-packaging-and-signing.md.
//
// Its start interface: two arguments, the operation (write, delete or
// list) and the binding name, or - for list; an environment with only
// the names on the fixed list of internal/credset, which is empty; for
// write, the value on standard input. It does one store operation and
// exits 0, or 1 after a refusal or a failed store call, with one
// decision line in its JSON log on standard error.
package main

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

//go:embed profile.sb
var profileText string

// profile is wb-credset's compiled-in Seatbelt profile, the store-only
// profile of S04-03-self-sandboxing. It has no parameters.
var profile = selfsandbox.Profile{Name: "wb-credset", Text: profileText}

func main() {
	setLog()
	os.Exit(run(context.Background(), credset.Process{Args: os.Args[1:], Environ: os.Environ(), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}

// noCore and confine are sessionproc.NoCore and selfsandbox.Confine. A
// test makes them refuse.
var (
	noCore  = sessionproc.NoCore
	confine = selfsandbox.Confine
)

// setLog makes the JSON log on standard error the default, which the
// store and the sandbox log to, before anything logs.
func setLog() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With(slog.String("version", version.Build())))
}

// run is wb-credset's main:
//
//  1. it sets its core file limit to 0, so no core file holds the value
//     (S13-01-process-hardening, "Limits and environment"). When that
//     fails it logs a refusal with credset-no-core as its decision, and
//     exits 1;
//  2. it opens the store with its own compiled-in identity. Open turns
//     the Keychain's dialogs off, checks that its signature on disk
//     matches the running code, reads the team from that signature and
//     the groups from the kernel's copy of its entitlements, and takes
//     nothing from its arguments, environment or descriptors. The
//     store-only profile blocks the signature read, so Open comes before
//     the profile (S04-03-self-sandboxing, "`wb-proxyd` and the secret
//     store on macOS"; S15-06-binding-commands, "`wb-credset`"). Each
//     item call then names the fixed account (S15-02-secret-store). An
//     open that fails, such as keychain-needs-team in an ad-hoc build,
//     is logged, and the store then refuses every call with that rule;
//  3. it confines itself with the store-only profile
//     (SEC12-least-privilege). When that fails it logs a refusal with
//     the sandbox's rule, or credset-not-confined, as its decision, and
//     exits 1, before it reads its arguments or its standard input;
//  4. credset.Run checks the arguments and the environment, and makes
//     one store call.
func run(ctx context.Context, p credset.Process) int {
	if err := noCore(); err != nil {
		return credset.RefuseStart(ctx, p.Stderr, credset.RuleNoCore, "the core file limit can't be set to 0: "+err.Error())
	}
	store, err := secretstore.Open(storeitem.ProgramCredset)
	if err != nil {
		// Open logged the rule. Each call on store refuses with it, and
		// credset.Run logs that as its decision.
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "wb-credset: the store didn't open, so its calls are refused", slog.String("error", err.Error()))
	}
	if err := confine(profile, nil); err != nil {
		rule := credset.RuleNotConfined
		if refusal, ok := errors.AsType[*selfsandbox.Refusal](err); ok {
			rule = string(refusal.Rule)
		}
		return credset.RefuseStart(ctx, p.Stderr, rule, "wb-credset can't confine itself: "+err.Error())
	}
	return credset.Run(ctx, p, store)
}
