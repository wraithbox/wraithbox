package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// The leaf's lifetime, and how long before its end a cached leaf is
// replaced (S09-07-ca-certificates, "Lifetimes").
const (
	leafLifetime = 24 * time.Hour
	leafRenew    = time.Hour
)

// maxLeavesPerVM bounds a VM's leaf cache. The guest picks the
// hostnames, so past the bound the least recently used leaf is dropped
// (SEC13-bounded-resources).
const maxLeavesPerVM = 1024

// maxNameLog bounds a hostname in a log line. A DNS name has at most
// 253 characters.
const maxNameLog = 253

// The reasons of a ca-leaf-refused (S09-07-ca-certificates, "Leaf
// issuance").
const (
	ReasonNotDNSName     = "not a DNS name in canonical form"
	ReasonWildcard       = "a wildcard name"
	ReasonIPAddress      = "an IP address"
	ReasonGeneration     = "not the generation of the VM's last CA set"
	ReasonNotAccepted    = "the VM's CAs of this generation aren't accepted yet"
	ReasonNoSigningCA    = "the VM has no signing CA"
	ReasonExpiredCA      = "the signing CA has expired or isn't valid yet"
	ReasonSignatureError = "the CA key didn't sign the leaf"
)

// Refusal is a leaf For refused: the rule, and why.
type Refusal struct {
	Rule   string
	Reason string
}

func (r *Refusal) Error() string { return r.Rule + ": " + r.Reason }

// For returns the leaf for an inspected stream of vm and generation to
// the hostname name, signed by the VM's signing CA, or a refusal
// (S09-07-ca-certificates, "Leaf issuance"):
//
//   - ca-leaf-refused for a name that isn't a DNS name in canonical form,
//     is a wildcard or is an IP address, for another generation than the
//     VM's last CA set, before the CaAccept, with no signing CA, or with
//     a signing CA that has expired on the proxy's clock;
//   - ca-not-installed when the CA set said no CA signs;
//   - the rule of the CA set's failed restore.
//
// A leaf is reused from the cache until an hour before its end.
//
// For doesn't read the policy. The caller passes only a stream that
// passed the SNI check and whose VM's effective policy inspects the
// host (S09-07-ca-certificates, "Leaf issuance"). A pass-mode stream
// never reaches For.
func (a *Authority) For(vm string, generation uint64, name string) (*tls.Certificate, *Refusal) {
	if reason := checkLeafName(name); reason != "" {
		return nil, a.refuseLeaf(vm, generation, name, "", &Refusal{Rule: RuleLeafRefused, Reason: reason})
	}
	now := a.clock()
	ca, cached, ref := a.signer(vm, generation, name, now)
	if ref != nil {
		return nil, a.refuseLeaf(vm, generation, name, ca.idOrNone(), ref)
	}
	if cached != nil {
		return cached, nil
	}
	leaf, err := signLeaf(ca, name, now)
	if err != nil {
		return nil, a.refuseLeaf(vm, generation, name, ca.id, &Refusal{
			Rule: RuleLeafRefused, Reason: ReasonSignatureError + ": " + err.Error(),
		})
	}
	a.cacheLeaf(vm, generation, ca.id, name, leaf, now)
	a.log.LogAttrs(context.Background(), slog.LevelDebug, "leaf signed",
		slog.String("vm", vm), slog.Uint64("generation", generation), slog.String("ca_id", ca.id),
		slog.String("host", name), slog.String("sha256", fingerprint(leaf.Certificate[0])))
	return leaf, nil
}

// checkLeafName returns why a leaf for name is refused, or "". A leaf
// has exactly one dNSName, which is neither a wildcard nor an IP
// address (S09-07-ca-certificates, "Leaf profile").
func checkLeafName(name string) string {
	if strings.Contains(name, "*") {
		return ReasonWildcard
	}
	if _, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil || strings.Contains(name, ":") {
		return ReasonIPAddress
	}
	n, ref := dnsname.FromHost(name)
	if ref != nil || n.String() != name {
		return ReasonNotDNSName
	}
	return ""
}

// signer returns the VM's signing CA and the cached leaf for name, if
// there is one, or the refusal, with the CA when it has expired. A
// valid CA is in the VM's last CA set,
// restored, and not expired on the proxy's clock (S09-06-ca-keys,
// "Valid CA").
func (a *Authority) signer(vm string, generation uint64, name string, now time.Time) (*caCert, *tls.Certificate, *Refusal) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.vms[vm]
	switch {
	case st == nil:
		return nil, nil, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNoSigningCA}
	case st.generation != generation:
		return nil, nil, &Refusal{Rule: RuleLeafRefused, Reason: ReasonGeneration}
	case st.failed != "":
		return nil, nil, &Refusal{Rule: st.failed, Reason: "the VM's CAs didn't restore"}
	case !st.accepted:
		return nil, nil, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNotAccepted}
	case st.signing == "":
		return nil, nil, &Refusal{Rule: RuleNotInstalled, Reason: ReasonNoSigningCA}
	}
	ca := st.cas[st.signing]
	if ca == nil {
		// egressmsg refuses a CaSet whose signing CA isn't one of its
		// CAs, so this is a bug, and it fails closed.
		return nil, nil, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNoSigningCA}
	}
	if now.After(ca.cert.NotAfter) || now.Before(ca.cert.NotBefore) {
		return ca, nil, &Refusal{Rule: RuleLeafRefused, Reason: ReasonExpiredCA}
	}
	return ca, st.leaves.get(name, now), nil
}

// refuseLeaf logs a refused leaf and returns the refusal.
func (a *Authority) refuseLeaf(vm string, generation uint64, name, id string, ref *Refusal) *Refusal {
	a.log.LogAttrs(context.Background(), slog.LevelWarn, "leaf refused",
		slog.String("vm", guesttext.LogField(vm, maxNameLog)), slog.Uint64("generation", generation),
		slog.String("ca_id", id), slog.String("host", guesttext.LogField(name, maxNameLog)),
		slog.String("rule", ref.Rule), slog.String("reason", ref.Reason), slog.String("decision", "refuse"))
	return ref
}

// cacheLeaf adds a leaf to the VM's cache, unless the VM's state moved
// on while it was signed.
func (a *Authority) cacheLeaf(vm string, generation uint64, id, name string, leaf *tls.Certificate, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.vms[vm]
	if st == nil || st.generation != generation || st.signing != id || st.leaves.caID != id {
		return
	}
	st.leaves.put(name, leaf, now)
}

// signLeaf signs a leaf of the leaf profile (S09-07-ca-certificates,
// "Leaf profile" and "Lifetimes"): the one dNSName name, CA:FALSE,
// extended key usage serverAuth, a new P-256 key, a 128-bit random
// serial, and a validity from 5 minutes back to 24 hours ahead, cut to
// the CA's.
func signLeaf(ca *caCert, name string, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             later(now.Add(-backdate), ca.cert.NotBefore),
		NotAfter:              earlier(now.Add(leafLifetime), ca.cert.NotAfter),
		DNSNames:              []string{name},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("sign the leaf: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse the leaf: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}, nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
