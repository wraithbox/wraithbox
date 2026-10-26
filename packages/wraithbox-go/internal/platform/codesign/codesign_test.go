package codesign

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// testProfile is the decoded form of a development profile, as
// `security cms -D -i` prints it, with the certificate and the device
// list cut short.
const testProfile = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AppIDName</key>
	<string>XC nl wraithbox dev abcde12345</string>
	<key>ApplicationIdentifierPrefix</key>
	<array>
	<string>ABCDE12345</string>
	</array>
	<key>CreationDate</key>
	<date>2026-10-20T09:00:00Z</date>
	<key>Platform</key>
	<array>
		<string>OSX</string>
	</array>
	<key>IsXcodeManaged</key>
	<true/>
	<key>DeveloperCertificates</key>
	<array>
		<data>MDEy
		MzQ1</data>
	</array>
	<key>Entitlements</key>
	<dict>
		<key>com.apple.application-identifier</key>
		<string>ABCDE12345.nl.wraithbox.dev.abcde12345</string>
		<key>keychain-access-groups</key>
		<array>
				<string>ABCDE12345.*</string>
		</array>
		<key>com.apple.developer.team-identifier</key>
		<string>ABCDE12345</string>
	</dict>
	<key>ExpirationDate</key>
	<date>2027-10-20T09:00:00Z</date>
	<key>Name</key>
	<string>Mac Team Provisioning Profile: nl.wraithbox.dev.abcde12345</string>
	<key>ProvisionedDevices</key>
	<array>
		<string>00008112-000A0B0C0D0E0F10</string>
	</array>
	<key>TeamIdentifier</key>
	<array>
		<string>ABCDE12345</string>
	</array>
	<key>TeamName</key>
	<string>Test &amp; Team</string>
	<key>TimeToLive</key>
	<integer>365</integer>
	<key>UUID</key>
	<string>0f0e0d0c-0b0a-0908-0706-050403020100</string>
	<key>Version</key>
	<integer>1</integer>
