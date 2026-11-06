package ca

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The CA's lifetime (S09-07-ca-certificates, "Lifetimes"), and how far
// back a certificate's NotBefore goes, for a guest clock that runs a
// little behind the host's.
const (
	caLifetime = 60 * 24 * time.Hour
	backdate   = 5 * time.Minute
)

// Issue issues a new CA for a VM (S09-06-ca-keys, "Issue"): it makes
// the key, self-signs the CA certificate, writes the key's wrapped form
// as the item `ca/<vm>/<ca>`, fetches the item back, restores the key
// from it and compares the public keys. A failed step deletes the item
// and fails the issue with ca-key-restore, or keychain-needs-team in an
// ad-hoc signed build. wb-proxyd keeps the restored key, so the CaSet
// that names the new CA doesn't restore it again.
func (a *Authority) Issue(ctx context.Context, m *egressv1.CaIssue) *egressv1.CaIssued {
	a.ctl.Lock()
	defer a.ctl.Unlock()
	vm, id := m.GetVm(), m.GetCaId()
	out := &egressv1.CaIssued{Vm: vm, CaId: id}
	item, err := itemName(vm, id)
	if err != nil {
		out.Rule = RuleKeyRestore
		a.logCA(slog.LevelError, "CA issue failed", vm, id, out.Rule, slog.String("reason", err.Error()))
		return out
	}
	if reason := a.issued(vm, id, item); reason != "" {
		out.Rule = RuleIssueRefused
		a.logCA(slog.LevelWarn, "CA issue refused", vm, id, out.Rule, slog.String("reason", reason))
		return out
	}
	der, held, err := a.issue(ctx, vm, id, item)
	if err != nil {
		out.Rule = failureRule(err)
		a.logCA(slog.LevelError, "CA issue failed", vm, id, out.Rule, slog.String("reason", err.Error()))
		return out
	}
	a.mu.Lock()
	a.held[item.String()] = held
	a.mu.Unlock()
	out.Certificate = der
	a.logCA(slog.LevelInfo, "CA issued", vm, id, "", sha256Attr(der), slog.String("item", item.String()))
	return out
}

// issued returns why a CA id may not be issued, or "": the item is held
// already, or the id is a CA of some VM's last CaSet. The caller holds
// ctl.
func (a *Authority) issued(vm, id string, item storeitem.Name) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.held[item.String()]; ok {
		return "wb-proxyd holds the key of " + item.String() + " already"
	}
	for other, st := range a.vms {
		if _, ok := st.cas[id]; ok {
			return "the CA id is a CA of VM " + other + "'s last CA set, not a new one for " + vm
		}
	}
	return ""
}

// issue runs the steps of an issue and returns the CA certificate and
// the restored key. Once the write was tried, a failed step deletes the
// item.
func (a *Authority) issue(ctx context.Context, vm, id string, item storeitem.Name) ([]byte, *heldKey, error) {
	gen, err := a.keys.Generate(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("generate the key: %w", err)
	}
	defer gen.Close()
	der, err := selfSign(gen, vm, id, a.clock())
	if err != nil {
		return nil, nil, err
	}
	restored, err := a.roundTrip(ctx, item, gen, der)
	if err != nil {
		a.deleteFailed(ctx, vm, id, item)
		return nil, nil, err
	}
	return der, &heldKey{key: restored}, nil
}

// roundTrip writes the wrapped form of gen as item, fetches it back,
// restores the key from what it fetched, and compares the restored
// public key with gen's and the certificate's.
func (a *Authority) roundTrip(ctx context.Context, item storeitem.Name, gen hwkey.Key, der []byte) (hwkey.Key, error) {
	if err := a.write(ctx, item, gen.Wrapped()); err != nil {
		return nil, fmt.Errorf("write the item: %w", err)
	}
	wrapped, err := a.fetch(ctx, item)
	if err != nil {
		return nil, fmt.Errorf("fetch the item back: %w", err)
	}
	restored, err := a.keys.Restore(ctx, wrapped)
	clear(wrapped)
	if err != nil {
		return nil, fmt.Errorf("restore the key: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		restored.Close()
		return nil, fmt.Errorf("parse the CA certificate: %w", err)
	}
	if !samePublicKey(restored, cert) || !samePublicKey(gen, cert) {
		restored.Close()
		return nil, errors.New("the restored key's public key differs from the generated key's")
	}
	return restored, nil
}

// deleteFailed deletes the item of a failed issue. A failed delete is
// logged with store-write-failed: the item then holds a key no record
// names, and no CA certificate trusts.
func (a *Authority) deleteFailed(ctx context.Context, vm, id string, item storeitem.Name) {
	if err := a.remove(ctx, item); err != nil {
		a.logCA(slog.LevelError, "CA item of a failed issue not deleted", vm, id,
			string(storeitem.RuleWriteFailed), slog.String("item", item.String()), slog.String("reason", err.Error()))
		return
	}
	a.logCA(slog.LevelInfo, "CA item of a failed issue deleted", vm, id, "", slog.String("item", item.String()))
}

// selfSign makes the CA certificate of the CA profile
// (S09-07-ca-certificates, "CA profile" and "Scope"): basicConstraints
// critical with CA:TRUE and pathLen:0, keyUsage keyCertSign only, no
// extended key usage and no name constraints, a 128-bit random serial,
// and 60 days of validity from now.
func selfSign(key hwkey.Key, vm, id string, now time.Time) ([]byte, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	name := pkix.Name{CommonName: fmt.Sprintf("Wraith Box inspection CA %s %s", vm, id[:8])}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               name,
		Issuer:                name,
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("self-sign the CA certificate: %w", err)
	}
	if len(der) > egressmsg.MaxCACert {
		return nil, fmt.Errorf("the CA certificate is %d bytes, the message takes %d", len(der), egressmsg.MaxCACert)
	}
	return der, nil
}

// randomSerial returns a serial number of 128 random bits.
func randomSerial() (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("serial number: %w", err)
	}
	serial := new(big.Int).SetBytes(b)
	if serial.Sign() == 0 {
		return nil, errors.New("serial number: the random source gave 0")
	}
	return serial, nil
}
