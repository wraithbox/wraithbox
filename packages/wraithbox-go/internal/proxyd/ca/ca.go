// Package ca is wb-proxyd's inspection CA (S09-06-ca-keys,
// S09-07-ca-certificates). It issues a VM's CA with a key from the
// hardware key store, keeps the key's wrapped form as the store item
// `ca/<vm>/<ca>`, restores the CAs wb-hostd's record names, signs a
// leaf for an inspected stream with the VM's signing CA, and deletes
// the items of the removal list (S15-03-access-rules, "Removing CA
// keys").
//
// An Authority holds no private key bytes. Each key is an hwkey.Key,
// which signs and gives its wrapped form, and the leaf keys, which are
// software keys, stay in the leaf cache in memory.
//
// The package exposes no x509.CertPool and no way to add a CA to one:
// a Wraith Box CA is trusted only inside guests, never by wb-proxyd's
// upstream connections (S09-07-ca-certificates, "Not on the host").
package ca

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The rules of the CA's refusals and failures.
const (
	// RuleKeyRestore fails an issue or a restore whose round trip
	// through the store and the key store failed (S09-06-ca-keys,
	// "Valid CA").
	RuleKeyRestore = "ca-key-restore"
	// RuleNeedsTeam takes the place of RuleKeyRestore when the store
	// refused the call because the build is ad-hoc signed
	// (S09-06-ca-keys, "Ad-hoc signed builds").
	RuleNeedsTeam = string(storeitem.RuleNeedsTeam)
	// RuleIssueRefused refuses a CaIssue for a CA id that wb-proxyd
	// holds already, before any store call, so a repeated id can't
	// replace or delete a live CA's item (S09-06-ca-keys, "Issue").
	RuleIssueRefused = "ca-issue-refused"
	// RuleNotInstalled refuses the leaves of a VM whose CaSet names no
	// signing CA (S09-07-ca-certificates, "Rotation", day 58).
	RuleNotInstalled = egressmsg.RuleCANotInstalled
	// RuleLeafRefused refuses a leaf the stream may not have
	// (S09-07-ca-certificates, "Leaf issuance").
	RuleLeafRefused = "ca-leaf-refused"
	// RuleRemovalRefused refuses a whole removal batch
	// (S15-03-access-rules, "Removing CA keys").
	RuleRemovalRefused = "ca-removal-refused"
)

// defaultUnlockPoll is how often a call that found the store locked
// tries again, when Config.UnlockPoll is 0.
const defaultUnlockPoll = 2 * time.Second

// Config is what an Authority uses.
type Config struct {
	// Store is wb-proxyd's secret store, opened for wb-proxyd.
	Store secretstore.Store
	// Keys is the hardware key store.
	Keys hwkey.Provider
	// Clock is the proxy's clock. Nil is time.Now.
	Clock func() time.Time
	// Log takes every decision. Nil discards them.
	Log *slog.Logger
	// UnlockPoll is how often a call that found the store locked tries
	// again. 0 is two seconds.
	UnlockPoll time.Duration
}

// Authority is wb-proxyd's inspection CA. Its methods are safe for
// concurrent use. The control messages are handled one at a time, and
// For runs next to them.
type Authority struct {
	store      secretstore.Store
	keys       hwkey.Provider
	clock      func() time.Time
	log        *slog.Logger
	unlockPoll time.Duration

	// ctl serializes the control messages, which make store calls and
	// may wait for an unlock. It is never held by For.
	ctl sync.Mutex

	// mu guards vms and held. Nothing slow runs under it: no store
	// call, no restore and no signature.
	mu  sync.Mutex
	vms map[string]*vmState
	// held is every CA key restored in this process, by item name.
	held map[string]*heldKey
}

// New returns an Authority with no VM.
func New(cfg Config) *Authority {
	a := &Authority{
		store:      cfg.Store,
		keys:       cfg.Keys,
		clock:      cfg.Clock,
		log:        cfg.Log,
		unlockPoll: cfg.UnlockPoll,
		vms:        map[string]*vmState{},
		held:       map[string]*heldKey{},
	}
	if a.clock == nil {
		a.clock = time.Now
	}
	if a.log == nil {
		a.log = slog.New(slog.DiscardHandler)
	}
	if a.unlockPoll <= 0 {
		a.unlockPoll = defaultUnlockPoll
	}
	return a
}

// ErrOutOfOrder is wrapped by the error of a CaSet of an older
// generation than the VM's last one, and of a CaAccept of another
// generation than it. The caller ends the control channel.
var ErrOutOfOrder = errors.New("ca: control message out of order")

// Handle runs one CA message of the control channel, which
// egressmsg.DecodeHostdToProxyd accepted, and returns the reply, or nil
// for a CaAccept. An error means the message is out of order, or isn't
// a CA message, and the caller ends the channel.
func (a *Authority) Handle(ctx context.Context, m *egressv1.HostdToProxyd) (*egressv1.ProxydToHostd, error) {
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue:
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: a.Issue(ctx, k.CaIssue)}}, nil
	case *egressv1.HostdToProxyd_CaSet:
		return a.SetCAs(ctx, k.CaSet)
	case *egressv1.HostdToProxyd_CaAccept:
		return nil, a.Accept(k.CaAccept)
	case *egressv1.HostdToProxyd_CaRemove:
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: a.Remove(ctx, k.CaRemove)}}, nil
	default:
		return nil, errors.New("ca: not a CA message")
	}
}

// itemName returns the store item of a VM's CA. vm and id were checked
// by egressmsg, so the name parses, and an error is a bug that fails
// the call closed.
func itemName(vm, id string) (storeitem.Name, error) {
	return storeitem.Parse(storeitem.Prefix + "ca/" + vm + "/" + id)
}

// fingerprint is the SHA-256 fingerprint of a certificate, as its logs
// name it (S09-07-ca-certificates, "Audit").
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// samePublicKey reports whether a key's public key equals a
// certificate's.
func samePublicKey(k hwkey.Key, cert *x509.Certificate) bool {
	pub, ok := k.Public().(interface{ Equal(x crypto.PublicKey) bool })
	return ok && pub.Equal(cert.PublicKey)
}

// sha256Attr is the log attribute of a certificate's fingerprint.
func sha256Attr(der []byte) slog.Attr { return slog.String("sha256", fingerprint(der)) }

// logCA logs a decision about a VM's CA, with its rule, if there is one.
// A decision about a certificate adds sha256Attr.
func (a *Authority) logCA(level slog.Level, msg, vm, id, rule string, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+3)
	all = append(all, slog.String("vm", vm), slog.String("ca_id", id))
	if rule != "" {
		all = append(all, slog.String("rule", rule))
	}
	a.log.LogAttrs(context.Background(), level, msg, append(all, attrs...)...)
}
