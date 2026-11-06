package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
)

// The request's bounds (proto/wraithbox/guest/v1, InstallTrustRequest).
const (
	MaxCAs    = 3
	MaxCASize = 1024
)

// MaxRootsSize bounds the public roots file. macOS's is about 300 KB.
const MaxRootsSize = 4 << 20

// CheckCAs parses the request's certificates, and refuses them unless
// there are 1 to MaxCAs, each of 1 to MaxCASize bytes, each a DER X.509
// certificate that parses with nothing after it, with basic
// constraints CA:TRUE and a P-256 public key, and no two the same. The
// request comes from wb-hostd, but wb-guestd still checks it, so a host
// bug can't put a leaf or garbage in every client's roots
// (S09-08-guest-trust, "Guest trust"; SEC11-root-gains-nothing).
func CheckCAs(ders [][]byte) ([]*x509.Certificate, error) {
	if len(ders) == 0 || len(ders) > MaxCAs {
		return nil, fmt.Errorf("%d certificates, want 1 to %d", len(ders), MaxCAs)
	}
	seen := make(map[string]bool, len(ders))
	out := make([]*x509.Certificate, 0, len(ders))
	for i, der := range ders {
		cert, err := checkCA(der)
		if err != nil {
			return nil, fmt.Errorf("certificate %d: %w", i, err)
		}
		fp := Fingerprint(der)
		if seen[fp] {
			return nil, fmt.Errorf("certificate %d: the same as an earlier one", i)
		}
		seen[fp] = true
		out = append(out, cert)
	}
	return out, nil
}

// checkCA checks one certificate of the request.
func checkCA(der []byte) (*x509.Certificate, error) {
	if len(der) == 0 || len(der) > MaxCASize {
		return nil, fmt.Errorf("%d bytes, want 1 to %d", len(der), MaxCASize)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("doesn't parse: %w", err)
	}
	if !cert.BasicConstraintsValid || !cert.IsCA {
		return nil, errors.New("not a CA: no basic constraints with CA:TRUE")
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("public key %s, want ECDSA P-256", cert.PublicKeyAlgorithm)
	}
	return cert, nil
}

// Fingerprint is the lowercase hex SHA-256 of a certificate's DER.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ParseRoots reads the CERTIFICATE blocks of a PEM file of public
// roots. It skips other blocks, and a certificate that doesn't parse,
// and returns how many it skipped. A file of more than MaxRootsSize
// bytes, or one without a certificate that parses, is an error.
func ParseRoots(data []byte) (roots []*x509.Certificate, skipped int, err error) {
	if len(data) > MaxRootsSize {
		return nil, 0, fmt.Errorf("%d bytes, more than %d", len(data), MaxRootsSize)
	}
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			skipped++
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			skipped++
			continue
		}
		roots = append(roots, cert)
	}
	if len(roots) == 0 {
		return nil, skipped, errors.New("no certificate that parses")
	}
	return roots, skipped, nil
}

// bundleOf returns the public roots and then the CAs, with each
// certificate once: the first time it appears.
func bundleOf(roots, cas []*x509.Certificate) []*x509.Certificate {
	out := make([]*x509.Certificate, 0, len(roots)+len(cas))
	seen := make(map[string]bool, len(roots)+len(cas))
	for _, list := range [][]*x509.Certificate{roots, cas} {
		for _, c := range list {
			if !seen[string(c.Raw)] {
				seen[string(c.Raw)] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// encodePEM writes each certificate as a PEM CERTIFICATE block.
func encodePEM(certs []*x509.Certificate) []byte {
	var b bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.Bytes()
}
