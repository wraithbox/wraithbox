package appbundle

import (
	"slices"
	"strings"
	"testing"
)

const (
	testStamp = "0.1.0+g0123456789abcdef"
	testTeam  = "ABCDE12345"
	otherTeam = "B2B2B2B2B2"
)

// goodBundle is what the inspection reads from a bundle built as it
// should be, ad-hoc when team is "", else signed by team.
func goodBundle(team string) Bundle {
	b := Bundle{Plists: map[string]*Plist{}, ProfileTeams: map[string][]string{}, ToolsVersion: testStamp}
	if team != "" {
		b.Profiles = append(b.Profiles, "Contents/"+ProfileName)
		b.ProfileTeams["Contents/"+ProfileName] = []string{team}
	}
	for _, p := range Programs {
		f := Found{Path: p.Path(), Signed: true, Runtime: true, Team: team, Entitlements: map[string]any{}, Stamp: testStamp}
		if p.Source == SourceGo {
			f.GoMain = testModule + "/cmd/" + p.Name
		}
		if p.Virtualization {
			f.Entitlements[VirtualizationEntitlement] = true
		}
		if team != "" && p.Place == PlaceHelper {
			f.Entitlements[groupsKey] = anyList(TeamGroups(team, p.Roles)...)
			b.Profiles = append(b.Profiles, HelperDir(p.Name)+"/Contents/"+ProfileName)
			b.ProfileTeams[HelperDir(p.Name)+"/Contents/"+ProfileName] = []string{team}
		}
		switch p.Place {
		case PlaceToolsDisk:
			b.DiskPrograms = append(b.DiskPrograms, f)
			continue
		case PlaceApp:
			b.Plists[InfoPlistPath] = &Plist{XML: true, Keys: map[string]any{
				keyIdentifier: AdhocBundleID, keyExecutable: p.Name, keyUIElement: true, BuildVersionKey: testStamp,
				keyMinSystem: "27.0",
			}}
		case PlaceHelper:
			b.Plists[HelperDir(p.Name)+"/"+InfoPlistPath] = &Plist{XML: true, Keys: map[string]any{
				keyIdentifier: AdhocBundleID, keyExecutable: p.Name, keyPackageType: "APPL", keyUIElement: true,
			}}
		case PlaceMacOS:
		}
		b.Programs = append(b.Programs, f)
	}
	return b
}

// testModule is the Go module the bundle's Go programs come from.
const testModule = "github.com/wraithbox/wraithbox/packages/wraithbox-go"

func testMode(team string) Mode {
	return Mode{Stamp: testStamp, Team: team, BundleID: AdhocBundleID, MinMacOS: 27}
}

