package ca

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// vmState is what wb-proxyd holds of a VM from its last CaSet.
type vmState struct {
	generation uint64
	// accepted is set by a CaAccept of generation: only then does the VM
	// get leaves (S09-07-ca-certificates, "Boot, restore and host
	// wake", step 7).
	accepted bool
	// cas are the VM's restored CAs, by CA id. None when failed is set.
	cas map[string]*caCert
	// signing is the CA id that signs, or "" when the CaSet said
	// ca-not-installed.
	signing string
	// failed is the rule of a CaSet whose CAs didn't all restore, or "".
	// Then the VM gets no leaf (S09-06-ca-keys, "Restore").
	failed string
	leaves *leafCache
}

// caCert is one restored CA.
type caCert struct {
	id   string
	cert *x509.Certificate
	key  *heldKey
}

// idOrNone returns the CA id, or "" for no CA, for a log line.
func (c *caCert) idOrNone() string {
	if c == nil {
		return ""
	}
	return c.id
}

// restoreError is a CA of a CaSet that didn't restore.
type restoreError struct {
	id  string
	err error
}

func (e *restoreError) Error() string { return "CA " + e.id + ": " + e.err.Error() }
func (e *restoreError) Unwrap() error { return e.err }

// SetCAs takes a VM's CAs from wb-hostd's record (S09-06-ca-keys, "Valid
// CA"). It restores each CA it doesn't hold yet from its item, and
// checks that the certificate has the CA profile, is self-signed, and
// has the restored key's public key. It returns a CaLoaded, or a
// CaRestoreFailed for the first CA that failed, and then the VM gets no
// leaf. wb-proxyd never issues a CA in place of one that failed.
//
// A CaSet of a newer generation stops the VM's leaves until a CaAccept
// of that generation. A CaSet of an older generation is an error that
// wraps ErrOutOfOrder.
func (a *Authority) SetCAs(ctx context.Context, m *egressv1.CaSet) (*egressv1.ProxydToHostd, error) {
	a.ctl.Lock()
	defer a.ctl.Unlock()
	vm, gen := m.GetVm(), m.GetGeneration()
	if last := a.generation(vm); gen < last {
		a.logCA(slog.LevelError, "CA set refused", vm, "", "", slog.Uint64("generation", gen),
			slog.Uint64("last_generation", last), slog.String("reason", "a CA set of an older generation"))
		return nil, fmt.Errorf("%w: CA set of generation %d for VM %s after generation %d", ErrOutOfOrder, gen, vm, last)
	}
	cas, err := a.restoreAll(ctx, vm, m.GetCas())
	if err != nil {
		var re *restoreError
		errors.As(err, &re)
		rule := failureRule(err)
		a.commit(vm, &vmState{generation: gen, failed: rule})
		a.logCA(slog.LevelError, "CA restore failed, the VM gets no streams", vm, re.id, rule,
			slog.Uint64("generation", gen), slog.String("reason", err.Error()))
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRestoreFailed{CaRestoreFailed: &egressv1.CaRestoreFailed{
			Vm: vm, Generation: gen, CaId: re.id, Rule: rule,
		}}}, nil
	}
	a.commit(vm, &vmState{generation: gen, cas: cas, signing: m.GetSigningCaId()})
	for _, ca := range cas {
		a.logCA(slog.LevelInfo, "CA loaded", vm, ca.id, "", sha256Attr(ca.cert.Raw), slog.Uint64("generation", gen),
			slog.Bool("signing", ca.id == m.GetSigningCaId()))
	}
	if m.GetSigningCaId() == "" {
		a.logCA(slog.LevelWarn, "VM has no signing CA", vm, "", m.GetNoSigningRule(), slog.Uint64("generation", gen))
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{
		Vm: vm, Generation: gen,
	}}}, nil
}

// generation returns the VM's last generation, or 0.
func (a *Authority) generation(vm string) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.vms[vm]; st != nil {
		return st.generation
	}
	return 0
}

// restoreAll restores each CA of a CaSet, or returns a *restoreError for
// the first that failed. A key it restored is closed again on a failure.
func (a *Authority) restoreAll(ctx context.Context, vm string, set []*egressv1.InspectionCa) (map[string]*caCert, error) {
	cas := make(map[string]*caCert, len(set))
	var fresh []*heldKey
	for _, in := range set {
		ca, isNew, err := a.restoreOne(ctx, vm, in)
		if err != nil {
			for _, k := range fresh {
				k.close()
			}
			return nil, &restoreError{id: in.GetCaId(), err: err}
		}
		if isNew {
			fresh = append(fresh, ca.key)
		}
		cas[ca.id] = ca
	}
	return cas, nil
}

