// Package carecord is wb-hostd's side of the TLS inspection CAs: each
// VM's CA record in state.db, the rotation schedule, the order a boot,
// a restore and a host wake run their steps in, the removal list of key
// items, and `wb vm reset-ca` (S09-07-ca-certificates, "Lifetimes";
// S09-06-ca-keys; S15-03-access-rules, "Removing CA keys").
//
// wb-hostd decides every step, and wb-proxyd does what a control
// message asks: issue a CA, load the VM's set, accept its streams, and
// delete key items. wb-hostd checks each reply as a record from a
// process that faces the guest: a reply that breaks its contract ends
// wb-proxyd through the Ender, as a malformed record does
// (S04-08-records-and-bounds). The guest only installs what the record
// holds, and wb-hostd never reads trust from it.
//
// wb-hostd doesn't import internal/platform/secretstore, so nothing here
// touches a key. The 3004 records of wb-proxyd's store accesses are
// written from its replies (S15-03-access-rules, rule 6).
package carecord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The rules of the CA record's decisions. wb-proxyd's rules, such as
// ca-key-restore, are egressmsg's.
const (
	// RuleReplyInvalid: a CA reply of wb-proxyd broke the contract of
	// control.proto, didn't answer the message sent, or held a
	// certificate without the CA profile (S09-07-ca-certificates, "CA
	// profile"). wb-hostd ends wb-proxyd with it.
	RuleReplyInvalid = "ca-reply-invalid"
	// RuleGuestClock: the boot couldn't set the guest clock, step 1 of
	// S09-07-ca-certificates, "Boot, restore and host wake".
	RuleGuestClock = "ca-guest-clock"
	// RuleInstallRefused: wb-guestd refused InstallTrust. The VM runs on
	// with its inspected streams refused.
	RuleInstallRefused = "ca-install-refused"
	// RuleInstallUnreported: wb-guestd didn't answer InstallTrust within
	// Config.InstallTimeout, or the call failed. As RuleInstallRefused.
	RuleInstallUnreported = "ca-install-unreported"
	// RuleRotation is the rule of a step of the schedule: an issue, an
	// install, a signing switch.
	RuleRotation = "ca-rotation"
	// RuleExpired removes a CA past its end (S09-07-ca-certificates,
	// "Rotation", day 60).
	RuleExpired = "ca-expired"
	// RuleIssueFailed is the rule of the 5019 record of an issue that
	// failed. The record names wb-proxyd's rule as well.
	RuleIssueFailed = "ca-issue-failed"
	// RuleReset drops the CAs of `wb vm reset-ca` (S09-06-ca-keys,
	// "Recovery").
	RuleReset = "ca-reset"
	// RuleRecordChanged refuses a `wb vm reset-ca` whose confirmed CAs
	// aren't the record's, so it drops only what the person saw.
	RuleRecordChanged = "ca-record-changed"
	// RuleRemoved is the rule of a key item wb-proxyd reported deleted.
	RuleRemoved = "ca-item-removed"
)

// The schedule, in days from the current CA's issue
// (S09-07-ca-certificates, "Rotation"). Day 60 is the CA's end, which
// its certificate carries.
const (
	successorAfter = 30 * 24 * time.Hour
	switchAfter    = 58 * 24 * time.Hour
	// Lifetime is the validity wb-proxyd gives a CA.
	Lifetime = 60 * 24 * time.Hour
)

// DefaultInstallTimeout is how long a boot or a step waits for
// wb-guestd's InstallTrust answer when Config.InstallTimeout is 0.
const DefaultInstallTimeout = 30 * time.Second

// proxydProgram names wb-proxyd in the store access records.
const proxydProgram = "wb-proxyd"

// Reason is why the boot order runs (S09-07-ca-certificates, "Boot,
// restore and host wake").
type Reason int

// The reasons. Only a boot moves signing to the successor. A restore
// and a wake keep the CA that signed before, while it is valid.
const (
	ReasonBoot Reason = iota + 1
	ReasonRestore
	ReasonWake
)

func (r Reason) String() string {
	switch r {
	case ReasonBoot:
		return "boot"
	case ReasonRestore:
		return "restore"
	case ReasonWake:
		return "wake"
	}
	return fmt.Sprintf("reason-%d", int(r))
}

// Proxyd is wb-hostd's end of wb-proxyd's control channel.
type Proxyd interface {
	// Call sends m and returns the record wb-proxyd answered with, not
	// yet decoded. An error is a failed channel, not a refusal.
	Call(ctx context.Context, m *egressv1.HostdToProxyd) ([]byte, error)
	// Send sends m, which has no answer: a CaAccept.
	Send(ctx context.Context, m *egressv1.HostdToProxyd) error
}

// Guest is the guest link of a VM's run.
type Guest interface {
	// SetClock sets the guest clock from the host's, with the reason
	// (S06-06-devices-and-warm-start, "Time and sleep").
	SetClock(ctx context.Context, r Reason) error
	// InstallTrust makes certs the guest's Wraith Box trust
	// (S09-08-guest-trust). nil means wb-guestd reported the install. An
	// error that wraps context.DeadlineExceeded is an answer that didn't
	// come, and any other a refusal, whose text is the guest's.
	InstallTrust(ctx context.Context, certs [][]byte) error
}

// Audit is the audit writer.
type Audit interface {
	Record(r audit.Record)
	Write(ctx context.Context, r audit.Record) error
}

