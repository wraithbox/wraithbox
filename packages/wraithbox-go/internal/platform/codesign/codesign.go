// Package codesign reads how a program on the host was signed: the team
// of its code signature, and the provisioning profile in its bundle.
// wb setup reads both from the helper bundles to name the team that
// signed the build, or to refuse the Keychain backend
// (S12-03-host-prerequisites, "Team-signed build"; S15-02-secret-store,
// "Ad-hoc signed builds").
//
// The calls are per OS (S12-01-platform-interfaces, "Rules"). On darwin
// they run /usr/bin/codesign and /usr/bin/security by absolute path,
// with a fixed environment. Code signing of this kind is macOS only, so
// on Linux and Windows both return an error that wraps
// errors.ErrUnsupported: unimplemented platforms fail clearly.
//
// The parsers are shared, so they are tested on every OS. The bytes they
// read come from the user's own bundle and tools, never from a guest.
package codesign

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoSignature is ParseTeam's error when the output of codesign has no
// TeamIdentifier line, such as for an unsigned file.
var ErrNoSignature = errors.New("codesign: no TeamIdentifier in its output")

// teamLine is the line `codesign -d --verbose=1` prints for the team of a
// signature, and teamNotSet its value for a signature without a team:
// ad-hoc, or Apple's own.
const (
	teamLine   = "TeamIdentifier="
	teamNotSet = "not set"
)

// ParseTeam returns the team of a signature from the output of
// `codesign -d --verbose=1`: the value of its TeamIdentifier line, or ""
// when that is "not set". Output without the line is ErrNoSignature. The
// output also holds the signature's identifier, which the signer chose,
// so a newline in it could put a TeamIdentifier line before the real
// one. Output with more than one such line, or a value that is neither
// "not set" nor a team ID (ValidTeamID), is an error, and a caller
// treats the signature as one it can't read.
func ParseTeam(out string) (string, error) {
	var values []string
	for line := range strings.Lines(out) {
		if value, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), teamLine); ok {
			values = append(values, value)
		}
	}
	switch {
	case len(values) == 0:
		return "", ErrNoSignature
	case len(values) > 1:
		return "", fmt.Errorf("codesign: %d TeamIdentifier lines in its output, not one", len(values))
	case values[0] == teamNotSet:
		return "", nil
	case !ValidTeamID(values[0]):
		return "", fmt.Errorf("codesign: the TeamIdentifier %q is not a team ID", values[0])
	}
	return values[0], nil
}

// teamIDLen is the length of an Apple team identifier.
const teamIDLen = 10

// ValidTeamID reports whether s is an Apple team identifier: ten
// uppercase ASCII letters and digits.
func ValidTeamID(s string) bool {
	if len(s) != teamIDLen {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// Profile is a decoded provisioning profile.
type Profile struct {
	// TeamIdentifier is the profile's list of teams, and TeamName the
	// name of its team.
	TeamIdentifier []string
	TeamName       string
	// Plist is the whole decoded profile, with dates as time.Time, so a
	// caller can read keys this type doesn't name, such as
	// ExpirationDate.
	Plist map[string]any
}

// ParseProfile reads a profile from the XML property list that
// `security cms -D` prints for it. That command decodes the CMS envelope
// without verifying who signed it, so the content isn't authenticated.
// wb setup uses a profile only after the helper's code signature, which
// macOS does verify, has named the team, and then only to compare it
// with that team and to print its TeamName. The expiry date that M6.6
// reads from it must keep that order. TeamIdentifier must be a list of
// strings and TeamName a string. A missing or malformed key is an
// error, so a profile that names no team never counts as one that does.
func ParseProfile(data []byte) (Profile, error) {
	root, err := DecodePlist(data)
	if err != nil {
		return Profile{}, err
	}
	dict, ok := root.(map[string]any)
	if !ok {
		return Profile{}, errors.New("profile: the property list is not a dict")
	}
	p := Profile{Plist: dict}
	teams, ok := dict["TeamIdentifier"].([]any)
	if !ok || len(teams) == 0 {
		return Profile{}, errors.New("profile: TeamIdentifier is not a list of teams")
	}
	for _, t := range teams {
		s, ok := t.(string)
		if !ok {
			return Profile{}, fmt.Errorf("profile: TeamIdentifier holds %v, not a string", t)
		}
		p.TeamIdentifier = append(p.TeamIdentifier, s)
	}
	if p.TeamName, ok = dict["TeamName"].(string); !ok {
		return Profile{}, errors.New("profile: TeamName is not a string")
	}
	return p, nil
}
