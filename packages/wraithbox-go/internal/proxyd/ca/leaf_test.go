package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestC02LeafRefusals: the leaf refusals of C02, "Leaf issuance", that
// this package sees, each logged with its rule.
func TestC02LeafRefusals(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	for _, c := range []struct{ name, reason string }{
		{"*.example.com", ReasonWildcard},
		{"*", ReasonWildcard},
		{"192.0.2.1", ReasonIPAddress},
		{"2001:db8::1", ReasonIPAddress},
		{"[::1]", ReasonIPAddress},
		{"Example.com", ReasonNotDNSName},
		{"example.com.", ReasonNotDNSName},
		{"", ReasonNotDNSName},
		{"exa mple.com", ReasonNotDNSName},
		{strings.Repeat("a", 64) + ".com", ReasonNotDNSName},
	} {
		e.wantRefusal("work", 1, c.name, RuleLeafRefused, c.reason)
	}
	e.wantRefusal("isolated", 1, "example.com", RuleLeafRefused, ReasonNoSigningCA)

	// An older generation.
	der := e.a.vms["work"].cas[caA].cert.Raw
	e.loaded("work", 2, caA, caA, der)
	e.accept("work", 2)
	e.wantRefusal("work", 1, "example.com", RuleLeafRefused, ReasonGeneration)

	// A CA set with no signing CA (S09-07-ca-certificates, "Rotation",
	// day 58).
	e.loaded("work", 2, "", caA, der)
	e.wantRefusal("work", 2, "example.com", RuleNotInstalled, "")

	// An expired CA, even with a leaf cached.
	e.loaded("work", 2, caA, caA, der)
	if leaf, ref := e.a.For("work", 2, "example.com"); leaf == nil {
		t.Fatal(ref)
	}
	e.advance(60*24*time.Hour + time.Second)
	e.wantRefusal("work", 2, "example.com", RuleLeafRefused, ReasonExpiredCA)

	if !strings.Contains(e.logs.String(), `generation=2 ca_id=`+caA+` host="\"example.com\"" rule=ca-leaf-refused reason="the signing CA has expired`) {
		t.Errorf("the refusal isn't logged with its rule:\n%s", e.logs)
	}
}

// TestC02LeafProfile: a leaf has exactly the leaf profile, verifies
// against its CA, and ends at the CA's end when that comes first.
func TestC02LeafProfile(t *testing.T) {
	e := newEnv(t, team)
	ca := e.ready()
	leaf, ref := e.a.For("work", 1, "api.example.com")
	if leaf == nil {
		t.Fatal(ref)
	}
	if len(leaf.Certificate) != 1 {
		t.Errorf("chain of %d certificates, want the leaf only", len(leaf.Certificate))
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	checkLeafProfile(t, cert, "api.example.com")
	if !cert.NotBefore.Equal(start.Add(-backdate)) || !cert.NotAfter.Equal(start.Add(24*time.Hour)) {
		t.Errorf("validity = %v to %v, want 5 minutes back to 24 hours ahead of %v", cert.NotBefore, cert.NotAfter, start)
	}
	if key, ok := leaf.PrivateKey.(*ecdsa.PrivateKey); !ok || !key.PublicKey.Equal(cert.PublicKey) || key.Curve != elliptic.P256() {
		t.Error("the leaf's private key isn't the P-256 key of its certificate")
	}
	verify(t, ca, cert, "api.example.com", e.clock())

	// Near the CA's end, the leaf ends with the CA.
	e.advance(59*24*time.Hour + 12*time.Hour)
	leaf, ref = e.a.For("work", 1, "end.example.com")
	if leaf == nil {
		t.Fatal(ref)
	}
	if !leaf.Leaf.NotAfter.Equal(ca.NotAfter) {
		t.Errorf("leaf ends %v, want the CA's end %v", leaf.Leaf.NotAfter, ca.NotAfter)
	}
	verify(t, ca, leaf.Leaf, "end.example.com", e.clock())
}

// checkLeafProfile checks the leaf profile of S09-07-ca-certificates:
// exactly one dNSName, CA:FALSE, extended key usage serverAuth, and a
// serial of at most 128 bits.
func checkLeafProfile(t *testing.T, cert *x509.Certificate, name string) {
	t.Helper()
	if !reflect.DeepEqual(cert.DNSNames, []string{name}) || len(cert.IPAddresses)+len(cert.EmailAddresses)+len(cert.URIs) != 0 {
		t.Errorf("names = %v %v %v %v, want the one dNSName %s", cert.DNSNames, cert.IPAddresses, cert.EmailAddresses, cert.URIs, name)
	}
	if !cert.BasicConstraintsValid || cert.IsCA {
		t.Errorf("basic constraints = valid %v, CA %v, want CA:FALSE", cert.BasicConstraintsValid, cert.IsCA)
	}
	if !reflect.DeepEqual(cert.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) || len(cert.UnknownExtKeyUsage) != 0 {
		t.Errorf("extended key usage = %v %v, want serverAuth", cert.ExtKeyUsage, cert.UnknownExtKeyUsage)
	}
	if cert.SerialNumber.Sign() <= 0 || cert.SerialNumber.BitLen() > 128 {
		t.Errorf("serial = %v, want 1 to 128 bits", cert.SerialNumber)
	}
}

// verify checks that cert verifies against ca for name at now, as a
// guest that trusts the CA does.
func verify(t *testing.T, ca, cert *x509.Certificate, name string, now time.Time) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: now}); err != nil {
		t.Errorf("the leaf for %s doesn't verify against its CA: %v", name, err)
	}
}