func anyList(items ...string) []any {
	out := make([]any, 0, len(items))
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

// program returns the Found of the program called name, in the bundle
// or on the disk, for a test to change.
func program(t *testing.T, b *Bundle, name string) *Found {
	t.Helper()
	for _, p := range Programs {
		if p.Name != name {
			continue
		}
		for _, list := range []*[]Found{&b.Programs, &b.DiskPrograms} {
			for i := range *list {
				if (*list)[i].Path == p.Path() {
					return &(*list)[i]
				}
			}
		}
	}
	t.Fatalf("no program %s in the bundle", name)
	return nil
}

func TestC05BuildCheckGoodLayoutsPass(t *testing.T) {
	for _, team := range []string{"", testTeam} {
		if got := Check(goodBundle(team), Programs, testMode(team)); len(got) != 0 {
			t.Errorf("team %q: the good bundle fails: %v", team, got)
		}
	}
}

// TestC05BuildCheck feeds the check one violation each, and sees it fail
// with a message that names the program and the rule.
func TestC05BuildCheck(t *testing.T) {
	cases := []struct {
		name    string
		team    string
		change  func(t *testing.T, b *Bundle)
		program string
		rule    Rule
	}{
		{"a helper with a group more than its row", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-credset").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"binding", "ca"})...)
		}, "wb-credset", RuleTeamHelperGroups},
		{"a helper with a group fewer than its row", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-proxyd").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"binding"})...)
		}, "wb-proxyd", RuleTeamHelperGroups},
		{"a helper with its row's group twice", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestadmin").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"wbadmin", "wbadmin"})...)
		}, "wb-guestadmin", RuleTeamHelperGroups},
		{"a helper with groups that aren't a list of strings", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-credset").Entitlements[groupsKey] = testTeam + ".nl.wraithbox.binding"
		}, "wb-credset", RuleTeamHelperGroups},
		{"a team helper with another team's prefix", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-credset").Entitlements[groupsKey] = anyList(TeamGroups(otherTeam, []string{"binding"})...)
		}, "wb-credset", RuleTeamHelperGroups},
		{"wb with a group in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"binding"})...)
		}, "wb", RuleTeamOtherGroup},
		{"the app executable with a group in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-notify").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"ca"})...)
		}, "wb-notify", RuleTeamOtherGroup},
		{"the app as Xcode signed it, had step 7 not signed it again", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-notify").Entitlements = map[string]any{
				groupsKey:                           anyList(testTeam + ".nl.wraithbox.xcode-profile"),
				applicationIDKey:                    testTeam + "." + AdhocBundleID,
				teamIDKey:                           testTeam,
				"com.apple.security.get-task-allow": true,
			}
		}, "wb-notify", RuleTeamOtherGroup},
		{"a helper profile of another team", testTeam, func(_ *testing.T, b *Bundle) {
			b.ProfileTeams[HelperDir("wb-credset")+"/Contents/"+ProfileName] = []string{otherTeam}
		}, HelperDir("wb-credset") + "/Contents/" + ProfileName, RuleProfileTeam},
		{"a helper profile of two teams", testTeam, func(_ *testing.T, b *Bundle) {
			b.ProfileTeams[HelperDir("wb-proxyd")+"/Contents/"+ProfileName] = []string{testTeam, otherTeam}
		}, HelperDir("wb-proxyd") + "/Contents/" + ProfileName, RuleProfileTeam},
		{"a helper profile that couldn't be decoded", testTeam, func(_ *testing.T, b *Bundle) {
			delete(b.ProfileTeams, HelperDir("wb-guestadmin")+"/Contents/"+ProfileName)
		}, HelperDir("wb-guestadmin") + "/Contents/" + ProfileName, RuleProfileTeam},
		{"the app's profile of another team", testTeam, func(_ *testing.T, b *Bundle) {
			b.ProfileTeams["Contents/"+ProfileName] = []string{otherTeam}
		}, "Contents/" + ProfileName, RuleProfileTeam},
		{"wb with a group in an ad-hoc build", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb").Entitlements[groupsKey] = anyList(TeamGroups(testTeam, []string{"binding"})...)
		}, "wb", RuleAdhocGroup},
		{"an ad-hoc helper with any group", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-proxyd").Entitlements[groupsKey] = anyList()
		}, "wb-proxyd", RuleAdhocGroup},
		{"a helper bundle with a profile in an ad-hoc build", "", func(_ *testing.T, b *Bundle) {
			b.Profiles = append(b.Profiles, HelperDir("wb-proxyd")+"/Contents/"+ProfileName)
		}, HelperDir("wb-proxyd") + "/Contents/" + ProfileName, RuleAdhocProfile},
		{"a team helper bundle without a profile", testTeam, func(_ *testing.T, b *Bundle) {
			b.Profiles = slices.DeleteFunc(b.Profiles, func(path string) bool {
				return path == HelperDir("wb-proxyd")+"/Contents/"+ProfileName
			})
		}, "wb-proxyd", RuleTeamProfile},
		{"a second program with the virtualization entitlement", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-hostd").Entitlements[VirtualizationEntitlement] = true
		}, "wb-hostd", RuleVirtualization},
		{"a guest program with the virtualization entitlement, false", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestd").Entitlements[VirtualizationEntitlement] = false
		}, "wb-guestd", RuleVirtualization},
		{"wb-vmd without the virtualization entitlement", "", func(t *testing.T, b *Bundle) {
			delete(program(t, b, "wb-vmd").Entitlements, VirtualizationEntitlement)
		}, "wb-vmd", RuleVMDHasVirtualization},
		{"wb-vmd with the virtualization entitlement false", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-vmd").Entitlements[VirtualizationEntitlement] = false
		}, "wb-vmd", RuleVMDHasVirtualization},
		{"an ad-hoc program with another entitlement", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Entitlements["com.apple.security.cs.disable-library-validation"] = true
		}, "wb-netd", RuleAdhocEntitlement},
		{"wb with only an application group in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb").Entitlements[applicationGroupsKey] = anyList(testTeam + ".nl.wraithbox.binding")
		}, "wb", RuleTeamOtherGroup},
		{"wb with an application identifier in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb").Entitlements[applicationIDKey] = testTeam + ".nl.wraithbox.dev.abcde12345"
		}, "wb", RuleTeamOtherGroup},
		{"wb-hostd with an application identifier in an ad-hoc build", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-hostd").Entitlements[applicationIDKey] = testTeam + ".nl.wraithbox.dev.abcde12345"
		}, "wb-hostd", RuleAdhocGroup},
		{"an ad-hoc program with an application group", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Entitlements[applicationGroupsKey] = anyList("group.nl.wraithbox")
		}, "wb-netd", RuleAdhocGroup},
		{"a helper with an application identifier on top of its row", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-credset").Entitlements[applicationIDKey] = testTeam + ".nl.wraithbox.dev.abcde12345"
		}, "wb-credset", RuleTeamHelperGroups},
		{"a helper with an application group on top of its row", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestadmin").Entitlements[applicationGroupsKey] = anyList(testTeam + ".nl.wraithbox.binding")
		}, "wb-guestadmin", RuleTeamHelperGroups},
		{"a helper with an application identifier that isn't a string", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-proxyd").Entitlements[applicationIDKey] = true
		}, "wb-proxyd", RuleTeamHelperGroups},
		{"a team program with get-task-allow", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-hostd").Entitlements["com.apple.security.get-task-allow"] = true
		}, "wb-hostd", RuleTeamEntitlement},
		{"a team helper with disable-library-validation", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-credset").Entitlements["com.apple.security.cs.disable-library-validation"] = true
		}, "wb-credset", RuleTeamEntitlement},
		{"a team program with allow-dyld-environment-variables", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb").Entitlements["com.apple.security.cs.allow-dyld-environment-variables"] = true
		}, "wb", RuleTeamEntitlement},
		{"a team wb-vmd with com.apple.vm.networking", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-vmd").Entitlements["com.apple.vm.networking"] = true
		}, "wb-vmd", RuleTeamEntitlement},
		{"a team guest program with get-task-allow", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestd").Entitlements["com.apple.security.get-task-allow"] = true
		}, "wb-guestd", RuleTeamEntitlement},
		{"a team program with an application group", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Entitlements[applicationGroupsKey] = anyList()
		}, "wb-netd", RuleTeamOtherGroup},
		{"a team program with another team's identifier", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Entitlements[teamIDKey] = otherTeam
		}, "wb-netd", RuleTeamEntitlement},
		{"an ad-hoc program with a team identifier", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Entitlements[teamIDKey] = testTeam
		}, "wb-netd", RuleAdhocEntitlement},
		{"a program another team signed", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-proxyd").Team = otherTeam
		}, "wb-proxyd", RuleSignatureTeam},
		{"an ad-hoc program in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-diskfmt").Team = ""
		}, "wb-diskfmt", RuleSignatureTeam},
		{"a guest program ad-hoc in a team build", testTeam, func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestd").Team = ""
		}, "wb-guestd", RuleSignatureTeam},
		{"a team-signed program in an ad-hoc build", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-notify").Team = testTeam
		}, "wb-notify", RuleSignatureTeam},
		{"a Mach-O file not in the table", "", func(_ *testing.T, b *Bundle) {
			b.Programs = append(b.Programs, Found{Path: "Contents/MacOS/wb-dev-app", Signed: true, Runtime: true, Stamp: testStamp})
		}, "Contents/MacOS/wb-dev-app", RuleUnknownMachO},
		{"a Mach-O file on the tools disk not in the table", "", func(_ *testing.T, b *Bundle) {
			b.DiskPrograms = append(b.DiskPrograms, Found{Path: "wb", Signed: true, Runtime: true, Stamp: testStamp})
		}, DiskPrefix + "wb", RuleUnknownMachO},
		{"a program of the table in another place", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-proxyd").Path = "Contents/MacOS/wb-proxyd"
		}, "wb-proxyd", RuleMissing},
		{"a symbolic link", "", func(_ *testing.T, b *Bundle) {
			b.Links = append(b.Links, "Contents/MacOS/wb-link")
		}, "Contents/MacOS/wb-link", RuleLink},
		{"a program without the stamp", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-netd").Stamp = ""
		}, "wb-netd", RuleStamp},
		{"a guest program of another build", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestd").Stamp = "0.1.0+g1111111111111111"
		}, "wb-guestd", RuleStamp},
		{"a Go program from another main package", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb").GoMain = testModule + "/cmd/wb-dev-app"
		}, "wb", RuleSource},
		{"a Go program from no Go package", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-guestd").GoMain = ""
		}, "wb-guestd", RuleSource},
		{"a Go program in the place of a Swift one", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-vmd").GoMain = testModule + "/cmd/wb-vmd"
		}, "wb-vmd", RuleSource},
		{"an app with another bundle identifier", "", func(_ *testing.T, b *Bundle) {
			b.Plists[InfoPlistPath].Keys[keyIdentifier] = "nl.example.other"
		}, "wb-notify", RuleInfoPlist},
		{"an unsigned program", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-hostd").Signed = false
		}, "wb-hostd", RuleSigned},
		{"a program without the hardened runtime", "", func(t *testing.T, b *Bundle) {
			program(t, b, "wb-vmd").Runtime = false
		}, "wb-vmd", RuleSigned},
		{"a tools disk VERSION that differs", "", func(_ *testing.T, b *Bundle) {
			b.ToolsVersion = testStamp + "\n"
		}, ToolsVersionFile, RuleToolsVersion},
		{"a WBBuildVersion that differs", "", func(_ *testing.T, b *Bundle) {
			b.Plists[InfoPlistPath].Keys[BuildVersionKey] = "0.1.0+g1111111111111111"
		}, "wb-notify", RuleBuildVersion},
		{"an app that opens on an older macOS", "", func(_ *testing.T, b *Bundle) {
			b.Plists[InfoPlistPath].Keys[keyMinSystem] = "26.0"
		}, "wb-notify", RuleMinMacOS},
		{"an app without LSMinimumSystemVersion", "", func(_ *testing.T, b *Bundle) {
			delete(b.Plists[InfoPlistPath].Keys, keyMinSystem)
		}, "wb-notify", RuleMinMacOS},
		{"an Info.plist that isn't XML", "", func(_ *testing.T, b *Bundle) {
			b.Plists[InfoPlistPath].XML = false
		}, "wb-notify", RuleInfoPlist},
		{"an app Info.plist that names another executable", "", func(_ *testing.T, b *Bundle) {
			b.Plists[InfoPlistPath].Keys[keyExecutable] = "wb"
		}, "wb-notify", RuleInfoPlist},
		{"a helper without LSUIElement", "", func(_ *testing.T, b *Bundle) {
			delete(b.Plists[HelperDir("wb-credset")+"/"+InfoPlistPath].Keys, keyUIElement)
		}, "wb-credset", RuleInfoPlist},
		{"a helper with another bundle identifier", "", func(_ *testing.T, b *Bundle) {
			b.Plists[HelperDir("wb-guestadmin")+"/"+InfoPlistPath].Keys[keyIdentifier] = "nl.example.other"
		}, "wb-guestadmin", RuleInfoPlist},
		{"a helper without an Info.plist", "", func(_ *testing.T, b *Bundle) {
			delete(b.Plists, HelperDir("wb-proxyd")+"/"+InfoPlistPath)
		}, "wb-proxyd", RuleInfoPlist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := goodBundle(tc.team)
			tc.change(t, &b)
			got := Check(b, Programs, testMode(tc.team))
			if !slices.Contains(Rules, tc.rule) {
				t.Errorf("%s is not in Rules", tc.rule.ID)
			}
			if !hasViolation(got, tc.program, tc.rule) {
				t.Fatalf("want a violation of %s by %s, got %v", tc.rule.ID, tc.program, got)
			}
		})
	}
}

