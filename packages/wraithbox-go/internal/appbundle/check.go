package appbundle

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// Rule is one rule of the entitlement check (S17-01-bundle, "The
// entitlement check"). Spec names the requirement it enforces, for the
// log.
type Rule struct {
	ID   string
	Spec string
}

// The check's rules. Each Violation names one.
var (
	RuleMode                 = Rule{"check-mode", `S04-08-records-and-bounds, "Version skew"`}
	RuleUnknownMachO         = Rule{"layout-unknown", `S17-01-bundle, "The entitlement check"`}
	RuleMissing              = Rule{"layout-missing", `S17-01-bundle, "The entitlement check"`}
	RuleLink                 = Rule{"layout-link", `S17-01-bundle, "The entitlement check"`}
	RuleSigned               = Rule{"signature-runtime", `SEC12-least-privilege; S10-02-packaging-and-signing, "macOS"`}
	RuleSignatureTeam        = Rule{"signature-team", `S17-01-bundle, "Signing identity"`}
	RuleAdhocGroup           = Rule{"keychain-adhoc", `SEC04-no-guest-secrets; S10-02-packaging-and-signing, "Signing"`}
	RuleTeamHelperGroups     = Rule{"keychain-helper-row", `S15-02-secret-store, "Keychain"; S10-02-packaging-and-signing, "Helper bundles"`}
	RuleTeamOtherGroup       = Rule{"keychain-other", `SEC04-no-guest-secrets; S10-02-packaging-and-signing, "Helper bundles"`}
	RuleAdhocEntitlement     = Rule{"entitlement-adhoc", `SEC12-least-privilege; S17-01-bundle, "The entitlement check"`}
	RuleTeamEntitlement      = Rule{"entitlement-team", `SEC12-least-privilege; S17-01-bundle, "The entitlement check"`}
	RuleVirtualization       = Rule{"virtualization-only-vmd", `SEC12-least-privilege; S10-02-packaging-and-signing, "macOS"`}
	RuleVMDHasVirtualization = Rule{"virtualization-vmd", `S10-02-packaging-and-signing, "macOS"`}
	RuleAdhocProfile         = Rule{"profile-adhoc", `S10-02-packaging-and-signing, "Helper bundles"`}
	RuleTeamProfile          = Rule{"profile-helper", `S10-02-packaging-and-signing, "Helper bundles"`}
	RuleProfileTeam          = Rule{"profile-team", `S17-01-bundle, "The entitlement check"`}
	RuleInfoPlist            = Rule{"info-plist", `S17-01-bundle, "The bundle"; S10-02-packaging-and-signing, "Helper bundles"`}
	RuleStamp                = Rule{"stamp-program", `S04-08-records-and-bounds, "Version skew"`}
	RuleBuildVersion         = Rule{"stamp-info-plist", `S04-08-records-and-bounds, "Version skew"`}
	RuleToolsVersion         = Rule{"stamp-tools-disk", `S04-08-records-and-bounds, "Version skew"`}
	RuleVerify               = Rule{"codesign-verify", `S17-01-bundle, "Building a bundle"`}
	RuleSource               = Rule{"program-source", `S17-01-bundle, "The entitlement check"`}
	RuleMinMacOS             = Rule{"min-macos", `NFR04-host-platforms; S17-01-bundle, "The entitlement check"`}
)

// Rules are every rule of the check, for the log of a bundle that passes.
var Rules = []Rule{
	RuleMode, RuleUnknownMachO, RuleMissing, RuleLink, RuleSigned, RuleSignatureTeam,
	RuleAdhocGroup, RuleTeamHelperGroups, RuleTeamOtherGroup, RuleAdhocEntitlement, RuleTeamEntitlement,
	RuleVirtualization, RuleVMDHasVirtualization, RuleAdhocProfile, RuleTeamProfile, RuleProfileTeam,
	RuleInfoPlist, RuleStamp, RuleBuildVersion, RuleToolsVersion, RuleVerify,
	RuleSource, RuleMinMacOS,
}

// Violation is one way the bundle breaks a rule.
type Violation struct {
	// Program is the program's name, or the path of the file the
	// violation is about when it isn't a program in the table.
	Program string
	Rule    Rule
	Detail  string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s [rule %s: %s]", v.Program, v.Detail, v.Rule.ID, v.Rule.Spec)
}

// Mode is what the bundle was built as.
type Mode struct {
	// Stamp is the run's build version, from build/version.
	Stamp string
	// Team is the team ID that signed the bundle, or "" for an ad-hoc
	// build.
	Team string
	// BundleID is the bundle identifier the build chose (BundleID).
	BundleID string
	// MinMacOS is the oldest macOS major version a host runs, which the
	// app's LSMinimumSystemVersion names. wb-dev-app passes
	// setup.MinMacOS.
	MinMacOS int
}

