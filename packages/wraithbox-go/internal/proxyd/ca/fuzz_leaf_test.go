package ca

import (
	"crypto/x509"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// FuzzLeafName feeds any hostname to For over the fakes, as the SNI of a
// guest's stream reaches it. It never panics, and a refusal has a rule.
// A leaf it signs has exactly one dNSName, equal to the input, which is
// neither a wildcard nor an IP address, and no other name. It is
// CA:FALSE, signed by the VM's CA, and valid within the CA's validity
// (S09-07-ca-certificates, "Leaf profile").
func FuzzLeafName(f *testing.F) {
	for _, s := range []string{
		"example.com", "api.example.com", "*.example.com", "192.0.2.1", "[::1]",
		"Example.com", "example.com.", "", "xn--bcher-kva.example", "a..b", "-a.com", "a\x00.com",
		strings.Repeat("a", 63) + ".com",
	} {
		f.Add(s)
	}
	e := newEnv(f, team)
	ca := e.ready()
	f.Fuzz(func(t *testing.T, name string) {
		leaf, ref := e.a.For("work", 1, name)
		if leaf == nil {
			if ref == nil || ref.Rule == "" {
				t.Fatalf("For(%q) gave neither a leaf nor a refusal with a rule", name)
			}
			return
		}
		requireLeafProfile(t, ca, leaf.Leaf, name)
	})
}

// requireLeafProfile fails the fuzz run unless cert is a leaf of ca for
// name alone.
func requireLeafProfile(t *testing.T, ca, cert *x509.Certificate, name string) {
	t.Helper()
	if !slices.Equal(cert.DNSNames, []string{name}) || len(cert.IPAddresses)+len(cert.EmailAddresses)+len(cert.URIs) != 0 {
		t.Fatalf("leaf for %q has names %v %v %v %v", name, cert.DNSNames, cert.IPAddresses, cert.EmailAddresses, cert.URIs)
	}
	if strings.Contains(name, "*") {
		t.Fatalf("a leaf was signed for the wildcard %q", name)
	}
	if _, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil {
		t.Fatalf("a leaf was signed for the IP address %q", name)
	}
	if !cert.BasicConstraintsValid || cert.IsCA {
		t.Fatalf("leaf for %q isn't CA:FALSE", name)
	}
	if err := cert.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("leaf for %q isn't signed by the VM's CA: %v", name, err)
	}
	if cert.NotBefore.Before(ca.NotBefore) || cert.NotAfter.After(ca.NotAfter) {
		t.Fatalf("leaf for %q is valid %v to %v, outside the CA's %v to %v", name, cert.NotBefore, cert.NotAfter, ca.NotBefore, ca.NotAfter)
	}
}
