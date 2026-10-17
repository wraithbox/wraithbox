package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// bundleFix is the fix of a refusal of the bundle check.
const bundleFix = `Put "` + appbundle.AppName + `" in ~/Applications or /Applications, and run the wb inside it: "<Applications>/` + appbundle.AppName + `/Contents/MacOS/wb" setup.`

// bundleOf returns the bundle directory of path when path is
// <bundle>/Contents/MacOS/wb.
func bundleOf(path string) (string, bool) {
	macos := filepath.Dir(path)
	contents := filepath.Dir(macos)
	if filepath.Base(path) != "wb" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" {
		return "", false
	}
	return filepath.Dir(contents), true
}

// checkBundle returns the real path of the bundle wb runs from, after
// the bundle check of S17-02-setup, "The bundle check":
//
//   - wb's real path is <bundle>/Contents/MacOS/wb, and the bundle's
//     Info.plist has a WBBuildVersion equal to wb's stamped version, as
//     bytes. An unstamped wb never matches. Otherwise RuleNotInBundle.
//   - The bundle has Contents/MacOS/wb-hostd, the program the agent
//     runs. Otherwise RuleNotInBundle.
//   - The bundle's parent is ~/Applications or /Applications, compared
//     by real path. Otherwise RuleBundleLocation, so the agent never
//     points into a build directory that a cleanup deletes.
func checkBundle(ctx context.Context, env *Env) (string, error) {
	notIn := func(format string, a ...any) error {
		return refuse(ctx, env.Log, &Refusal{Rule: RuleNotInBundle, Msg: fmt.Sprintf(format, a...), Fix: bundleFix})
	}
	exe, err := filepath.EvalSymlinks(env.Executable)
	if err != nil {
		return "", notIn("wb's own path %q can't be resolved: %v", env.Executable, err)
	}
	bundle, ok := bundleOf(exe)
	if !ok {
		return "", notIn("wb runs from %q, which is not Contents/MacOS/wb of a bundle", exe)
	}
	if env.Version == "" {
		return "", notIn("this wb is unstamped, so it has no build version to match its bundle's (S04-08-records-and-bounds, \"Version skew\")")
	}
	plist := filepath.Join(bundle, filepath.FromSlash(appbundle.InfoPlistPath))
	got, err := appbundle.ReadBuildVersion(plist)
	if err != nil {
		return "", notIn("the bundle %q has no build version: %v", bundle, err)
	}
	if got != env.Version {
		return "", notIn("the bundle %q has %s %s, and this wb is build %s", bundle, appbundle.BuildVersionKey, version.Label(got), version.Label(env.Version))
	}
	hostd := filepath.Join(bundle, "Contents", "MacOS", "wb-hostd")
	if fi, err := os.Lstat(hostd); err != nil || !fi.Mode().IsRegular() {
		return "", notIn("the bundle %q has no program Contents/MacOS/wb-hostd", bundle)
	}
	decide(ctx, env.Log, RuleNotInBundle, "accept", fmt.Sprintf("bundle %q, build %s", bundle, version.Label(got)))

	if !inApplications(env, filepath.Dir(bundle)) {
		return "", refuse(ctx, env.Log, &Refusal{
			Rule: RuleBundleLocation,
			Msg:  fmt.Sprintf("the bundle %q is not directly in ~/Applications or /Applications, so the agent could point into a directory that a cleanup deletes", bundle),
			Fix:  bundleFix,
		})
	}
	decide(ctx, env.Log, RuleBundleLocation, "accept", fmt.Sprintf("bundle %q", bundle))
	return bundle, nil
}

// inApplications reports whether dir, a real path, is the real path of
// ~/Applications or /Applications. A folder that doesn't exist matches
// nothing.
func inApplications(env *Env, dir string) bool {
	for _, apps := range []string{filepath.Join(env.Home, "Applications"), env.SystemApplications} {
		resolved, err := filepath.EvalSymlinks(apps)
		if err == nil && resolved == dir {
			return true
		}
	}
	return false
}
