package carecord

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// validityTolerance is how far a CA's validity may be from Lifetime and
// the backdate wb-proxyd gives it, and how far its NotBefore may be
// ahead of wb-hostd's clock (S09-07-ca-certificates, "Lifetimes").
const validityTolerance = time.Hour

// backdate is how far before its issue wb-proxyd starts a CA's
// validity, for a guest clock that runs a little behind.
const backdate = 5 * time.Minute

// The extensions the CA profile names (S09-07-ca-certificates, "CA
// profile" and "Scope").
var (
	oidBasicConstraints = asn1.ObjectIdentifier{2, 5, 29, 19}
	oidExtKeyUsage      = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidNameConstraints  = asn1.ObjectIdentifier{2, 5, 29, 30}
)

// errInvalidReply is a reply of wb-proxyd that broke the contract of
// control.proto or of S09-07-ca-certificates. wb-hostd ends wb-proxyd
// for it, as for a malformed record (S04-08-records-and-bounds).
var errInvalidReply = errors.New("carecord: invalid reply of wb-proxyd")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalidReply, fmt.Sprintf(format, a...))
}

// decodeReply decodes one record of wb-proxyd's control channel and
// checks its form (internal/egressmsg). A refused record is an invalid
// reply.
func decodeReply(record []byte) (*egressv1.ProxydToHostd, error) {
	m, ref := egressmsg.DecodeProxydToHostd(record, false)
	if ref != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidReply, ref)
	}
	return m, nil
}

// Issued is wb-proxyd's answer to a CaIssue that passed every check:
// the certificate, or the rule of a failed issue.
type Issued struct {
	Cert *x509.Certificate
	// SHA256 is the certificate's fingerprint, lowercase hexadecimal.
	SHA256 string
	// Rule is set when wb-proxyd failed the issue, and Cert is nil.
	Rule string
}

// CheckIssued checks record as wb-proxyd's answer to a CaIssue of slot
// and id at now (S09-07-ca-certificates, "Messages"). The answer must
// be a CaIssued of that slot and id. Its certificate must have the CA
// profile (CheckCA). Anything else is an error that wraps
// errInvalidReply. This is the parser FuzzProxydCAReply feeds.
func CheckIssued(record []byte, slot, id string, now time.Time) (Issued, error) {
	m, err := decodeReply(record)
	if err != nil {
		return Issued{}, err
	}
	r := m.GetCaIssued()
	if r == nil {
		return Issued{}, invalid("answered a CaIssue with %s", kindName(m))
	}
	if r.GetVm() != slot || r.GetCaId() != id {
		return Issued{}, invalid("answered the CaIssue of another VM or CA id")
	}
	if rule := r.GetRule(); rule != "" {
		return Issued{Rule: rule}, nil
	}
	cert, err := CheckCA(r.GetCertificate(), now)
	if err != nil {
		return Issued{}, err
	}
	sum := sha256.Sum256(cert.Raw)
	return Issued{Cert: cert, SHA256: hex.EncodeToString(sum[:])}, nil
}

// CheckCA checks that der is a certificate of the CA profile of
// S09-07-ca-certificates, issued at about now:
//
//   - it parses, has an ECDSA P-256 key, and is self-signed by it;
//   - basicConstraints is critical, with CA:TRUE and pathLen:0;
//   - keyUsage is keyCertSign only;
//   - it has no extended key usage, no name constraints, and no
//     critical extension Go doesn't handle;
//   - its validity is Lifetime and the backdate, within
//     validityTolerance, and starts by now and the tolerance.
//
// A certificate that fails is an error that wraps errInvalidReply.
func CheckCA(der []byte, now time.Time) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, invalid("the CA certificate doesn't parse: %v", err)
	}
	if err := checkKey(cert); err != nil {
		return nil, err
	}
	if err := checkExtensions(cert); err != nil {
		return nil, err
	}
	return cert, checkValidity(cert, now)
}

// checkKey: an ECDSA P-256 key that signed the certificate itself.
func checkKey(cert *x509.Certificate) error {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return invalid("the CA key isn't ECDSA P-256")
	}
	if !bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return invalid("the CA certificate isn't self-issued")
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return invalid("the CA certificate isn't self-signed: %v", err)
	}
	return nil
}

// checkExtensions: the extensions of the CA profile, and no other that
// constrains the CA.
func checkExtensions(cert *x509.Certificate) error {
	if len(cert.UnhandledCriticalExtensions) > 0 {
		return invalid("the CA certificate has a critical extension of an unknown kind")
	}
	if !cert.BasicConstraintsValid || !cert.IsCA || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		return invalid("basicConstraints isn't CA:TRUE with pathLen:0")
	}
	if cert.KeyUsage != x509.KeyUsageCertSign {
		return invalid("keyUsage isn't keyCertSign only")
	}
	critical := false
	for _, e := range cert.Extensions {
		switch {
		case e.Id.Equal(oidBasicConstraints):
			critical = e.Critical
		case e.Id.Equal(oidExtKeyUsage):
			return invalid("the CA certificate has an extended key usage")
		case e.Id.Equal(oidNameConstraints):
			return invalid("the CA certificate has name constraints")
		}
	}
	if !critical {
		return invalid("basicConstraints isn't critical")
	}
	return nil
}

// checkValidity: about Lifetime, starting at about now.
func checkValidity(cert *x509.Certificate, now time.Time) error {
	want := Lifetime + backdate
	if d := cert.NotAfter.Sub(cert.NotBefore); d < want-validityTolerance || d > want+validityTolerance {
		return invalid("the CA certificate is valid for %v, want %v", d, want)
	}
	if cert.NotBefore.After(now.Add(validityTolerance)) || cert.NotBefore.Before(now.Add(-backdate-validityTolerance)) {
		return invalid("the CA certificate's validity starts at %v, not about now", cert.NotBefore.UTC())
	}
	return nil
}

// kindName names a reply's kind for an error.
func kindName(m *egressv1.ProxydToHostd) string {
	return fmt.Sprintf("%T", m.GetKind())
}
