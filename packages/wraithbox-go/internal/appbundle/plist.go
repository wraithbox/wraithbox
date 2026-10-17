package appbundle

import (
	"fmt"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// The Info.plist keys the check reads.
const (
	keyIdentifier  = "CFBundleIdentifier"
	keyExecutable  = "CFBundleExecutable"
	keyPackageType = "CFBundlePackageType"
	keyUIElement   = "LSUIElement"
)

// PlistPaths are the Info.plist files the check reads: the app's and each
// helper bundle's, relative to the bundle's directory.
func PlistPaths(programs []Program) []string {
	paths := []string{InfoPlistPath}
	for _, p := range programs {
		if p.Place == PlaceHelper {
			paths = append(paths, HelperDir(p.Name)+"/"+InfoPlistPath)
		}
	}
	return paths
}

// checkPlists checks the app's Info.plist (S17-01-bundle, "The bundle")
// and each helper bundle's (S10-02-packaging-and-signing, "Helper
// bundles"): XML, the executable each names, the app's WBBuildVersion,
// and that each helper has the app's bundle identifier and LSUIElement.
func checkPlists(plists map[string]*Plist, rows []Program, m Mode) []Violation {
	var out []Violation
	appID := ""
	for _, p := range rows {
		var (
			path string
			want map[string]any
		)
		switch p.Place {
		case PlaceApp:
			path = InfoPlistPath
			want = map[string]any{keyExecutable: p.Name, keyUIElement: true}
		case PlaceHelper:
			path = HelperDir(p.Name) + "/" + InfoPlistPath
			want = map[string]any{keyExecutable: p.Name, keyPackageType: "APPL", keyUIElement: true}
		case PlaceMacOS, PlaceToolsDisk:
			continue
		}
		plist := plists[path]
		if plist == nil {
			out = append(out, Violation{p.Name, RuleInfoPlist, path + " is missing"})
			continue
		}
		out = append(out, checkPlistKeys(p.Name, path, plist, want)...)
		if p.Place == PlaceApp {
			appID, _ = plist.Keys[keyIdentifier].(string)
			out = append(out, checkAppPlist(p.Name, plist, m)...)
		}
	}
	return append(out, checkHelperIDs(plists, rows, appID)...)
}

// checkPlistKeys checks that a plist is XML and holds the wanted values.
func checkPlistKeys(name, path string, plist *Plist, want map[string]any) []Violation {
	var out []Violation
	if !plist.XML {
		out = append(out, Violation{name, RuleInfoPlist, path + " is not an XML property list"})
	}
	for _, key := range sortedKeys(want) {
		if got := plist.Keys[key]; got != want[key] {
			out = append(out, Violation{name, RuleInfoPlist, fmt.Sprintf("%s has %s %v, not %v", path, key, got, want[key])})
		}
	}
	return out
}

// checkAppPlist checks the app's bundle identifier and WBBuildVersion.
func checkAppPlist(name string, plist *Plist, m Mode) []Violation {
	var out []Violation
	if id, _ := plist.Keys[keyIdentifier].(string); !ValidBundleID(id) {
		out = append(out, Violation{name, RuleInfoPlist, fmt.Sprintf("%s has %s %q, not a bundle identifier", InfoPlistPath, keyIdentifier, id)})
	}
	if got, _ := plist.Keys[BuildVersionKey].(string); got != m.Stamp {
		out = append(out, Violation{
			name, RuleBuildVersion,
			fmt.Sprintf("%s has %s %s, not the stamp %s", InfoPlistPath, BuildVersionKey, version.Label(got), version.Label(m.Stamp)),
		})
	}
	return out
}

// checkHelperIDs checks that each helper bundle has the app's bundle
// identifier.
func checkHelperIDs(plists map[string]*Plist, rows []Program, appID string) []Violation {
	var out []Violation
	for _, p := range rows {
		plist := plists[HelperDir(p.Name)+"/"+InfoPlistPath]
		if p.Place != PlaceHelper || plist == nil {
			continue
		}
		if id, _ := plist.Keys[keyIdentifier].(string); id == "" || id != appID {
			out = append(out, Violation{p.Name, RuleInfoPlist, fmt.Sprintf("has the bundle identifier %q, not the app's %q", id, appID)})
		}
	}
	return out
}