// Ender ends wb-proxyd: wb-hostd's restart rules decide what follows,
// and the exit counts toward the restart limit
// (S04-08-records-and-bounds).
type Ender interface {
	End(rule string)
}

// Config is what a Keeper needs.
type Config struct {
	Records *state.DB
	// Proxyd is wb-proxyd's control channel. nil is none yet: List and
	// Reset work, and the removal list waits for ProxydStarted.
	Proxyd Proxyd
	// Ender ends wb-proxyd for an invalid reply. A Keeper with a Proxyd
	// needs one.
	Ender Ender
	Audit Audit
	// DeleteSavedState deletes a VM's saved state, the last step of
	// `wb vm reset-ca`. No saved state exists before M6.4, so nil does
	// nothing.
	DeleteSavedState func(ctx context.Context, slot string) error
	// Clock is wall time: the CA's dates are. nil is time.Now.
	Clock func() time.Time
	// NewID returns a new CA id. nil is 128 random bits.
	NewID          func() (string, error)
	InstallTimeout time.Duration
	Log            *slog.Logger
}

// Keeper keeps the CA records. Its methods are safe for concurrent use.
// The caller runs one boot or step of a VM at a time, and `wb vm
// reset-ca` only while the VM is stopped.
type Keeper struct {
	cfg Config
	// mu orders the exchanges on wb-proxyd's control channel, one
	// message and its reply at a time, and guards the fields below.
	mu sync.Mutex
	// inSet holds, per slot, the CA ids of the last CaSet this wb-proxyd
	// took. Its removal check refuses an entry of one, so the batch
	// holds those back until a new set or a new wb-proxyd.
	inSet map[string]map[string]bool
	// held holds, per slot, the CA ids whose keys this wb-proxyd holds:
	// a CaSet fetches only the others.
	held map[string]map[string]bool
	// kept holds, per slot, the generation a restore or a wake started:
	// its run keeps the signer it had (S09-07-ca-certificates, "Boot,
	// restore and host wake").
	kept map[string]uint64
}

// New returns a Keeper.
func New(cfg Config) *Keeper {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = randomID
	}
	if cfg.InstallTimeout == 0 {
		cfg.InstallTimeout = DefaultInstallTimeout
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Keeper{cfg: cfg, inSet: map[string]map[string]bool{}, held: map[string]map[string]bool{}, kept: map[string]uint64{}}
}

// randomID is a CA id: 128 bits of crypto/rand, as 32 lowercase
// hexadecimal characters (control.proto, CaIssue.ca_id).
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Refusal is a boot or a command the CA record refused, with its rule
// and a message for the person. KeepsRunning is set when the VM runs on
// with its inspected streams refused, as after an install the guest
// didn't report; otherwise the start is refused.
type Refusal struct {
	Rule         string
	Message      string
	KeepsRunning bool
}

func (r *Refusal) Error() string { return r.Message }

// List returns slot's CAs, oldest first.
func (k *Keeper) List(ctx context.Context, slot string) ([]state.CA, error) {
	var cas []state.CA
	err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		var err error
		cas, err = tx.List(slot)
		return err
	})
	return cas, err
}

// ProxydStarted is called once a new wb-proxyd took its start message.
// It holds no key and no CA set yet, so the removal list goes out whole
// (S15-03-access-rules, "Removing CA keys", step 1).
func (k *Keeper) ProxydStarted(ctx context.Context) {
	k.mu.Lock()
	k.inSet = map[string]map[string]bool{}
	k.held = map[string]map[string]bool{}
	k.mu.Unlock()
	k.SendRemovals(ctx)
}

// item is the store item of slot's CA id, `wraithbox/ca/<vm>/<ca>`.
func item(slot, id string) string {
	return storeitem.Prefix + "ca/" + slot + "/" + id
}

// auditCA is a CA as its audit records name it.
func auditCA(ca state.CA) audit.CA {
	return audit.CA{ID: ca.ID, SHA256: ca.SHA256, NotBeforeUnix: ca.NotBefore.Unix(), NotAfterUnix: ca.NotAfter.Unix()}
}

func auditCAs(cas []state.CA) []audit.CA {
	out := make([]audit.CA, 0, len(cas))
	for _, ca := range cas {
		out = append(out, auditCA(ca))
	}
	return out
}

// changed writes the 5019 record of a change to slot's CAs, and logs
// it with its rule.
func (k *Keeper) changed(slot string, generation uint64, decision, rule, msg string, cas ...state.CA) {
	level := slog.LevelInfo
	if decision != "accept" {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("vm", slot), slog.Uint64("generation", generation),
		slog.String("decision", decision), slog.String("rule", rule),
	}
	for _, ca := range cas {
		attrs = append(attrs, slog.String("sha256", ca.SHA256))
	}
	k.cfg.Log.LogAttrs(context.Background(), level, msg, attrs...)
	k.cfg.Audit.Record(audit.CAChanged(audit.CAChange{
		Slot: slot, Generation: generation, Decision: decision, Rule: rule, Message: msg, CAs: auditCAs(cas),
	}))
}

// accessed writes the 3004 record of one access of wb-proxyd to a CA
// item (S15-03-access-rules, rule 6).
func (k *Keeper) accessed(slot string, generation uint64, rule, itemName, access string) {
	k.cfg.Audit.Record(audit.StoreAccessed(slot, generation, rule, proxydProgram, itemName, access))
}