// Found is what was read from one Mach-O file.
type Found struct {
	// Path is relative to the bundle's directory, or to the root of the
	// tools disk, with slashes.
	Path string
	// Signed is whether codesign reads a signature, and Runtime whether
	// it has the hardened runtime flag.
	Signed  bool
	Runtime bool
	// Team is the team of the signature, or "" for one without a team,
	// such as an ad-hoc signature.
	Team string
	// Entitlements are the signed entitlements, as plutil converts them
	// to JSON. Empty or nil when there are none.
	Entitlements map[string]any
	// Stamp is the build version linked into the program, or "" when it
	// has none, and GoMain the main package of a Go program, or "" for
	// another (ReadLinked).
	Stamp  string
	GoMain string
}

// Plist is what was read from one Info.plist.
type Plist struct {
	// XML is whether the file is an XML property list, which wb setup and
	// wb-hostd read with a plain XML reader.
	XML  bool
	Keys map[string]any
}

// Bundle is what was read from a built bundle and its tools disk.
type Bundle struct {
	// Programs are the Mach-O files under the bundle's directory, and
	// DiskPrograms those on the tools disk.
	Programs     []Found
	DiskPrograms []Found
	// Profiles are the paths of every ProfileName file in the bundle.
	Profiles []string
	// ProfileTeams is the TeamIdentifier list of each profile that could
	// be decoded, by its path. A profile missing here couldn't be.
	ProfileTeams map[string][]string
	// Links are the paths of every symbolic link in the bundle or on the
	// disk. The walk doesn't follow them, so the check refuses them.
	Links []string
	// Plists are the Info.plist files of the app and of each helper
	// bundle, by path; nil for one that is missing.
	Plists map[string]*Plist
	// ToolsVersion is the content of the tools disk's VERSION file.
	ToolsVersion string
}

