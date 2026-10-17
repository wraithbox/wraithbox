// Command wb-dev-app is the Go half of the macOS bundle build
// (docs/spec/S17-install-and-upgrade/S17-01-bundle.md). scripts/app-bundle.sh,
// which `mise run app:bundle` runs, does the rest. It is a dev tool: it
// isn't in the bundle's layout table, so it never ships, and the check
// refuses it in a bundle.
//
//	wb-dev-app identity                 # print the bundle identifier, or refuse WB_TEAM_ID and a bad WB_BUNDLE_ID
//	wb-dev-app layout                   # print the layout table for the script
//	wb-dev-app go-build -stamp F -out D # build the Go programs of the table for darwin/arm64, stamped
//	wb-dev-app check -app A -disk D -stamp F -bundle-id I [-team T]  # the entitlement check (step 8)
//	wb-dev-app install -app A -to D     # install A as D with the swap of S17-05-upgrades-and-migrations
//
// check and install run codesign and plutil by absolute path, so they
// need macOS.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
)

const usage = "usage: wb-dev-app identity | layout | go-build | check | install (see the package comment)"

// errUsage is a usage error: exit 2.
var errUsage = errors.New(usage)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	err := dispatch(ctx, args, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage), errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(stderr, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "wb-dev-app: FAIL: %v\n", err)
		return 1
	}
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	rest := args[1:]
	switch args[0] {
	case "identity":
		return identity(stdout)
	case "layout":
		for _, line := range appbundle.LayoutLines(appbundle.Programs) {
			fmt.Fprintln(stdout, line)
		}
		return nil
	case "go-build":
		return goBuildCommand(ctx, rest, stdout, stderr)
	case "check":
		return checkCommand(ctx, rest, stdout, stderr)
	case "install":
		return installCommand(ctx, rest, stdout, stderr)
	}
	return fmt.Errorf("unknown command %q: %w", args[0], errUsage)
}

// identity prints the bundle identifier of an ad-hoc build. A set
// WB_TEAM_ID or an invalid WB_BUNDLE_ID fails, before the build builds
// anything (S17-01-bundle, "Signing identity").
func identity(stdout io.Writer) error {
	id, err := appbundle.BundleID(os.Getenv(appbundle.TeamEnv), os.Getenv(appbundle.BundleIDEnv))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, id)
	return nil
}

func installCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	app := fs.String("app", "", "the built bundle")
	to := fs.String("to", "", "where to install it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *app == "" || *to == "" || fs.NArg() != 0 {
		return errUsage
	}
	// Only a bundle that verifies is installed: the built one, and then
	// its copy, before the copy is renamed in.
	verify := func(path string) error {
		if out, err := run1(ctx, codesignPath, "--verify", "--deep", "--strict", path); err != nil {
			return fmt.Errorf("codesign --verify refuses %s: %w: %s", path, err, strings.TrimSpace(out))
		}
		return nil
	}
	if err := verify(*app); err != nil {
		return err
	}
	if err := appbundle.Install(*app, *to, verify, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "install: %s is installed\n", *to)
	return nil
}
