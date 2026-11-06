package carecord

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

const testID = "0123456789abcdef0123456789abcdef"

// TestCheckCA feeds CheckCA certificates without the CA profile of
// S09-07-ca-certificates and sees each refused.
func TestCheckCA(t *testing.T) {
	if _, err := CheckCA(newCA(start, nil), start); err != nil {
		t.Fatalf("the profile refused: %v", err)
	}
	for name, edit := range map[string]func(*x509.Certificate, *crypto.Signer){
		"P-384 key": func(_ *x509.Certificate, s *crypto.Signer) { *s = mustKey(elliptic.P384()) },
		"not CA":    func(c *x509.Certificate, _ *crypto.Signer) { c.IsCA, c.MaxPathLenZero = false, false },
		"no basicConstraints": func(c *x509.Certificate, _ *crypto.Signer) {
			c.BasicConstraintsValid, c.IsCA = false, false
		},
		"pathLen 1":            func(c *x509.Certificate, _ *crypto.Signer) { c.MaxPathLen, c.MaxPathLenZero = 1, false },
		"no pathLen":           func(c *x509.Certificate, _ *crypto.Signer) { c.MaxPathLen, c.MaxPathLenZero = -1, false },
		"digitalSignature too": func(c *x509.Certificate, _ *crypto.Signer) { c.KeyUsage |= x509.KeyUsageDigitalSignature },
		"extended key usage": func(c *x509.Certificate, _ *crypto.Signer) {
			c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		},
		"name constraints": func(c *x509.Certificate, _ *crypto.Signer) { c.PermittedDNSDomains = []string{"example.com"} },
		"30 days":          func(c *x509.Certificate, _ *crypto.Signer) { c.NotAfter = c.NotBefore.Add(30 * day) },
		"starts tomorrow": func(c *x509.Certificate, _ *crypto.Signer) {
			c.NotBefore, c.NotAfter = c.NotBefore.Add(day), c.NotAfter.Add(day)
		},
		"started yesterday": func(c *x509.Certificate, _ *crypto.Signer) {
			c.NotBefore, c.NotAfter = c.NotBefore.Add(-day), c.NotAfter.Add(-day)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := CheckCA(newCA(start, edit), start)
			if !errors.Is(err, errInvalidReply) {
				t.Errorf("CheckCA = %v, want an invalid reply", err)
			}
		})
	}
	if _, err := CheckCA([]byte{0x30, 0}, start); !errors.Is(err, errInvalidReply) {
		t.Errorf("garbage: %v", err)
	}
}

// TestCheckCANotSelfSigned: a certificate signed by another key than
// its own is refused.
func TestCheckCANotSelfSigned(t *testing.T) {
	key, other := mustKey(elliptic.P256()), mustKey(elliptic.P256())
	name := pkix.Name{CommonName: "ca"}
	tmpl := &x509.Certificate{
		SerialNumber: bigOne(), Subject: name, Issuer: name, NotBefore: start.Add(-backdate), NotAfter: start.Add(Lifetime),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckCA(der, start); !errors.Is(err, errInvalidReply) {
		t.Errorf("CheckCA = %v, want an invalid reply", err)
	}
}

// TestCheckIssued: the answer must be a CaIssued of the VM and CA id
// asked for.
func TestCheckIssued(t *testing.T) {
	good := newCA(start, nil)
	issued := func(vm, id string, cert []byte, rule string) []byte {
		b, _ := proto.Marshal(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: &egressv1.CaIssued{
			Vm: vm, CaId: id, Certificate: cert, Rule: rule,
		}}})
		return b
	}
	got, err := CheckIssued(issued("work", testID, good, ""), "work", testID, start)
	if err != nil || got.Cert == nil || len(got.SHA256) != 64 {
		t.Fatalf("CheckIssued = %+v, %v", got, err)
	}
	if got, err = CheckIssued(issued("work", testID, nil, egressmsg.RuleCAKeyRestore), "work", testID, start); err != nil || got.Rule != egressmsg.RuleCAKeyRestore {
		t.Errorf("failed issue = %+v, %v", got, err)
	}
	loaded, _ := proto.Marshal(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{Vm: "work", Generation: 1}}})
	for name, rec := range map[string][]byte{
		"other VM":      issued("isolated", testID, good, ""),
		"other CA id":   issued("work", "f"+testID[1:], good, ""),
		"another kind":  loaded,
		"garbage":       {0xff, 0xff},
		"bad cert":      issued("work", testID, []byte{0x30, 0}, ""),
		"rule and cert": issued("work", testID, good, egressmsg.RuleCAKeyRestore),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckIssued(rec, "work", testID, start); !errors.Is(err, errInvalidReply) {
				t.Errorf("CheckIssued = %v, want an invalid reply", err)
			}
		})
	}
}

// FuzzProxydCAReply feeds any bytes to wb-hostd's parser of wb-proxyd's
// answer to a CaIssue. It never panics, and an answer it takes holds a
// certificate of the CA profile or the rule of a failed issue.
func FuzzProxydCAReply(f *testing.F) {
	now := start
	for _, cert := range [][]byte{newCA(now, nil), newCA(now, func(c *x509.Certificate, _ *crypto.Signer) { c.KeyUsage |= x509.KeyUsageCRLSign })} {
		b, _ := proto.Marshal(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: &egressv1.CaIssued{
			Vm: "work", CaId: testID, Certificate: cert,
		}}})
		f.Add(b)
	}
	b, _ := proto.Marshal(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: &egressv1.CaIssued{
		Vm: "work", CaId: testID, Rule: egressmsg.RuleCAIssueRefused,
	}}})
	f.Add(b)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, rec []byte) {
		got, err := CheckIssued(rec, "work", testID, now)
		if err != nil {
			if !errors.Is(err, errInvalidReply) {
				t.Fatalf("error %v isn't an invalid reply", err)
			}
			return
		}
		if got.Rule != "" {
			if got.Cert != nil {
				t.Fatal("a rule with a certificate")
			}
			return
		}
		requireProfile(t, got.Cert, now)
	})
}

// requireProfile checks the CA profile again, from the parsed
// certificate.
func requireProfile(t *testing.T, c *x509.Certificate, now time.Time) {
	t.Helper()
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	switch {
	case !ok || pub.Curve != elliptic.P256():
		t.Fatal("a CA key that isn't P-256")
	case !c.IsCA || c.MaxPathLen != 0 || c.KeyUsage != x509.KeyUsageCertSign:
		t.Fatal("a certificate without the CA profile")
	case len(c.ExtKeyUsage) > 0 || len(c.UnknownExtKeyUsage) > 0 || len(c.PermittedDNSDomains) > 0:
		t.Fatal("extended key usage or name constraints")
	case c.NotAfter.Before(now.Add(Lifetime - 2*validityTolerance)):
		t.Fatal("a CA that ends early")
	}
}

func bigOne() *big.Int { return big.NewInt(1) }

func mustKey(c elliptic.Curve) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}
