package appbundle

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testCert makes a self-signed certificate with the common name and
// organizational units, and returns its PEM and its keychain hash.
func testCert(t *testing.T, cn string, ou ...string) (pemData []byte, hash string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn, OrganizationalUnit: ou},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), strings.ToUpper(hex.EncodeToString(sum[:]))
}

// TestC05BuildSigningIdentity: the build signs with the one Apple
// Development identity whose certificate has the team as its OU, and
// fails, listing what it found, on none or more than one.
func TestC05BuildSigningIdentity(t *testing.T) {
	good, goodHash := testCert(t, "Apple Development: dev@example.com (QWERTY1234)", testTeam)
	other, otherHash := testCert(t, "Apple Development: dev@example.com (OTHER56789)", otherTeam)
	// The team ID in the name and not in the OU: never chosen.
	named, namedHash := testCert(t, "Apple Development: "+testTeam, otherTeam)
	dist, distHash := testCert(t, "Developer ID Application: Example ("+testTeam+")", testTeam)
	second, secondHash := testCert(t, "Apple Development: other@example.com (SECOND9012)", testTeam)
	list := func(hashes ...string) string {
		names := map[string]string{
			goodHash: "Apple Development: dev@example.com (QWERTY1234)", otherHash: "Apple Development: dev@example.com (OTHER56789)",
			namedHash: "Apple Development: " + testTeam, distHash: "Developer ID Application: Example (" + testTeam + ")",
			secondHash: "Apple Development: other@example.com (SECOND9012)",
		}
		var b strings.Builder
		for i, h := range hashes {
			fmt.Fprintf(&b, "  %d) %s \"%s\"\n", i+1, h, names[h])
		}
		fmt.Fprintf(&b, "     %d valid identities found\n", len(hashes))
		return b.String()
	}
	certs := join(good, other, named, dist, second, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"))

	ids := ParseIdentities(list(goodHash, otherHash, namedHash, distHash), certs)
	id, err := ChooseIdentity(ids, testTeam)
	if err != nil || id.Hash != goodHash {
		t.Errorf("ChooseIdentity = %v, %v; want %s", id, err, goodHash)
	}

	for label, findIdentity := range map[string]string{
		"no identity of the team":  list(otherHash, namedHash, distHash),
		"two identities":           list(goodHash, secondHash),
		"no identities at all":     list(),
		"an identity without cert": "  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"Apple Development: x\"\n",
	} {
		id, err := ChooseIdentity(ParseIdentities(findIdentity, certs), testTeam)
		if err == nil {
			t.Errorf("%s: ChooseIdentity = %v, want an error", label, id)
			continue
		}
		if !strings.Contains(err.Error(), testTeam) || !strings.Contains(err.Error(), "Signing identity") {
			t.Errorf("%s: the message %q doesn't name the team and the rule", label, err)
		}
	}
	_, err = ChooseIdentity(ParseIdentities(list(otherHash), certs), testTeam)
	if err == nil || !strings.Contains(err.Error(), otherHash) {
		t.Errorf("the error %v doesn't list the identity it found", err)
	}
}

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// TestHelperEntitlements: a helper's entitlements are its row's groups
// and nothing else, which the team check passes.
func TestHelperEntitlements(t *testing.T) {
	for _, p := range Programs {
		if p.Place != PlaceHelper {
			continue
		}
		got := string(HelperEntitlements(testTeam, p.Roles))
		if strings.Count(got, "<key>") != 1 || !strings.Contains(got, "<key>"+groupsKey+"</key>") {
			t.Errorf("%s: entitlements\n%s", p.Name, got)
		}
		for _, g := range TeamGroups(testTeam, p.Roles) {
			if !strings.Contains(got, "<string>"+g+"</string>") {
				t.Errorf("%s: no group %s in\n%s", p.Name, g, got)
			}
		}
	}
}

func TestC05BuildMissingProfileMessage(t *testing.T) {
	err := MissingProfile(testTeam, "x doesn't exist")
	for _, want := range []string{TeamEnv, testTeam, "x doesn't exist", "S12-03-host-prerequisites", "Xcode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q doesn't name %q", err, want)
		}
	}
}
