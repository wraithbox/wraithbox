package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
)

// keychainFix names the one-time setup of S12-03-host-prerequisites,
// "Development builds on macOS", and WB_TEAM_ID.
const keychainFix = `To sign the build with your own team, once per Mac:
  1. Sign in to Xcode with an Apple ID (Xcode, Settings, Accounts). Xcode lists its team as a personal team.
  2. Build a small app project once in the Xcode app, with that team and "My Mac" as the destination.
Then set ` + appbundle.TeamEnv + ` to the team ID, the organizational unit (OU) in the Apple Development certificate's subject, and build Wraith Box again.`

// checkKeychain refuses the Keychain backend with RuleKeychainNeedsTeam
// when any helper bundle has no Contents/embedded.provisionprofile: then
// no team signed the build, and it has no Keychain backend
// (S10-02-packaging-and-signing, "Signing"; S15-02-secret-store,
// "Ad-hoc signed builds"). The profile is the signal until I280 reads
// the team from the code signature (S17-02-setup, "The Keychain
// backend"). wb setup runs it after it installed the
// agent and the link, and still exits nonzero on it (S05-03-management-commands,
// "`wb setup`"). A profile that isn't a regular file counts as missing.
func checkKeychain(ctx context.Context, env *Env, bundle string) error {
	var missing []string
	for _, p := range appbundle.Programs {
		if p.Place != appbundle.PlaceHelper {
			continue
		}
		profile := filepath.Join(bundle, filepath.FromSlash(appbundle.HelperDir(p.Name)), "Contents", appbundle.ProfileName)
		if fi, err := os.Lstat(profile); err != nil || !fi.Mode().IsRegular() {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) == 0 {
		decide(ctx, env.Log, RuleKeychainNeedsTeam, "accept", "every helper bundle has a provisioning profile")
		return nil
	}
	return refuse(ctx, env.Log, &Refusal{
		Rule: RuleKeychainNeedsTeam,
		Msg: fmt.Sprintf("this build is ad-hoc signed: the helper bundles of %s have no %s, so it has no Keychain backend, and Wraith Box can't keep credentials",
			strings.Join(missing, ", "), appbundle.ProfileName),
		Fix: keychainFix,
	})
}
