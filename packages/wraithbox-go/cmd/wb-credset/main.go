// Command wb-credset is the helper that `wb cred set`, `wb cred rm` and
// `wb cred list` run as the user: it writes, lists and deletes the
// binding secrets in the secret store. It is the main executable of a
// helper bundle of its own, which holds the `binding` access group in a
// build that a team signs. See
// docs/spec/S15-least-privilege/S15-02-secret-store.md and
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
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
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
	os.Exit(run(context.Background(), os.Args[1:], os.Environ()))
}

// setLog makes the JSON log on standard error the default, which the
// store and the sandbox log to, before anything logs.
func setLog() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With(slog.String("version", version.Build())))
}

// run is wb-credset's main, in the order of S15-02-secret-store,
// "`wb-credset`":
//
//  1. it opens the store with its own compiled-in identity, which turns
//     the Keychain's dialogs off and reads the team and the groups of its
//     own signature. An open that fails, such as keychain-needs-team in
//     an ad-hoc build, is logged, and the store then refuses every call
//     with that rule;
//  2. it confines itself with the store-only profile
//     (SEC12-least-privilege), and exits 1 when that fails, before it
//     reads its arguments;
//  3. credset.Run checks the arguments and the environment, and makes
//     one store call.
func run(ctx context.Context, args, environ []string) int {
	store, err := secretstore.Open(storeitem.ProgramCredset)
	if err != nil {
		// Open logged the rule. Each call on store refuses with it, and
		// credset.Run logs that as its decision.
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "wb-credset: the store didn't open, so its calls are refused", slog.String("error", err.Error()))
	}
	if err := selfsandbox.Confine(profile, nil); err != nil {
		return 1
	}
	return credset.Run(ctx, credset.Process{Args: args, Environ: environ, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}, store)
}