// Check returns every way b breaks the rules for programs and m, sorted
// by program and rule. No violation means the bundle passes. The caller
// runs `codesign --verify --deep --strict` on the app as well (RuleVerify).
func Check(b Bundle, programs []Program, m Mode) []Violation {
	var out []Violation
	if !version.Valid(m.Stamp) {
		out = append(out, Violation{"build", RuleMode, "the stamp " + version.Label(m.Stamp) + " is not a build version"})
	}
	if m.Team != "" && !ValidTeamID(m.Team) {
		out = append(out, Violation{"build", RuleMode, fmt.Sprintf("the team %q is not a team ID", m.Team)})
	}
	if !ValidBundleID(m.BundleID) {
		out = append(out, Violation{"build", RuleMode, fmt.Sprintf("the bundle identifier %q is not reverse-DNS", m.BundleID)})
	}
	if m.MinMacOS <= 0 {
		out = append(out, Violation{"build", RuleMode, fmt.Sprintf("the minimum macOS %d is not a major version", m.MinMacOS)})
	}
	app, disk := split(programs)
	out = append(out, checkPrograms(b.Programs, app, m, "")...)
	out = append(out, checkPrograms(b.DiskPrograms, disk, m, DiskPrefix)...)
	for _, link := range b.Links {
		out = append(out, Violation{link, RuleLink, "is a symbolic link, which the check doesn't follow"})
	}
	out = append(out, checkProfiles(b, app, m)...)
	out = append(out, checkPlists(b.Plists, app, m)...)
	if b.ToolsVersion != m.Stamp {
		out = append(out, Violation{
			ToolsVersionFile, RuleToolsVersion,
			fmt.Sprintf("the tools disk's version is %s, not the stamp %s", version.Label(b.ToolsVersion), version.Label(m.Stamp)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Program != out[j].Program {
			return out[i].Program < out[j].Program
		}
		return out[i].Rule.ID < out[j].Rule.ID
	})
	return out
}

// split returns the rows in the bundle and the rows on the tools disk.
func split(programs []Program) (app, disk []Program) {
	for _, p := range programs {
		if p.Place == PlaceToolsDisk {
			disk = append(disk, p)
		} else {
			app = append(app, p)
		}
	}
	return app, disk
}

// DiskPrefix marks a path on the tools disk in the check's messages.
const DiskPrefix = "tools-disk:"

// checkPrograms matches the Mach-O files found against the rows by path,
// and checks each program found. prefix goes in front of the path of a
// file that isn't in the table.
func checkPrograms(found []Found, rows []Program, m Mode, prefix string) []Violation {
	var out []Violation
	byPath := make(map[string]Program, len(rows))
	for _, p := range rows {
		byPath[p.Path()] = p
	}
	seen := make(map[string]bool, len(found))
	for _, f := range found {
		p, ok := byPath[f.Path]
		if !ok {
			out = append(out, Violation{prefix + f.Path, RuleUnknownMachO, "is a Mach-O file that isn't in the bundle's layout table"})
			continue
		}
		seen[f.Path] = true
		out = append(out, checkProgram(p, f, m)...)
	}
	for _, p := range rows {
		if !seen[p.Path()] {
			out = append(out, Violation{p.Name, RuleMissing, "is missing from " + p.Path()})
		}
	}
	return out
}

// checkProgram checks one program's source, signature, entitlements and
// stamp.
func checkProgram(p Program, f Found, m Mode) []Violation {
	var out []Violation
	if !fromSource(p, f.GoMain) {
		out = append(out, Violation{p.Name, RuleSource, fmt.Sprintf("is built from the Go package %q, and its row says %s ./cmd/%s", f.GoMain, p.Source, p.Name)})
	}
	if !f.Signed || !f.Runtime {
		out = append(out, Violation{p.Name, RuleSigned, "is not signed with the hardened runtime"})
	}
	if f.Signed && f.Team != m.Team {
		out = append(out, Violation{p.Name, RuleSignatureTeam, fmt.Sprintf("is signed by the team %q, and the build's team is %q", f.Team, m.Team)})
	}
	out = append(out, checkGroups(p, f.Entitlements, m)...)
	out = append(out, checkVirtualization(p, f.Entitlements)...)
	out = append(out, checkAllowed(p, f.Entitlements, m)...)
	if f.Stamp != m.Stamp {
		out = append(out, Violation{
			p.Name, RuleStamp,
			fmt.Sprintf("carries the version %s, not the stamp %s", version.Label(f.Stamp), version.Label(m.Stamp)),
		})
	}
	return out
}

// fromSource reports whether a program was built from its row's source:
// a Go row from the main package ./cmd/<name>, any other row from no Go
// package. So another program put at a row's path, with the right stamp,
// still fails.
func fromSource(p Program, goMain string) bool {
	if p.Source == SourceGo {
		return strings.HasSuffix(goMain, "/cmd/"+p.Name)
	}
	return goMain == ""
}

// The entitlements that name Keychain access groups. Apple takes a
// program's access groups from all three: keychain-access-groups, the
// application identifier, and the application groups. So the group
// rules look at each, and wb with only an application group still has a
// group (SEC04-no-guest-secrets; S15-02-secret-store, "`wb` doesn't hold
// a group").
const (
	groupsKey            = "keychain-access-groups"
	applicationIDKey     = "com.apple.application-identifier"
	applicationGroupsKey = "com.apple.security.application-groups"
)

// groupKeys are the entitlements that name access groups.
var groupKeys = []string{groupsKey, applicationIDKey, applicationGroupsKey}

// teamIDKey is the entitlement that names the signing team.
const teamIDKey = "com.apple.developer.team-identifier"

// checkGroups applies the Keychain rules: no program has a group in an
// ad-hoc build; in a team build each helper has exactly its row's groups
// under the team's prefix (S15-02-secret-store, "Keychain"), and every
// other program, wb and the app included, has none. Each entitlement in
// groupKeys counts.
func checkGroups(p Program, entitlements map[string]any, m Mode) []Violation {
	present := presentKeys(entitlements, groupKeys)
	switch {
	case m.Team == "" && len(present) > 0:
		return []Violation{{p.Name, RuleAdhocGroup, fmt.Sprintf("has %s in an ad-hoc build", describe(entitlements, present))}}
	case m.Team == "":
		return nil
	case p.Place != PlaceHelper && len(present) > 0:
		return []Violation{{p.Name, RuleTeamOtherGroup, fmt.Sprintf("has %s, and only a helper bundle holds a group", describe(entitlements, present))}}
	case p.Place != PlaceHelper:
		return nil
	}
	want := TeamGroups(m.Team, p.Roles)
	got, ok := groupsOf(entitlements, present)
	if !ok || !sameSet(got, want) {
		return []Violation{{p.Name, RuleTeamHelperGroups, fmt.Sprintf("has %s, and its row is %s %v", describe(entitlements, present), groupsKey, want)}}
	}
	return nil
}

// presentKeys returns the keys of entitlements that are in keys, in the
// order of keys.
func presentKeys(entitlements map[string]any, keys []string) []string {
	var out []string
	for _, k := range keys {
		if _, has := entitlements[k]; has {
			out = append(out, k)
		}
	}
	return out
}

// describe names each of keys with its value, for a message.
func describe(entitlements map[string]any, keys []string) string {
	if len(keys) == 0 {
		return "no group"
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %v", k, entitlements[k]))
	}
	return strings.Join(parts, " and ")
}

// groupsOf returns every access group the keys name: the lists of
// keychain-access-groups and the application groups, and the application
// identifier. A value of another form is not ok, so the check fails
// closed on it.
func groupsOf(entitlements map[string]any, keys []string) ([]string, bool) {
	var groups []string
	for _, k := range keys {
		if k == applicationIDKey {
			id, ok := entitlements[k].(string)
			if !ok {
				return nil, false
			}
			groups = append(groups, id)
			continue
		}
		list, ok := stringList(entitlements[k])
		if !ok {
			return nil, false
		}
		groups = append(groups, list...)
	}
	return groups, true
}

// TeamGroups are the access groups of roles in a build that team signs:
// `<TEAMID>.nl.wraithbox.<role>` (S15-02-secret-store, "Keychain").
func TeamGroups(team string, roles []string) []string {
	groups := make([]string, 0, len(roles))
	for _, role := range roles {
		groups = append(groups, team+".nl.wraithbox."+role)
	}
	return groups
}

// stringList returns raw as a list of strings, when it is one.
func stringList(raw any) ([]string, bool) {
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// sameSet reports whether got holds each string of want once, and no
// other.
func sameSet(got, want []string) bool {
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		return false
	}
	return slices.Equal(got, want)
}

// checkVirtualization applies SEC12-least-privilege's rule for the
// virtualization entitlement: wb-vmd has it, as true, and no other
// program has it at all.
func checkVirtualization(p Program, entitlements map[string]any) []Violation {
	value, has := entitlements[VirtualizationEntitlement]
	switch {
	case p.Virtualization && value != true:
		return []Violation{{p.Name, RuleVMDHasVirtualization, "doesn't have " + VirtualizationEntitlement}}
	case !p.Virtualization && has:
		return []Violation{{p.Name, RuleVirtualization, "has " + VirtualizationEntitlement + ", which only wb-vmd has"}}
	}
	return nil
}

// The entitlements each mode allows. Every other entitlement fails the
// check (S17-01-bundle, "The entitlement check"), such as get-task-allow,
// disable-library-validation, allow-dyld-environment-variables, and
// com.apple.vm.networking, which S15-01-rights, "Hardening and
// entitlements", gives no process. The group and virtualization
// entitlements have rules of their own on top, so an allowed key held by
// the wrong program still fails.
var (
	adhocAllowed = []string{groupsKey, applicationIDKey, applicationGroupsKey, VirtualizationEntitlement}
	teamAllowed  = []string{groupsKey, applicationIDKey, teamIDKey, VirtualizationEntitlement}
)

// checkAllowed refuses every entitlement outside the mode's allowlist,
// and in a team build a team identifier entitlement of another team
// (SEC12-least-privilege).
func checkAllowed(p Program, entitlements map[string]any, m Mode) []Violation {
	allowed, rule, build := adhocAllowed, RuleAdhocEntitlement, "an ad-hoc build"
	if m.Team != "" {
		allowed, rule, build = teamAllowed, RuleTeamEntitlement, "a build of the team "+m.Team
	}
	var out []Violation
	for _, key := range sortedKeys(entitlements) {
		if !slices.Contains(allowed, key) {
			out = append(out, Violation{p.Name, rule, "has the entitlement " + key + " in " + build})
		}
	}
	if team, has := entitlements[teamIDKey]; has && m.Team != "" && team != m.Team {
		out = append(out, Violation{p.Name, rule, fmt.Sprintf("has %s %v in %s", teamIDKey, team, build)})
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// checkProfiles refuses every profile in an ad-hoc build. In a team
// build it refuses every helper bundle without one, and every profile
// that couldn't be decoded or whose TeamIdentifier isn't exactly the
// build's team (S10-02-packaging-and-signing, "Helper bundles").
func checkProfiles(b Bundle, rows []Program, m Mode) []Violation {
	var out []Violation
	if m.Team == "" {
		for _, path := range b.Profiles {
			out = append(out, Violation{path, RuleAdhocProfile, "is a provisioning profile in an ad-hoc build"})
		}
		return out
	}
	for _, p := range rows {
		if p.Place == PlaceHelper && !slices.Contains(b.Profiles, HelperDir(p.Name)+"/Contents/"+ProfileName) {
			out = append(out, Violation{p.Name, RuleTeamProfile, "has no provisioning profile in its helper bundle"})
		}
	}
	for _, path := range b.Profiles {
		teams, ok := b.ProfileTeams[path]
		switch {
		case !ok:
			out = append(out, Violation{path, RuleProfileTeam, "couldn't be decoded"})
		case !slices.Equal(teams, []string{m.Team}):
			out = append(out, Violation{path, RuleProfileTeam, fmt.Sprintf("names the teams %q, not only %s", teams, m.Team)})
		}
	}
	return out
}
