package appbundle

import (
	"fmt"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
)

// AdhocBundleID is the bundle identifier of an ad-hoc build when
// WB_BUNDLE_ID isn't set (S17-01-bundle, "Signing identity").
const AdhocBundleID = "nl.wraithbox.dev.adhoc"

// The settings S17-01-bundle, "Signing identity", reads.
const (
	TeamEnv     = "WB_TEAM_ID"
	BundleIDEnv = "WB_BUNDLE_ID"
)

// identityRule names the requirement in BundleID's errors.
const identityRule = `S17-01-bundle, "Signing identity"`

// DevBundleIDPrefix is the start of the bundle identifier of a build a
// team signs when WB_BUNDLE_ID isn't set: the team ID in lowercase
// follows it (S10-02-packaging-and-signing, "Signing").
const DevBundleIDPrefix = "nl.wraithbox.dev."

// maxBundleIDLen is the longest bundle identifier BundleID accepts.
const maxBundleIDLen = 155

// BundleID returns the bundle identifier of a build from the values of
// WB_TEAM_ID and WB_BUNDLE_ID (S17-01-bundle, "Signing identity"):
// WB_BUNDLE_ID when it is set and valid (ValidBundleID), else
// DevBundleIDPrefix and the team in lowercase, or AdhocBundleID without
// a team. A team that isn't ten uppercase letters and digits
// (ValidTeamID) is refused, so the build stops before it runs anything
// with it.
func BundleID(team, bundleID string) (string, error) {
	if team != "" && !ValidTeamID(team) {
		return "", fmt.Errorf("%s=%q is not an Apple team ID: ten uppercase letters and digits, the organizational unit (OU) in the subject of your Apple Development certificate (%s)",
			TeamEnv, team, identityRule)
	}
	if bundleID != "" {
		if !ValidBundleID(bundleID) {
			return "", fmt.Errorf("%s=%q is not a reverse-DNS bundle identifier: two or more labels of letters, digits and hyphens, joined by dots, at most %d bytes (%s)",
				BundleIDEnv, bundleID, maxBundleIDLen, identityRule)
		}
		return bundleID, nil
	}
	if team == "" {
		return AdhocBundleID, nil
	}
	return DevBundleIDPrefix + strings.ToLower(team), nil
}

// ValidBundleID reports whether s is a reverse-DNS bundle identifier:
// two or more labels joined by dots, each one or more ASCII letters,
// digits and hyphens, and no longer than maxBundleIDLen bytes. Those are
// the bytes Apple allows in CFBundleIdentifier.
func ValidBundleID(s string) bool {
	if len(s) > maxBundleIDLen {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || strings.IndexFunc(label, notBundleIDRune) >= 0 {
			return false
		}
	}
	return true
}

func notBundleIDRune(r rune) bool {
	return !isUpperOrDigit(r) && (r < 'a' || r > 'z') && r != '-'
}

func isUpperOrDigit(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// ValidTeamID reports whether s is an Apple team identifier: ten
// uppercase ASCII letters and digits. codesign.ParseTeam holds a team
// read from a signature to the same form.
func ValidTeamID(s string) bool {
	return codesign.ValidTeamID(s)
}
