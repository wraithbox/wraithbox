//go:build darwin || linux

package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"testing"
)

// FuzzCheckCAs: CheckCAs never panics, and every certificate it accepts
// is a CA with a P-256 key, within the request's bounds.
func FuzzCheckCAs(f *testing.F) {
	f.Add([]byte("not a certificate"), []byte{}, uint8(1))
	f.Add(bytes.Repeat([]byte{0x30, 0x82}, 8), []byte{0x30, 0x00}, uint8(2))
	f.Add(selfSigned(f, "Wraith Box CA", true, newP256(f)), selfSigned(f, "leaf", false, newP256(f)), uint8(1))
	f.Add(selfSigned(f, "Wraith Box CA A", true, newP256(f)), selfSigned(f, "Wraith Box CA B", true, newP256(f)), uint8(2))
	f.Fuzz(func(t *testing.T, a, b []byte, n uint8) {
		ders := [][]byte{a, b, a, b}[:n%5]
		certs, err := CheckCAs(ders)
		if err != nil {
			return
		}
		if len(certs) == 0 || len(certs) > MaxCAs || len(certs) != len(ders) {
			t.Fatalf("accepted %d certificates of %d", len(certs), len(ders))
		}
		for _, c := range certs {
			key, ok := c.PublicKey.(*ecdsa.PublicKey)
			if !c.IsCA || !c.BasicConstraintsValid || !ok || key.Curve != elliptic.P256() || len(c.Raw) > MaxCASize {
				t.Fatalf("accepted a certificate outside the rule: %v", c.Subject)
			}
		}
	})
}

// FuzzParseRoots: ParseRoots never panics, and returns at least one
// certificate or an error.
func FuzzParseRoots(f *testing.F) {
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMAA=\n-----END CERTIFICATE-----\n"))
	f.Add([]byte("-----BEGIN TRUSTED CERTIFICATE-----\n-----END TRUSTED CERTIFICATE-----\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		roots, _, err := ParseRoots(data)
		if err == nil && len(roots) == 0 {
			t.Fatal("no roots and no error")
		}
	})
}