// hasViolation reports whether got holds a violation by program of rule,
// whose message names both.
func hasViolation(got []Violation, program string, rule Rule) bool {
	for _, v := range got {
		msg := v.String()
		if v.Program == program && v.Rule == rule && strings.Contains(msg, program) && strings.Contains(msg, rule.ID) {
			return true
		}
	}
	return false
}

func TestC05BuildCheckMode(t *testing.T) {
	for _, m := range []Mode{
		{Stamp: "", BundleID: AdhocBundleID, MinMacOS: 27},
		{Stamp: "unstamped", BundleID: AdhocBundleID, MinMacOS: 27},
		{Stamp: testStamp, Team: "abcde12345", BundleID: AdhocBundleID, MinMacOS: 27},
		{Stamp: testStamp, MinMacOS: 27},
		{Stamp: testStamp, BundleID: "wraithbox", MinMacOS: 27},
		{Stamp: testStamp, BundleID: AdhocBundleID},
	} {
		b := goodBundle(m.Team)
		if got := Check(b, Programs, m); !hasViolation(got, "build", RuleMode) {
			t.Errorf("mode %+v: want a %s violation, got %v", m, RuleMode.ID, got)
		}
	}
}

// TestC05BuildCheckEmptyStamp: an empty stamp in the mode fails the
// check even when every program, like it, has none.
func TestC05BuildCheckEmptyStamp(t *testing.T) {
	b := goodBundle("")
	for i := range b.Programs {
		b.Programs[i].Stamp = ""
	}
	b.ToolsVersion = ""
	b.Plists[InfoPlistPath].Keys[BuildVersionKey] = ""
	if got := Check(b, Programs, Mode{}); !hasViolation(got, "build", RuleMode) {
		t.Errorf("an empty stamp passes: %v", got)
	}
}

func TestLayoutTable(t *testing.T) {
	names := map[string]bool{}
	virtualization := 0
	for _, p := range Programs {
		if names[p.Name] {
			t.Errorf("%s is in the table twice", p.Name)
		}
		names[p.Name] = true
		if p.Virtualization {
			virtualization++
		}
		if (p.Place == PlaceHelper) != (len(p.Roles) > 0) {
			t.Errorf("%s: only a helper has roles, and every helper has one", p.Name)
		}
	}
	if virtualization != 1 {
		t.Errorf("%d programs have the virtualization entitlement, want 1 (wb-vmd)", virtualization)
	}
	if len(LayoutLines(Programs)) != len(Programs) {
		t.Errorf("LayoutLines has a line count other than the table's")
	}
}
