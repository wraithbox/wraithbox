package appbundle

import (
	"errors"
	"fmt"
	"strings"
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

// ErrTeamBuild is BundleID's error when WB_TEAM_ID is set. The
// team-signed build is M2.2c (I280). Until it is built, a build with a
// team fails before it builds anything, so nobody gets a bundle that is
// half team-signed.
var ErrTeamBuild = errors.New(TeamEnv + " is set, and the team-signed build isn't built yet (#280); unset " + TeamEnv + " for an ad-hoc build")

// maxBundleIDLen is the longest bundle identifier BundleID accepts.
const maxBundleIDLen = 155

// BundleID returns the bundle identifier of an ad-hoc build from the
// values of WB_TEAM_ID and WB_BUNDLE_ID: WB_BUNDLE_ID when it is set and
// valid (ValidBundleID), else AdhocBundleID. A set WB_TEAM_ID is
// refused (ErrTeamBuild).
func BundleID(team, bundleID string) (string, error) {
	if team != "" {
		return "", fmt.Errorf("%w (%s)", ErrTeamBuild, identityRule)
	}
	if bundleID == "" {
		return AdhocBundleID, nil
	}
	if !ValidBundleID(bundleID) {
		return "", fmt.Errorf("%s=%q is not a reverse-DNS bundle identifier: two or more labels of letters, digits and hyphens, joined by dots, at most %d bytes (%s)",
			BundleIDEnv, bundleID, maxBundleIDLen, identityRule)
	}
	return bundleID, nil
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

// teamIDLen is the length of an Apple team identifier.
const teamIDLen = 10

// ValidTeamID reports whether s is an Apple team identifier: ten
// uppercase ASCII letters and digits.
func ValidTeamID(s string) bool {
	return len(s) == teamIDLen && strings.IndexFunc(s, func(r rune) bool { return !isUpperOrDigit(r) }) < 0
}
