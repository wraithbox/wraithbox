package setup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
)

// mixedFix is the fix of RuleKeychainTeamMixed.
const mixedFix = "Build Wraith Box again with " + appbundle.TeamEnv + " set to your team ID, install it with `mise run app:install`, and run wb setup again."

// checkKeychain reads the team of each helper bundle's code signature,
// and decides the Keychain backend (S17-02-setup, "The Keychain
// backend"). wb setup runs it after it installed the agent and the
// link, and still exits nonzero on a refusal (S05-03-management-commands,
// "`wb setup`").
//
//   - No helper has a team signature: the build is ad-hoc, and has no
//     Keychain backend. RuleKeychainNeedsTeam refuses it with the
//     one-time setup (S15-02-secret-store, "Ad-hoc signed builds").
//   - The helpers don't agree on one team, a signature can't be read,
//     or a helper's profile is missing, unreadable or of another team:
//     RuleKeychainTeamMixed refuses it (S12-03-host-prerequisites,
//     "Team-signed build").
//   - Otherwise it logs the accept under RuleKeychainTeam, and prints
//     the team from the profile and that the team's groups are team-wide (S15-02-secret-store, "Team-wide
//     groups in development builds").
func checkKeychain(ctx context.Context, env *Env, bundle string) error {
	teams, problems := helperTeams(ctx, env, bundle)
	if len(problems) == 0 && len(teams) == 1 && teams[""] != nil {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleKeychainNeedsTeam,
			Msg: fmt.Sprintf("this build is ad-hoc signed: no team signed the helper bundles of %s, so it has no Keychain backend, and Wraith Box can't keep credentials",
				strings.Join(teams[""], ", ")),
			Fix: appbundle.TeamSetupSteps,
		})
	}
	if len(teams) > 1 {
		problems = append(problems, "the helpers aren't signed by one team: "+describeTeams(teams))
	}
	var profile codesign.Profile
	if len(problems) == 0 {
		// One team, and not "": the ad-hoc case returned above.
		profile, problems = helperProfiles(ctx, env, bundle, slices.Collect(maps.Keys(teams))[0])
	}
	if len(problems) > 0 {
		return refuse(ctx, env.Log, &Refusal{
			Rule: RuleKeychainTeamMixed,
			Msg:  "the helper bundles don't agree on one team, so the Keychain backend is refused: " + strings.Join(problems, "; "),
			Fix:  mixedFix,
		})
	}
	team := profile.TeamIdentifier[0]
	decide(ctx, env.Log, RuleKeychainTeam, "accept", fmt.Sprintf("the team %s (%s) signed every helper bundle and its profile", team, profile.TeamName))
	fmt.Fprintf(env.Stdout, "wb setup: this build is signed by the team %q (%s). Every program that team signs can reach Wraith Box's Keychain groups (%s.nl.wraithbox.*), not only Wraith Box's helpers (S15-02-secret-store, \"Team-wide groups in development builds\").\n",
		profile.TeamName, team, team)
	return nil
}

// helperNames are the programs of the layout with a helper bundle.
func helperNames() []string {
	var names []string
	for _, p := range appbundle.Programs {
		if p.Place == appbundle.PlaceHelper {
			names = append(names, p.Name)
		}
	}
	return names
}

func helperDir(bundle, name string) string {
	return filepath.Join(bundle, filepath.FromSlash(appbundle.HelperDir(name)))
}

// helperTeams reads the team of each helper bundle's signature, and
// returns the helpers by team, "" for a signature without one. A
// signature it can't read is a problem.
func helperTeams(ctx context.Context, env *Env, bundle string) (map[string][]string, []string) {
	teams := map[string][]string{}
	var problems []string
	for _, name := range helperNames() {
		team, err := readTeam(ctx, env, helperDir(bundle, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("the signature of %s can't be read: %v", name, err))
			continue
		}
		teams[team] = append(teams[team], name)
	}
	return teams, problems
}

func readTeam(ctx context.Context, env *Env, path string) (string, error) {
	if env.ReadTeam == nil {
		return "", errors.New("wb setup has no code signature reader")
	}
	return env.ReadTeam(ctx, path)
}

// describeTeams names each team and its helpers, in a fixed order.
func describeTeams(teams map[string][]string) string {
	parts := make([]string, 0, len(teams))
	for _, team := range slices.Sorted(maps.Keys(teams)) {
		label := "the team " + team
		if team == "" {
			label = "no team"
		}
		parts = append(parts, fmt.Sprintf("%s by %s", strings.Join(teams[team], ", "), label))
	}
	return strings.Join(parts, ", ")
}

// helperProfiles reads the profile of each helper bundle, and returns
// the last one read. A profile that is missing, isn't a regular file,
// can't be read, names another team than exactly team, names no team
// name, or another team name than the others is a problem.
func helperProfiles(ctx context.Context, env *Env, bundle, team string) (codesign.Profile, []string) {
	var last codesign.Profile
	var problems []string
	for _, name := range helperNames() {
		p, err := readProfile(ctx, env, filepath.Join(helperDir(bundle, name), "Contents", appbundle.ProfileName))
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("the profile of %s: %v", name, err))
		case !slices.Equal(p.TeamIdentifier, []string{team}):
			problems = append(problems, fmt.Sprintf("the profile of %s is of the team %q, and its signature of %s", name, p.TeamIdentifier, team))
		case p.TeamName == "":
			problems = append(problems, fmt.Sprintf("the profile of %s names no team name", name))
		case last.TeamName != "" && p.TeamName != last.TeamName:
			problems = append(problems, fmt.Sprintf("the profile of %s names the team %q, and another helper's %q", name, p.TeamName, last.TeamName))
		default:
			last = p
		}
	}
	return last, problems
}

// readProfile reads the profile at path. One that isn't a regular file,
// a link included, is an error.
func readProfile(ctx context.Context, env *Env, path string) (codesign.Profile, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return codesign.Profile{}, err
	}
	if !fi.Mode().IsRegular() {
		return codesign.Profile{}, fmt.Errorf("%s is not a regular file", path)
	}
	if env.ReadProfile == nil {
		return codesign.Profile{}, errors.New("wb setup has no profile reader")
	}
	return env.ReadProfile(ctx, path)
}
