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
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
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
	ReasonNotInspected   = "the VM's policy doesn't inspect the host and port"
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

// LeafRequest is the inspected stream a leaf is asked for.
type LeafRequest struct {
	VM         string
	Generation uint64
	// Name is the stream's hostname, which its SNI matched.
	Name string
	Port uint16
	// Policy is the VM's effective policy now. For signs only when it
	// inspects Name and Port.
	Policy *policy.Policy
}

// For returns the leaf for an inspected stream to the hostname
// req.Name, signed by the VM's signing CA, or a refusal
// (S09-07-ca-certificates, "Leaf issuance"):
//
//   - ca-leaf-refused for a name that isn't a DNS name in canonical form,
//     is a wildcard or is an IP address, for a host and port the VM's
//     policy doesn't inspect (a pass host's included), for another
//     generation than the VM's last CA set, before the CaAccept, with no
//     signing CA, or with a signing CA that has expired on the proxy's
//     clock;
//   - ca-not-installed when the CA set said no CA signs;
//   - the rule of the CA set's failed restore.
//
// A leaf is reused from the cache until an hour before its end. Calls
// for one leaf that isn't cached wait for the first one's signature, so
// it is signed once.
//
// Leaf signing is bounded by the stream admission caps
// (SEC13-bounded-resources). The caller asks for a leaf once per
// stream, after the stream's first-byte and SNI checks, so a VM has at
// most its hand-off socket's stream cap (handoff.Caps.Streams) of For
// calls at once. A new signature needs a name the cache doesn't hold:
// one the VM's policy names, or one under a wildcard, which the
// wildcard budget bounds (S07-03-dns, "Wildcard budget").
func (a *Authority) For(req LeafRequest) (*tls.Certificate, *Refusal) {
	vm, generation, name := req.VM, req.Generation, req.Name
	if reason := checkLeafName(name); reason != "" {
		return nil, a.refuseLeaf(req, "", &Refusal{Rule: RuleLeafRefused, Reason: reason})
	}
	if !inspected(req) {
		return nil, a.refuseLeaf(req, "", &Refusal{Rule: RuleLeafRefused, Reason: ReasonNotInspected})
	}
	now := a.clock()
	l, ref := a.signer(vm, generation, name, now)
	switch {
	case ref != nil:
		return nil, a.refuseLeaf(req, l.ca.idOrNone(), ref)
	case l.cached != nil:
		return l.cached, nil
	case !l.lead:
		<-l.flight.done
		return l.flight.leaf, l.flight.ref
	}
	leaf, err := signLeaf(l.ca, name, now)
	if err != nil {
		ref = a.refuseLeaf(req, l.ca.id, &Refusal{
			Rule: RuleLeafRefused, Reason: ReasonSignatureError + ": " + err.Error(),
		})
		leaf = nil
	} else {
		a.log.LogAttrs(context.Background(), slog.LevelDebug, "leaf signed",
			slog.String("vm", vm), slog.Uint64("generation", generation), slog.String("ca_id", l.ca.id),
			slog.String("host", name), slog.String("sha256", fingerprint(leaf.Certificate[0])))
	}
	a.land(vm, generation, l, leaf, ref, now)
	return leaf, ref
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

// inspected reports whether the request's policy inspects its host and
// port, so a pass host, a plain HTTP or raw TCP port, or a port the
// policy doesn't name gets no leaf (S09-07-ca-certificates, "Leaf
// issuance"). checkLeafName passed already.
func inspected(req LeafRequest) bool {
	n, ref := dnsname.FromHost(req.Name)
	return ref == nil && req.Policy.InspectsTLS(n, req.Port)
}

// lookup is what signer found for a leaf that isn't refused: the
// signing CA, and the cached leaf, or the signature in flight.
type lookup struct {
	ca     *caCert
	cached *tls.Certificate
	// flight is the signature of the leaf, which this call makes when
	// lead is set, and waits for otherwise.
	flight *flight
	lead   bool
}

// signer returns the VM's signing CA with the cached leaf for name, or
// the flight that signs it, or the refusal, with the CA when it has
// expired. A valid CA is in the VM's last CA set, restored, and not
// expired on the proxy's clock (S09-06-ca-keys, "Valid CA").
func (a *Authority) signer(vm string, generation uint64, name string, now time.Time) (lookup, *Refusal) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.vms[vm]
	switch {
	case st == nil:
		return lookup{}, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNoSigningCA}
	case st.generation != generation:
		return lookup{}, &Refusal{Rule: RuleLeafRefused, Reason: ReasonGeneration}
	case st.failed != "":
		return lookup{}, &Refusal{Rule: st.failed, Reason: "the VM's CAs didn't restore"}
	case !st.accepted:
		return lookup{}, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNotAccepted}
	case st.signing == "":
		return lookup{}, &Refusal{Rule: RuleNotInstalled, Reason: ReasonNoSigningCA}
	}
	ca := st.cas[st.signing]
	if ca == nil {
		// egressmsg refuses a CaSet whose signing CA isn't one of its
		// CAs, so this is a bug, and it fails closed.
		return lookup{}, &Refusal{Rule: RuleLeafRefused, Reason: ReasonNoSigningCA}
	}
	if now.After(ca.cert.NotAfter) || now.Before(ca.cert.NotBefore) {
		return lookup{ca: ca}, &Refusal{Rule: RuleLeafRefused, Reason: ReasonExpiredCA}
	}
	if leaf := st.leaves.get(name, now); leaf != nil {
		return lookup{ca: ca, cached: leaf}, nil
	}
	f, lead := st.leaves.join(name)
	return lookup{ca: ca, flight: f, lead: lead}, nil
}

// refuseLeaf logs a refused leaf and returns the refusal.
func (a *Authority) refuseLeaf(req LeafRequest, id string, ref *Refusal) *Refusal {
	a.log.LogAttrs(context.Background(), slog.LevelWarn, "leaf refused",
		slog.String("vm", guesttext.LogField(req.VM, maxNameLog)), slog.Uint64("generation", req.Generation),
		slog.String("ca_id", id), slog.String("host", guesttext.LogField(req.Name, maxNameLog)),
		slog.String("rule", ref.Rule), slog.String("reason", ref.Reason), slog.String("decision", "refuse"),
		slog.Int("port", int(req.Port)))
	return ref
}

// land ends a flight this call led: it caches the leaf, unless the VM's
// state moved on while it was signed, and hands the result to the calls
// that wait for it.
func (a *Authority) land(vm string, generation uint64, l lookup, leaf *tls.Certificate, ref *Refusal, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l.flight.from.leave(l.flight)
	l.flight.leaf, l.flight.ref = leaf, ref
	close(l.flight.done)
	st := a.vms[vm]
	if leaf == nil || st == nil || st.generation != generation || st.signing != l.ca.id || st.leaves != l.flight.from {
		return
	}
	st.leaves.put(l.flight.name, leaf, now)
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
