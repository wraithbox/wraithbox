//go:build unix

package setup

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// wantInstalled checks that wb setup installed the agent and the link
// before the Keychain check.
func (f *fixture) wantInstalled() {
	f.t.Helper()
	if want := []string{"state " + f.hostdProgram(), "replace " + f.hostdProgram()}; !slices.Equal(f.svc.calls, want) {
		f.t.Errorf("service calls %q, want %q", f.svc.calls, want)
	}
	f.wantLink()
}

// wantLogged checks that the log holds the decision with the rule.
func (f *fixture) wantLogged(rule Rule, decision string) {
	f.t.Helper()
	if !strings.Contains(f.log.String(), "rule="+string(rule)) || !strings.Contains(f.log.String(), "decision="+decision) {
		f.t.Errorf("log = %q, want rule %s and decision %s", f.log.String(), rule, decision)
	}
}

// makeAdhoc gives every helper an ad-hoc signature, and removes its
// profile unless keepProfiles.
func (f *fixture) makeAdhoc(keepProfiles bool) {
	f.t.Helper()
	for helper := range f.teams {
		f.teams[helper] = ""
		if keepProfiles {
			continue
		}
		if err := os.Remove(f.profilePath(helper)); err != nil {
			f.t.Fatal(err)
		}
	}
}

// TestC05SetupAdhocKeychain sees an ad-hoc bundle, whose helper bundles
// no team signed, refused the Keychain backend with keychain-needs-team,
// with the Xcode steps and WB_TEAM_ID named, after the agent and the
// link are installed, and a nonzero exit (C05-credentials-and-store,
// "Builds without a team, and development builds"). A profile file
// doesn't make a bundle team-signed: wb setup reads the signature.
func TestC05SetupAdhocKeychain(t *testing.T) {
	for name, profiles := range map[string]bool{
		"no profiles":                     false,
		"profiles, and ad-hoc signatures": true,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.makeAdhoc(profiles)
			if code := f.run(); code != ExitRefused {
				t.Errorf("exit %d, want %d", code, ExitRefused)
			}
			f.wantStderr("refused (keychain-needs-team)", "Sign in to Xcode with an Apple ID", `"My Mac"`, "WB_TEAM_ID", "wb-credset")
			if strings.Contains(f.stderr.String(), "1Password") {
				t.Errorf("stderr = %q, mentions 1Password", f.stderr.String())
			}
			f.wantLogged(RuleKeychainNeedsTeam, "refuse")
			f.wantInstalled()
		})
	}
}

// TestC05SetupTeamReport: a bundle whose helpers one team signed, each
// with that team's profile, passes, and wb setup prints the team's name
// and ID and that its groups are team-wide (S17-02-setup, "The Keychain
// backend").
func TestC05SetupTeamReport(t *testing.T) {
	f := newFixture(t)
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	for _, want := range []string{`the team "Test & Team" (ABCDE12345)`, "ABCDE12345.nl.wraithbox.*", "Team-wide groups in development builds"} {
		if !strings.Contains(f.stdout.String(), want) {
			t.Errorf("stdout = %q, want %q", f.stdout.String(), want)
		}
	}
	f.wantLogged(RuleKeychainTeamMixed, "accept")
}

// TestC05SetupTeamMixed feeds wb setup bundles whose helpers don't agree
// on one team, and sees each refused with keychain-team-mixed after the
// install, naming the helper (S12-03-host-prerequisites, "Team-signed
// build").
func TestC05SetupTeamMixed(t *testing.T) {
	cases := []struct {
		name   string
		change func(f *fixture)
		says   string
	}{
		{"a helper another team signed", func(f *fixture) { f.teams["wb-proxyd"] = "B2B2B2B2B2" }, "wb-proxyd by the team B2B2B2B2B2"},
		{"a helper ad-hoc among team helpers", func(f *fixture) { f.teams["wb-credset"] = "" }, "wb-credset by no team"},
		{"a helper whose signature can't be read", func(f *fixture) { delete(f.teams, "wb-guestadmin") }, "the signature of wb-guestadmin can't be read"},
		{"a helper without a profile", func(f *fixture) {
			if err := os.Remove(f.profilePath("wb-credset")); err != nil {
				f.t.Fatal(err)
			}
		}, "the profile of wb-credset"},
		{"a profile that is a link", func(f *fixture) {
			path := f.profilePath("wb-proxyd")
			if err := os.Remove(path); err != nil {
				f.t.Fatal(err)
			}
			other := f.root + "/profile"
			f.write(other, testProfile(testTeam, testTeamName))
			f.symlink(other, path)
		}, "is not a regular file"},
		{"a profile of another team", func(f *fixture) {
			f.write(f.profilePath("wb-guestadmin"), testProfile("B2B2B2B2B2", testTeamName))
		}, "the profile of wb-guestadmin is of the team"},
		{"a profile of two teams", func(f *fixture) {
			f.write(f.profilePath("wb-guestadmin"), strings.Replace(testProfile(testTeam, testTeamName),
				"<string>"+testTeam+"</string>", "<string>"+testTeam+"</string><string>B2B2B2B2B2</string>", 1))
		}, "the profile of wb-guestadmin is of the team"},
		{"a profile with another team name", func(f *fixture) {
			f.write(f.profilePath("wb-guestadmin"), testProfile(testTeam, "Other"))
		}, `names the team "Other"`},
		{"a profile that isn't one", func(f *fixture) { f.write(f.profilePath("wb-credset"), "profile") }, "the profile of wb-credset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(f)
			if code := f.run(); code != ExitRefused {
				t.Errorf("exit %d, want %d", code, ExitRefused)
			}
			f.wantStderr("refused (keychain-team-mixed)", tc.says, "WB_TEAM_ID")
			if strings.Contains(f.stdout.String(), "Keychain groups") {
				t.Errorf("stdout = %q, reports a team", f.stdout.String())
			}
			f.wantLogged(RuleKeychainTeamMixed, "refuse")
			f.wantInstalled()
		})
	}
}

// TestSetupNoSignatureReader refuses the Keychain backend when the Env
// has no reader, rather than taking the build as team-signed.
func TestSetupNoSignatureReader(t *testing.T) {
	f := newFixture(t)
	f.env.ReadTeam = nil
	if code := f.run(); code != ExitRefused || !strings.Contains(f.stderr.String(), "keychain-team-mixed") {
		t.Errorf("exit %d, stderr %q; want keychain-team-mixed", code, f.stderr.String())
	}
	f = newFixture(t)
	f.env.ReadProfile = nil
	if code := f.run(); code != ExitRefused || !strings.Contains(f.stderr.String(), "no profile reader") {
		t.Errorf("exit %d, stderr %q; want no profile reader", code, f.stderr.String())
	}
}