</dict>
</plist>
`

func TestParseProfile(t *testing.T) {
	p, err := ParseProfile([]byte(testProfile))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.TeamIdentifier, []string{"ABCDE12345"}) || p.TeamName != "Test & Team" {
		t.Errorf("team %q %q", p.TeamIdentifier, p.TeamName)
	}
	if exp, ok := p.Plist["ExpirationDate"].(time.Time); !ok || !exp.Equal(time.Date(2027, 10, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("ExpirationDate = %v", p.Plist["ExpirationDate"])
	}
	if ttl, ok := p.Plist["TimeToLive"].(int64); !ok || ttl != 365 {
		t.Errorf("TimeToLive = %v", p.Plist["TimeToLive"])
	}
	certs, _ := p.Plist["DeveloperCertificates"].([]any)
	if len(certs) != 1 || !bytes.Equal(asBytes(certs[0]), []byte("012345")) {
		t.Errorf("DeveloperCertificates = %v", p.Plist["DeveloperCertificates"])
	}
	ent, _ := p.Plist["Entitlements"].(map[string]any)
	if groups, _ := ent["keychain-access-groups"].([]any); !slices.Equal(groups, []any{"ABCDE12345.*"}) {
		t.Errorf("Entitlements = %v", p.Plist["Entitlements"])
	}
	if p.Plist["IsXcodeManaged"] != true {
		t.Errorf("IsXcodeManaged = %v", p.Plist["IsXcodeManaged"])
	}
}

func asBytes(v any) []byte {
	b, _ := v.([]byte)
	return b
}

// TestParseProfileRefused sees a profile that names no team, or names it
// in the wrong form, refused.
func TestParseProfileRefused(t *testing.T) {
	teams := "<key>TeamIdentifier</key>\n\t<array>\n\t\t<string>ABCDE12345</string>\n\t</array>"
	name := "<key>TeamName</key>\n\t<string>Test &amp; Team</string>"
	for label, edit := range map[string]func(string) string{
		"no TeamIdentifier":       func(s string) string { return strings.Replace(s, teams, "", 1) },
		"an empty TeamIdentifier": func(s string) string { return strings.Replace(s, teams, "<key>TeamIdentifier</key><array/>", 1) },
		"a TeamIdentifier string": func(s string) string {
			return strings.Replace(s, teams, "<key>TeamIdentifier</key><string>ABCDE12345</string>", 1)
		},
		"a team that is a number": func(s string) string {
			return strings.Replace(s, teams, "<key>TeamIdentifier</key><array><integer>1</integer></array>", 1)
		},
		"no TeamName":   func(s string) string { return strings.Replace(s, name, "", 1) },
		"not a dict":    func(string) string { return "<plist><array/></plist>" },
		"a binary list": func(string) string { return "bplist00" },
	} {
		if p, err := ParseProfile([]byte(edit(testProfile))); err == nil {
			t.Errorf("%s: ParseProfile = %+v, want an error", label, p)
		}
	}
}

func TestDecodePlistRefused(t *testing.T) {
	for label, doc := range map[string]string{
		"empty":               "",
		"not a plist":         "<dict/>",
		"a key twice":         "<plist><dict><key>a</key><true/><key>a</key><false/></dict></plist>",
		"a key with no value": "<plist><dict><key>a</key></dict></plist>",
		"a value with no key": "<plist><dict><string>a</string></dict></plist>",
		"an unknown element":  "<plist><dict><key>a</key><set/></dict></plist>",
		"a bad integer":       "<plist><integer>x</integer></plist>",
		"a bad date":          "<plist><date>tomorrow</date></plist>",
		"bad data":            "<plist><data>!!</data></plist>",
		"text in a dict":      "<plist><dict>x<key>a</key><true/></dict></plist>",
		"two top values":      "<plist><true/><false/></plist>",
		"text after the list": "<plist><true/></plist>x",
		"unclosed":            "<plist><dict><key>a</key><true/>",
		"mismatched end":      "<plist><dict></array></plist>",
		"too deep":            "<plist>" + strings.Repeat("<array>", maxPlistDepth+1) + strings.Repeat("</array>", maxPlistDepth+1) + "</plist>",
		"too large":           "<plist><string>" + strings.Repeat("a", MaxPlistSize) + "</string></plist>",
	} {
		if v, err := DecodePlist([]byte(doc)); err == nil {
			t.Errorf("%s: DecodePlist = %v, want an error", label, v)
		}
	}
}

func TestDecodePlistValues(t *testing.T) {
	v, err := DecodePlist([]byte(`<?xml version="1.0"?><!-- c --><plist version="1.0"><array>
		<string></string><integer>-3</integer><real>1.5</real><false/><dict/><array/>
	</array></plist>
	`))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"", int64(-3), 1.5, false}
	got, ok := v.([]any)
	if !ok || len(got) != 6 || !slices.Equal(got[:4], want) {
		t.Errorf("DecodePlist = %#v", v)
	}
}

func TestParseTeam(t *testing.T) {
	cases := []struct {
		out  string
		want string
		err  error
	}{
		{"Executable=/x\nIdentifier=nl.wraithbox.dev.abcde12345\nTeamIdentifier=ABCDE12345\nSealed Resources=none\n", "ABCDE12345", nil},
		{"Executable=/bin/ls\nTeamIdentifier=not set\n", "", nil},
		{"Executable=/x\r\nTeamIdentifier=ABCDE12345\r\n", "ABCDE12345", nil},
		{"Executable=/x\nSignature=adhoc\n", "", ErrNoSignature},
		{"/x: code object is not signed at all\n", "", ErrNoSignature},
		{"", "", ErrNoSignature},
	}
	for _, tc := range cases {
		got, err := ParseTeam(tc.out)
		if got != tc.want || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("ParseTeam(%q) = %q, %v; want %q, %v", tc.out, got, err, tc.want, tc.err)
		}
	}
}

// FuzzDecodePlist feeds the profile decoder any bytes. It must return a
// value or an error, never panic, and a value it returns must not hold a
// type outside the property list types.
func FuzzDecodePlist(f *testing.F) {
	f.Add([]byte(testProfile))
	f.Add([]byte("<plist><dict><key>a</key><array><data>AA==</data></array></dict></plist>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := DecodePlist(data)
		if err != nil {
			return
		}
		if !plistTypes(v, 0) {
			t.Fatalf("DecodePlist returned %#v", v)
		}
		_, _ = ParseProfile(data)
	})
}

func plistTypes(v any, depth int) bool {
	if depth > maxPlistDepth+1 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		for _, item := range x {
			if !plistTypes(item, depth+1) {
				return false
			}
		}
		return true
	case []any:
		for _, item := range x {
			if !plistTypes(item, depth+1) {
				return false
			}
		}
		return true
	case string, int64, float64, bool, time.Time, []byte:
		return true
	}
	return false
}
