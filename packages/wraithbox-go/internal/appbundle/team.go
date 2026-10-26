package appbundle

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: the keychain names a signing identity by its certificate's SHA-1 hash; it is a lookup key, not a security check
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// TeamSetupSteps is the one-time setup of S12-03-host-prerequisites,
// "Development builds on macOS", and the setting it gives the build. The
// build prints it when Xcode gives no profile, and wb setup when no team
// signed the build.
const TeamSetupSteps = `To sign the build with your own team, once per Mac:
  1. Sign in to Xcode with an Apple ID (Xcode, Settings, Accounts). Xcode lists its team as a personal team.
  2. Build a small app project once in the Xcode app, with that team and "My Mac" as the destination.
Then set ` + TeamEnv + ` to the team ID, the organizational unit (OU) in the Apple Development certificate's subject, and build Wraith Box again.`

// ErrNoProfile is the error of a build with a team whose Xcode build gave
// the app no profile (S12-03-host-prerequisites, "Missing profile").
var ErrNoProfile = errors.New("the app from Xcode has no " + ProfileName)

// MissingProfile returns the error of a build with team whose app from
// Xcode has no profile, or a profile of another team. It names the
// one-time setup and WB_TEAM_ID, so the build stops before it makes a
// bundle that macOS would refuse to start (S12-03-host-prerequisites,
// "Missing profile").
func MissingProfile(team, detail string) error {
	return fmt.Errorf("%w for the team %s (%s): %s (S12-03-host-prerequisites, \"Missing profile\")\n%s",
		ErrNoProfile, team, TeamEnv, detail, TeamSetupSteps)
}

// SigningIdentity is a code signing identity in the user's keychains.
type SigningIdentity struct {
	// Hash is the SHA-1 hash of the certificate, in uppercase hex, the
	// name codesign takes for it.
	Hash string
	// Name is the name `security find-identity` prints for it, the
	// certificate's common name.
	Name string
	// Teams are the organizational units (OU) of the certificate's
	// subject. An Apple Development certificate has one, its team ID.
	Teams []string
}

func (s SigningIdentity) String() string {
	return fmt.Sprintf("%s %q (OU %s)", s.Hash, s.Name, strings.Join(s.Teams, ","))
}

// developmentPrefix starts the common name of an Apple Development
// certificate.
const developmentPrefix = "Apple Development: "

// ChooseIdentity returns the one Apple Development identity of team: an
// identity whose name starts with "Apple Development: " and whose
// certificate has team as its organizational unit. It chooses by the
// team ID in the certificate, never by a part of the name. No match, or
// more than one, is an error that lists the identities it found. One
// certificate listed twice, as in two keychains, counts once: codesign
// names it by its hash, so both are the same identity.
func ChooseIdentity(ids []SigningIdentity, team string) (SigningIdentity, error) {
	var matches []SigningIdentity
	for _, id := range ids {
		duplicate := slices.ContainsFunc(matches, func(m SigningIdentity) bool { return m.Hash == id.Hash })
		if !duplicate && strings.HasPrefix(id.Name, developmentPrefix) && slices.Equal(id.Teams, []string{team}) {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	found := make([]string, 0, len(ids))
	for _, id := range ids {
		found = append(found, "  "+id.String())
	}
	list := strings.Join(found, "\n")
	if len(found) == 0 {
		list = "  (none)"
	}
	return SigningIdentity{}, fmt.Errorf("%d Apple Development identities have the team %s as their OU, and the build signs with exactly one (%s). The valid code signing identities:\n%s",
		len(matches), team, identityRule, list)
}

// identityLine is a line of `security find-identity -v -p codesigning`:
// an index, the SHA-1 hash, and the name in quotes.
var identityLine = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-F]{40})\s+"(.*)"\s*$`)

// ParseIdentities joins the output of `security find-identity -v -p
// codesigning` with the certificates of `security find-certificate -a
// -p`, by SHA-1 hash, and returns each listed identity with the
// organizational units of its certificate. An identity whose certificate
// isn't among them gets no team, so ChooseIdentity never picks it.
func ParseIdentities(findIdentity string, certsPEM []byte) []SigningIdentity {
	teams := certificateTeams(certsPEM)
	var ids []SigningIdentity
	for line := range strings.Lines(findIdentity) {
		m := identityLine.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if m == nil {
			continue
		}
		ids = append(ids, SigningIdentity{Hash: m[1], Name: m[2], Teams: teams[m[1]]})
	}
	return ids
}

// certificateTeams returns the organizational units of each certificate
// in the PEM data, by the uppercase hex SHA-1 hash of its DER bytes.
func certificateTeams(data []byte) map[string][]string {
	out := map[string][]string{}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return out
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			// The keychains hold certificates of every kind. One Go can't
			// parse can't be the identity: its hash finds no team.
			continue
		}
		sum := sha1.Sum(block.Bytes) //nolint:gosec // G401: the keychain's name for a certificate is its SHA-1 hash
		out[strings.ToUpper(hex.EncodeToString(sum[:]))] = cert.Subject.OrganizationalUnit
	}
}

// HelperEntitlements is the entitlements file of a helper in a build
// team signs: keychain-access-groups with its row's groups
// (TeamGroups), and nothing else (S10-02-packaging-and-signing, "Helper
// bundles"). X29-dev-signing ran a helper with only that key.
func HelperEntitlements(team string, roles []string) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>` + groupsKey + `</key>
	<array>
`)
	for _, g := range TeamGroups(team, roles) {
		b.WriteString("\t\t<string>")
		// Writes to a bytes.Buffer don't fail.
		_ = xml.EscapeText(&b, []byte(g))
		b.WriteString("</string>\n")
	}
	b.WriteString("\t</array>\n</dict>\n</plist>\n")
	return b.Bytes()
}