// TestC02LeafCache: the cache returns the same leaf for the same VM, CA
// and hostname, a new one near the leaf's end and after a signing CA
// change, and stays within its cap.
func TestC02LeafCache(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	first, _ := e.a.For("work", 1, "example.com")
	if again, _ := e.a.For("work", 1, "example.com"); again != first {
		t.Error("a second leaf for the same host isn't the cached one")
	}
	if other, _ := e.a.For("work", 1, "example.org"); other == first {
		t.Error("another host got the same leaf")
	}

	e.advance(22*time.Hour + 59*time.Minute)
	if again, _ := e.a.For("work", 1, "example.com"); again != first {
		t.Error("the leaf was replaced more than an hour before its end")
	}
	e.advance(time.Minute)
	renewed, _ := e.a.For("work", 1, "example.com")
	if renewed == first {
		t.Error("the leaf wasn't replaced an hour before its end")
	}

	derA := e.a.vms["work"].cas[caA].cert.Raw
	derB := e.issue("work", caB)
	e.loaded("work", 1, caA, caA, derA, caB, derB)
	if again, _ := e.a.For("work", 1, "example.com"); again != renewed {
		t.Error("a CA set with the same signing CA dropped the cache")
	}
	e.loaded("work", 1, caB, caA, derA, caB, derB)
	switched, _ := e.a.For("work", 1, "example.com")
	if switched == renewed || switched.Leaf.Issuer.String() == renewed.Leaf.Issuer.String() {
		t.Error("the leaf after the signing CA change isn't signed by the new CA")
	}

	for i := range maxLeavesPerVM + 10 {
		if leaf, ref := e.a.For("work", 1, fmt.Sprintf("h%d.example.com", i)); leaf == nil {
			t.Fatal(ref)
		}
	}
	cache := e.a.vms["work"].leaves
	if n := len(cache.byHost); n != maxLeavesPerVM {
		t.Errorf("the cache holds %d leaves, want the cap %d", n, maxLeavesPerVM)
	}
	if _, ok := cache.byHost["h0.example.com"]; ok {
		t.Error("the least recently used leaf wasn't dropped")
	}
}

// TestLeafConcurrent signs leaves while the CA set changes, under the
// race detector.
func TestLeafConcurrent(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	derA := e.a.vms["work"].cas[caA].cert.Raw
	derB := e.issue("work", caB)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 50 {
				e.a.For("work", 1, fmt.Sprintf("h%d-%d.example.com", g, i%5))
			}
		})
	}
	for i := range 20 {
		signing := caA
		if i%2 == 1 {
			signing = caB
		}
		e.loaded("work", 1, signing, caA, derA, caB, derB)
	}
	wg.Wait()
}

// TestC02VMScope: each VM's leaves are signed by its own CA, so a leaf
// signed for one VM doesn't verify in another (S09-07-ca-certificates,
// "Scope").
func TestC02VMScope(t *testing.T) {
	e := newEnv(t, team)
	work := e.ready()
	der := e.issue("isolated", caB)
	e.loaded("isolated", 4, caB, caB, der)
	e.accept("isolated", 4)
	isolated, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leaf, ref := e.a.For("isolated", 4, "example.com")
	if leaf == nil {
		t.Fatal(ref)
	}
	verify(t, isolated, leaf.Leaf, "example.com", e.clock())
	roots := x509.NewCertPool()
	roots.AddCert(work)
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example.com", CurrentTime: e.clock()}); err == nil {
		t.Error("a leaf of VM isolated verifies against VM work's CA")
	}
	e.wantRefusal("work", 4, "example.com", RuleLeafRefused, ReasonGeneration)
}

// BenchmarkLeaf signs leaves with the fake key, so the Secure Enclave
// can be compared with it (M3.9d).
func BenchmarkLeaf(b *testing.B) {
	e := newEnv(b, team)
	e.ready()
	ca := e.a.vms["work"].cas[caA]
	now := e.clock()
	for b.Loop() {
		if _, err := signLeaf(ca, "example.com", now); err != nil {
			b.Fatal(err)
		}
	}
}
