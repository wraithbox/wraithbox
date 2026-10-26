package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
)

// securityPath is the tool sign-identity runs, by absolute path.
const securityPath = "/usr/bin/security"

// layoutCommand prints the layout table for the script. With -team it
// writes each helper's entitlements file into -dir, keychain-access-groups
// with its row's groups and nothing else, and prints that file's absolute
// path in the helper's line (S10-02-packaging-and-signing, "Helper
// bundles").
func layoutCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("layout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	team := fs.String("team", "", "the team that signs the build; empty for an ad-hoc build")
	dir := fs.String("dir", "", "where to write the helpers' entitlements files, with -team")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*team == "") != (*dir == "") {
		return errUsage
	}
	if *team != "" && !appbundle.ValidTeamID(*team) {
		return fmt.Errorf("-team %q is not an Apple team ID", *team)
	}
	lines := appbundle.LayoutLines(appbundle.Programs)
	if *team != "" {
		var err error
		if lines, err = teamLayout(*team, *dir); err != nil {
			return err
		}
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// teamLayout writes each helper's entitlements into dir and returns the
// layout lines with their absolute paths.
func teamLayout(team, dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(appbundle.Programs))
	for i, line := range appbundle.LayoutLines(appbundle.Programs) {
		p := appbundle.Programs[i]
		if p.Place != appbundle.PlaceHelper {
			lines = append(lines, line)
			continue
		}
		path := filepath.Join(abs, p.Name+".entitlements")
		if err := os.WriteFile(path, appbundle.HelperEntitlements(team, p.Roles), 0o600); err != nil {
			return nil, err
		}
		lines = append(lines, strings.Join([]string{string(p.Source), string(p.Place), p.Name, path}, " "))
	}
	return lines, nil
}

// profileCommand checks that the app Xcode signed for -team has a
// profile of that team, before the script puts the bundle together
// (S12-03-host-prerequisites, "Missing profile"). It prints the team's
// name.
func profileCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	app := fs.String("app", "", "the app Xcode built")
	team := fs.String("team", "", "the team the build signs with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *app == "" || *team == "" || fs.NArg() != 0 {
		return errUsage
	}
	p, err := checkProfile(ctx, filepath.Join(*app, "Contents", appbundle.ProfileName), *team, codesign.ReadProfile)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "profile: the team %q (%s) signed the app's %s\n", p.TeamName, *team, appbundle.ProfileName)
	return nil
}

// checkProfile reads the profile at path with read and fails, naming the
// one-time setup and WB_TEAM_ID, unless it is a regular file of exactly
// team.
func checkProfile(ctx context.Context, path, team string, read func(context.Context, string) (codesign.Profile, error)) (codesign.Profile, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return codesign.Profile{}, appbundle.MissingProfile(team, path+" doesn't exist")
	}
	if err != nil {
		return codesign.Profile{}, appbundle.MissingProfile(team, err.Error())
	}
	if !info.Mode().IsRegular() {
		return codesign.Profile{}, appbundle.MissingProfile(team, path+" is not a regular file")
	}
	p, err := read(ctx, path)
	if err != nil {
		return codesign.Profile{}, appbundle.MissingProfile(team, err.Error())
	}
	if !slices.Equal(p.TeamIdentifier, []string{team}) {
		return codesign.Profile{}, appbundle.MissingProfile(team, fmt.Sprintf("its TeamIdentifier is %q", p.TeamIdentifier))
	}
	return p, nil
}

// signIdentityCommand prints the SHA-1 hash of the one Apple Development
// identity of -team in the user's keychains, for codesign --sign
// (S17-01-bundle, "Signing identity").
func signIdentityCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sign-identity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	team := fs.String("team", "", "the team the build signs with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *team == "" || fs.NArg() != 0 {
		return errUsage
	}
	list, err := exec.CommandContext(ctx, securityPath, "find-identity", "-v", "-p", "codesigning").Output()
	if err != nil {
		return fmt.Errorf("security find-identity: %w", err)
	}
	certs, err := exec.CommandContext(ctx, securityPath, "find-certificate", "-a", "-p").Output()
	if err != nil {
		return fmt.Errorf("security find-certificate: %w", err)
	}
	id, err := appbundle.ChooseIdentity(appbundle.ParseIdentities(string(list), certs), *team)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "sign-identity: %s\n", id)
	fmt.Fprintln(stdout, id.Hash)
	return nil
}
