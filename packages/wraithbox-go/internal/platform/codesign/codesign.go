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
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
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
// value is returned as it is, and the caller checks its form.
func ParseTeam(out string) (string, error) {
	for line := range strings.Lines(out) {
		value, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), teamLine)
		if !ok {
			continue
		}
		if value == teamNotSet {
			return "", nil
		}
		return value, nil
	}
	return "", ErrNoSignature
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
// `security cms -D` prints for it. TeamIdentifier must be a list of
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
