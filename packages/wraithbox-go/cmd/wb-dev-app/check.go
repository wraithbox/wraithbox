package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
)

// The tools check and install run, by absolute path, so a PATH entry
// can't stand in for them.
const (
	codesignPath = "/usr/bin/codesign"
	plutilPath   = "/usr/bin/plutil"
)

// checkCommand is the entitlement check of S17-01-bundle, "Building a
// bundle", step 8: it reads every Mach-O file in the bundle at -app and
// on the tools disk mounted at -disk, checks them against the layout
// table (appbundle.Check), and runs `codesign --verify --deep --strict`
// on the app. It prints each violation with its rule, or the rules the
// bundle passed, and fails on any violation.
func checkCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	app := fs.String("app", "", "the built bundle")
	disk := fs.String("disk", "", "where the bundle's tools disk is mounted")
	stampFile := fs.String("stamp", "", "the file that holds the run's build version")
	team := fs.String("team", "", "the team that signed the bundle; empty for an ad-hoc build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *app == "" || *disk == "" || *stampFile == "" || fs.NArg() != 0 {
		return errUsage
	}
	stamp, err := readStamp(*stampFile)
	if err != nil {
		return err
	}
	b, err := inspect(ctx, *app, *disk)
	if err != nil {
		return err
	}
	mode := appbundle.Mode{Stamp: stamp, Team: *team}
	violations := appbundle.Check(b, appbundle.Programs, mode)
	if out, err := run1(ctx, codesignPath, "--verify", "--deep", "--strict", *app); err != nil {
		violations = append(violations, appbundle.Violation{
			Program: appbundle.AppName, Rule: appbundle.RuleVerify,
			Detail: fmt.Sprintf("codesign --verify --deep --strict fails: %v: %s", err, strings.TrimSpace(out)),
		})
	}
	return report(stdout, b, mode, violations)
}

// report prints the check's decision: each violation, or the rules the
// bundle passed.
func report(stdout io.Writer, b appbundle.Bundle, m appbundle.Mode, violations []appbundle.Violation) error {
	build := "ad-hoc"
	if m.Team != "" {
		build = "team " + m.Team
	}
	for _, v := range violations {
		fmt.Fprintf(stdout, "check: FAIL %s\n", v)
	}
	if len(violations) > 0 {
		return fmt.Errorf("the bundle (%s, %s) fails the entitlement check, violations: %d", build, m.Stamp, len(violations))
	}
	fmt.Fprintf(stdout, "check: pass (%s, %s): %d programs in the bundle and %d on the tools disk, rules %s\n",
		build, m.Stamp, len(b.Programs), len(b.DiskPrograms), ruleIDs())
	return nil
}

func ruleIDs() string {
	ids := make([]string, 0, len(appbundle.Rules))
	for _, r := range appbundle.Rules {
		ids = append(ids, r.ID)
	}
	return strings.Join(ids, ", ")
}

// inspect reads what the check needs from the bundle at app and the tools
// disk mounted at disk.
func inspect(ctx context.Context, app, disk string) (appbundle.Bundle, error) {
	var b appbundle.Bundle
	files, err := appbundle.Walk(app)
	if err != nil {
		return b, fmt.Errorf("walking the bundle: %w", err)
	}
	diskFiles, err := appbundle.Walk(disk)
	if err != nil {
		return b, fmt.Errorf("walking the tools disk: %w", err)
	}
	if b.Programs, err = readPrograms(ctx, app, files.MachO); err != nil {
		return b, err
	}
	if b.DiskPrograms, err = readPrograms(ctx, disk, diskFiles.MachO); err != nil {
		return b, err
	}
	b.Profiles = files.Profiles
	b.Links = files.Links
	for _, link := range diskFiles.Links {
		b.Links = append(b.Links, appbundle.DiskPrefix+link)
	}
	if b.Plists, err = readPlists(ctx, app); err != nil {
		return b, err
	}
	version, err := os.ReadFile(filepath.Join(disk, appbundle.ToolsVersionFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	b.ToolsVersion = string(version)
	return b, nil
}

// readPrograms reads the signature, entitlements and stamp of each
// Mach-O file, by its path relative to root.
func readPrograms(ctx context.Context, root string, paths []string) ([]appbundle.Found, error) {
	found := make([]appbundle.Found, 0, len(paths))
	for _, rel := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		f := appbundle.Found{Path: rel}
		f.Signed, f.Runtime = readFlags(ctx, path)
		var err error
		// An unsigned file has no entitlements, and the check refuses it
		// for being unsigned.
		if f.Signed {
			if f.Entitlements, err = readEntitlements(ctx, path); err != nil {
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
		}
		if f.Stamp, err = appbundle.ReadStamp(path); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		found = append(found, f)
	}
	return found, nil
}

// flagsLine is the code directory flags in `codesign -d --verbose=1`.
var flagsLine = regexp.MustCompile(`(?m)^CodeDirectory .*\bflags=0x[0-9a-f]+\(([^)]*)\)`)

// readFlags reports whether codesign reads a signature, and whether its
// flags include the hardened runtime.
func readFlags(ctx context.Context, path string) (signed, runtime bool) {
	out, err := run1(ctx, codesignPath, "-d", "--verbose=1", path)
	if err != nil {
		return false, false
	}
	return true, hasRuntimeFlag(out)
}

// hasRuntimeFlag reports whether the code directory flags in the output
// of `codesign -d --verbose=1` include the hardened runtime.
func hasRuntimeFlag(out string) bool {
	m := flagsLine.FindStringSubmatch(out)
	return m != nil && slices.Contains(strings.Split(m[1], ","), "runtime")
}

// readEntitlements returns the signed entitlements of a signed file, as
// plutil converts them to JSON. codesign prints nothing for a signature
// without entitlements: an empty map.
func readEntitlements(ctx context.Context, path string) (map[string]any, error) {
	cmd := exec.CommandContext(ctx, codesignPath, "-d", "--entitlements", "-", "--xml", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codesign -d --entitlements: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return map[string]any{}, nil
	}
	return plistJSON(ctx, &stdout)
}

// readPlists reads the Info.plist of the app and of each helper bundle.
// A missing one is nil in the map, and the check refuses it.
func readPlists(ctx context.Context, app string) (map[string]*appbundle.Plist, error) {
	plists := make(map[string]*appbundle.Plist)
	for _, rel := range appbundle.PlistPaths(appbundle.Programs) {
		path := filepath.Join(app, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		keys, err := plistJSON(ctx, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		plists[rel] = &appbundle.Plist{XML: bytes.HasPrefix(data, []byte("<?xml")), Keys: keys}
	}
	return plists, nil
}

// plistJSON converts the property list read from in to JSON with plutil,
// and decodes it.
func plistJSON(ctx context.Context, in io.Reader) (map[string]any, error) {
	cmd := exec.CommandContext(ctx, plutilPath, "-convert", "json", "-o", "-", "--", "-")
	cmd.Stdin = in
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plutil -convert json: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var keys map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &keys); err != nil {
		return nil, fmt.Errorf("decoding plutil's JSON: %w", err)
	}
	return keys, nil
}

// run1 runs a tool and returns its combined output.
func run1(ctx context.Context, tool string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	return string(out), err
}