// restoreOne checks a CA certificate and restores its key, unless this
// process holds it already, and reports whether it restored it now.
func (a *Authority) restoreOne(ctx context.Context, vm string, in *egressv1.InspectionCa) (*caCert, bool, error) {
	id := in.GetCaId()
	item, err := itemName(vm, id)
	if err != nil {
		return nil, false, err
	}
	cert, err := checkCACert(in.GetCertificate())
	if err != nil {
		return nil, false, err
	}
	a.mu.Lock()
	held := a.held[item.String()]
	a.mu.Unlock()
	isNew := held == nil
	if isNew {
		wrapped, err := a.fetch(ctx, item)
		if err != nil {
			return nil, false, fmt.Errorf("fetch %s: %w", item, err)
		}
		key, err := a.keys.Restore(ctx, wrapped)
		clear(wrapped)
		if err != nil {
			return nil, false, fmt.Errorf("restore %s: %w", item, err)
		}
		held = &heldKey{key: key}
	}
	if !samePublicKey(held.key, cert) {
		if isNew {
			held.close()
		}
		return nil, false, fmt.Errorf("the key of %s differs from the CA certificate's public key", item)
	}
	return &caCert{id: id, cert: cert, key: held}, isNew, nil
}

// checkCACert parses a CA certificate of a CaSet and checks it is a CA
// that signed itself. Its public key is the restore's check.
func checkCACert(der []byte) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("the CA certificate doesn't parse: %w", err)
	}
	if !cert.BasicConstraintsValid || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("the CA certificate isn't a CA that signs certificates")
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		return nil, fmt.Errorf("the CA certificate isn't self-signed: %w", err)
	}
	return cert, nil
}

// commit makes st the VM's state. The VM stays accepted only within one
// generation, and keeps its leaves only while the signing CA stays the
// same. It drops and closes each held key of the VM that st doesn't
// name.
func (a *Authority) commit(vm string, st *vmState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.vms[vm]
	if old != nil && old.generation == st.generation {
		st.accepted = old.accepted
	}
	if old != nil && old.generation == st.generation && sameSigner(old, st) {
		st.leaves = old.leaves
	} else {
		st.leaves = newLeafCache(st.signing)
	}
	a.vms[vm] = st
	for _, ca := range st.cas {
		item, _ := itemName(vm, ca.id)
		a.held[item.String()] = ca.key
	}
	a.dropHeld(vm, st)
}

// sameSigner reports whether two states of a VM sign with the same CA
// certificate.
func sameSigner(old, st *vmState) bool {
	if old.signing != st.signing {
		return false
	}
	before, now := old.cas[old.signing], st.cas[st.signing]
	return before != nil && now != nil && bytes.Equal(before.cert.Raw, now.cert.Raw)
}

// dropHeld closes and forgets each held key of vm that st doesn't name.
// The caller holds mu.
func (a *Authority) dropHeld(vm string, st *vmState) {
	for _, id := range a.heldOf(vm) {
		if _, ok := st.cas[id]; ok {
			continue
		}
		item, _ := itemName(vm, id)
		a.held[item.String()].close()
		delete(a.held, item.String())
	}
}

// heldOf returns the CA ids of vm's held keys. The caller holds mu.
func (a *Authority) heldOf(vm string) []string {
	prefix := storeitem.Prefix + "ca/" + vm + "/"
	var ids []string
	for name := range a.held {
		if id, ok := strings.CutPrefix(name, prefix); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// Accept lets the VM's leaves of generation be signed, once wb-hostd
// recorded the install of its CAs (S09-07-ca-certificates, "Boot,
// restore and host wake", step 7). A CaAccept of another generation than
// the VM's last CaSet is an error that wraps ErrOutOfOrder.
func (a *Authority) Accept(m *egressv1.CaAccept) error {
	a.ctl.Lock()
	defer a.ctl.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	vm, gen := m.GetVm(), m.GetGeneration()
	st := a.vms[vm]
	if st == nil || st.generation != gen {
		var last uint64
		if st != nil {
			last = st.generation
		}
		a.logCA(slog.LevelError, "CA accept refused", vm, "", "", slog.Uint64("generation", gen),
			slog.Uint64("last_generation", last), slog.String("reason", "not the generation of the VM's last CA set"))
		return fmt.Errorf("%w: CA accept of generation %d for VM %s, whose last CA set is of generation %d", ErrOutOfOrder, gen, vm, last)
	}
	st.accepted = true
	a.logCA(slog.LevelInfo, "VM's streams accepted", vm, st.signing, st.failed, slog.Uint64("generation", gen))
	return nil
}
