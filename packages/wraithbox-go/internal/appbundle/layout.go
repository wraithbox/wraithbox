// Package appbundle holds what the macOS bundle build needs to know in
// Go: the layout of `Wraith Box.app`, the bundle identifier of a build,
// the entitlement check of the built bundle, and the swap that installs
// it (S17-01-bundle, S10-02-packaging-and-signing).
//
// The package doesn't run codesign or plutil. cmd/wb-dev-app does that,
// and gives Check what it read, so the check's rules are tested on
// parsed entitlement sets with no signed binaries.
package appbundle

import "strings"

// AppName is the bundle's directory name (S17-01-bundle, "The bundle").
const AppName = "Wraith Box.app"

// Paths inside the bundle, relative to the bundle's directory, with
// slashes.
const (
	// InfoPlistPath is the app's Info.plist.
	InfoPlistPath = "Contents/Info.plist"
	// ToolsDiskPath is the guest tools disk (S06-12-guest-agent, "Guest
	// tools disk").
	ToolsDiskPath = "Contents/Resources/wb-guest-tools.img"
	// ProfileName is the file name of a provisioning profile in a bundle.
	ProfileName = "embedded.provisionprofile"
	// ToolsVersionFile is the file at the root of the tools disk that
	// holds the build version.
	ToolsVersionFile = "VERSION"
)

// BuildVersionKey is the Info.plist key that holds the build version
// (S17-01-bundle, "The bundle"). wb-hostd reads it to notice an upgrade.
const BuildVersionKey = "WBBuildVersion"

// VirtualizationEntitlement is the one entitlement wb-vmd has, and no
// other program (SEC12-least-privilege; S10-02-packaging-and-signing,
// "macOS").
const VirtualizationEntitlement = "com.apple.security.virtualization"

// VirtualizationEntitlementsFile is the entitlements file the build signs
// a program of Virtualization with, relative to the repository root
// (S15-01-rights, "Hardening and entitlements").
const VirtualizationEntitlementsFile = "packages/wraithbox-swift/wb-vmd.entitlements"

// Place says where in the bundle a program goes.
type Place string

const (
	// PlaceApp is the app's main executable, Contents/MacOS/<name>.
	PlaceApp Place = "app"
	// PlaceMacOS is a plain program in Contents/MacOS/<name>.
	PlaceMacOS Place = "macos"
	// PlaceHelper is the main executable of a helper bundle,
	// Contents/Helpers/<name>.app/Contents/MacOS/<name>
	// (S10-02-packaging-and-signing, "Helper bundles").
	PlaceHelper Place = "helper"
	// PlaceToolsDisk is a program at the root of the guest tools disk.
	PlaceToolsDisk Place = "disk"
)

// Source says what builds a program.
type Source string

const (
	// SourceGo is `go build ./cmd/<name>` for darwin/arm64.
	SourceGo Source = "go"
	// SourceSwiftPM is the Swift package's product <name>, built with
	// `swift build -c release`.
	SourceSwiftPM Source = "swiftpm"
	// SourceXcode is the app target of WraithBox.xcodeproj.
	SourceXcode Source = "xcode"
)

// Program is one row of the bundle's layout.
type Program struct {
	Name   string
	Source Source
	Place  Place
	// Roles are the Keychain roles of the helper's access groups in a
	// build that a team signs (S15-02-secret-store, "Keychain"). An
	// ad-hoc build gives no program a group.
	Roles []string
	// Virtualization is whether the program has
	// VirtualizationEntitlement. Only wb-vmd does.
	Virtualization bool
}

// Path is where the program is: relative to the bundle's directory, or
// to the root of the tools disk for PlaceToolsDisk.
func (p Program) Path() string {
	switch p.Place {
	case PlaceHelper:
		return HelperDir(p.Name) + "/Contents/MacOS/" + p.Name
	case PlaceToolsDisk:
		return p.Name
	case PlaceApp, PlaceMacOS:
		return "Contents/MacOS/" + p.Name
	}
	return "Contents/MacOS/" + p.Name
}

// HelperDir is the directory of a helper bundle, relative to the bundle's
// directory.
func HelperDir(name string) string {
	return "Contents/Helpers/" + name + ".app"
}

// Programs is the layout of the bundle: every program in it, and every
// program on the guest tools disk. A program is in the bundle only when
// it is in this table. The build doesn't take every command under cmd/,
// so dev tools such as wb-dev-app never ship, and the check refuses a
// Mach-O file that isn't here.
//
// Each program S10-02-packaging-and-signing lists that doesn't exist yet
// joins this table in the change that writes it. The helpers and their
// roles are the rows of S15-02-secret-store, "Keychain".
var Programs = []Program{
	{Name: "wb-notify", Source: SourceXcode, Place: PlaceApp},
	{Name: "wb", Source: SourceGo, Place: PlaceMacOS},
	{Name: "wb-hostd", Source: SourceGo, Place: PlaceMacOS},
	{Name: "wb-netd", Source: SourceGo, Place: PlaceMacOS},
	{Name: "wb-diskfmt", Source: SourceGo, Place: PlaceMacOS},
	{Name: "wb-vmd", Source: SourceSwiftPM, Place: PlaceMacOS, Virtualization: true},
	{Name: "wb-proxyd", Source: SourceGo, Place: PlaceHelper, Roles: []string{"binding", "ca"}},
	{Name: "wb-credset", Source: SourceGo, Place: PlaceHelper, Roles: []string{"binding"}},
	{Name: "wb-guestadmin", Source: SourceGo, Place: PlaceHelper, Roles: []string{"wbadmin"}},
	{Name: "wb-guestd", Source: SourceGo, Place: PlaceToolsDisk},
}

// LayoutLines formats the table for the build script, one program per
// line: source, place, name, and the entitlements file relative to the
// repository root or `-` for none.
func LayoutLines(programs []Program) []string {
	lines := make([]string, 0, len(programs))
	for _, p := range programs {
		entitlements := "-"
		if p.Virtualization {
			entitlements = VirtualizationEntitlementsFile
		}
		lines = append(lines, strings.Join([]string{string(p.Source), string(p.Place), p.Name, entitlements}, " "))
	}
	return lines
}
