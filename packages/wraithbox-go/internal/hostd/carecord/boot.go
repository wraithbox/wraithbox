package carecord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// Boot runs the CA steps of a boot, a restore from saved state or a host
// wake of slot's run of generation, in the order of
// S09-07-ca-certificates, "Boot, restore and host wake":
//
//  1. the guest clock is set;
//  2. expired CAs leave the record, and their items go on the removal
//     list;
//  3. a step of the schedule that came due runs: the successor is
//     issued from day 30, and signs from day 58 on a boot;
//  4. with no CA left, wb-proxyd issues one;
//  5. wb-proxyd loads the record's CAs;
//  6. the guest installs the record's CAs, never what it reports it has,
//     and wb-hostd records the install;
//  7. wb-proxyd accepts the VM's streams.
//
// A restore or a wake keeps the CA that signed before while it is
// valid, also past day 58. Only a boot moves signing to the successor.
//
// A *Refusal refuses the start, unless its KeepsRunning is set: the
// guest refused or didn't report the install, and the VM runs with its
// inspected streams refused, because no CaAccept was sent.
func (k *Keeper) Boot(ctx context.Context, slot string, generation uint64, reason Reason, g Guest) error {
	if err := g.SetClock(ctx, reason); err != nil {
		k.cfg.Log.Error("guest clock not set, the VM doesn't start", "vm", slot, "generation", generation,
			"reason", reason.String(), "decision", "refuse", "rule", RuleGuestClock, "error", err.Error())
		return &Refusal{Rule: RuleGuestClock, Message: fmt.Sprintf("the guest clock of VM %s wasn't set: %v", slot, err)}
	}
	k.mu.Lock()
	if reason == ReasonBoot {
		delete(k.kept, slot)
	} else {
		k.kept[slot] = generation
	}
	k.mu.Unlock()
	r, err := k.due(ctx, slot, generation)
	if err != nil {
		return err
	}
	if len(r.live) == 0 {
		ca, err := k.issue(ctx, slot, generation)
		if err != nil {
			return err
		}
		r.live = []state.CA{ca}
	}
	signing := k.bootSigner(r.live, reason)
	if err := k.load(ctx, slot, generation, r.live, signing); err != nil {
		return err
	}
	if err := k.install(ctx, slot, generation, g, r.live); err != nil {
		return err
	}
	if err := k.recordSigner(ctx, slot, generation, r, signing); err != nil {
		return err
	}
	return k.accept(ctx, slot, generation)
}

// bootSigner is the CA that signs after a boot, a restore or a wake.
// Step 6 installs every CA before step 7 accepts, so the scheduled CA
// signs on a boot. A restore or a wake keeps the record's signer while
// it is live.
func (k *Keeper) bootSigner(live []state.CA, reason Reason) string {
	if s := signer(live); s != nil && reason != ReasonBoot {
		return s.ID
	}
	return Due(live, k.cfg.Clock()).scheduled().ID
}

// Tick runs the steps of the schedule that are due on slot's running
// VM of generation (S09-07-ca-certificates, "Rotation"). Its caller runs
// it from a timer, at most one Boot or Tick of a VM at a time:
//
//   - day 30: wb-proxyd issues the successor and loads both CAs, the
//     current one signing, and the guest installs both;
//   - day 58: the successor signs if this run's guest reported its
//     install, and otherwise no CA does, with ca-not-installed;
//   - day 60: the old CA leaves the record, and its item goes on the
//     removal list.
//
// A run that a restore or a wake started keeps its signer while it is
// valid. A Tick with nothing due sends nothing.
func (k *Keeper) Tick(ctx context.Context, slot string, generation uint64, g Guest) error {
	r, err := k.due(ctx, slot, generation)
	if err != nil {
		return err
	}
	if len(r.live) == 0 {
		if r.dropped {
			k.cfg.Log.Warn("VM has no live CA, its next boot issues one", "vm", slot, "generation", generation,
				"decision", "refuse", "rule", RuleExpired)
		}
		return nil
	}
	signing := k.tickSigner(r, slot, generation)
	if !r.dropped && r.issued == nil && signing == r.prevSigner {
		return nil
	}
	if err := k.load(ctx, slot, generation, r.live, signing); err != nil {
		return err
	}
	if r.issued != nil {
		if err := k.install(ctx, slot, generation, g, r.live); err != nil {
			return err
		}
	}
	return k.recordSigner(ctx, slot, generation, r, signing)
}

// tickSigner is the CA that signs on a running VM: the scheduled CA
// once this run's guest reported its install, and otherwise none. A run
// a restore or a wake started keeps the record's signer while it is
// live.
func (k *Keeper) tickSigner(r dueResult, slot string, generation uint64) string {
	k.mu.Lock()
	kept := k.kept[slot] == generation
	k.mu.Unlock()
	if s := signer(r.live); s != nil && kept {
		return s.ID
	}
	ca := Due(r.live, k.cfg.Clock()).scheduled()
	if ca.InstallGeneration != generation {
		return ""
	}
	return ca.ID
}

// dueResult is the record after steps 2 and 3 of the boot order.
type dueResult struct {
	// live is the record's CAs that haven't expired, oldest first.
	live []state.CA
	// prevSigner is the CA id that signed before, expired or not, or "".
	prevSigner string
	dropped    bool
	issued     *state.CA
}

// due drops slot's expired CAs and issues the successor when it is due:
// steps 2 and 3 of the boot order. A successor whose issue failed is
// logged and audited, and the next step tries again. The current CA
// signs until its end.
func (k *Keeper) due(ctx context.Context, slot string, generation uint64) (dueResult, error) {
	cas, err := k.List(ctx, slot)
	if err != nil {
		return dueResult{}, fmt.Errorf("carecord: read the CA record: %w", err)
	}
	var r dueResult
	if s := signer(cas); s != nil {
		r.prevSigner = s.ID
	}
	p := Due(cas, k.cfg.Clock())
	r.live = p.Live
	if len(p.Expired) > 0 {
		if err := k.dropExpired(ctx, slot, generation, p.Expired); err != nil {
			return dueResult{}, err
		}
		r.dropped = true
	}
	if !p.IssueSuccessor {
		return r, nil
	}
	ca, err := k.issue(ctx, slot, generation)
	var refusal *Refusal
	switch {
	case err == nil:
		r.live = append(r.live, ca)
		r.issued = &ca
	case errors.As(err, &refusal) && refusal.Rule != RuleReplyInvalid:
		k.cfg.Log.Warn("successor CA not issued, the current CA signs on", "vm", slot, "generation", generation,
			"rule", refusal.Rule)
	default:
		return dueResult{}, err
	}
	return r, nil
}

// dropExpired takes the expired CAs out of slot's record and puts their
// items on the removal list (S09-07-ca-certificates, "Rotation", day
// 60). wb-proxyd deletes them once its CA set no longer has them.
func (k *Keeper) dropExpired(ctx context.Context, slot string, generation uint64, expired []state.CA) error {
	now := k.cfg.Clock()
	err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		for _, ca := range expired {
			if err := tx.Drop(slot, ca.ID, item(slot, ca.ID), state.RemovalExpired, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("carecord: drop the expired CAs: %w", err)
	}
	for _, ca := range expired {
		k.changed(slot, generation, "accept", RuleExpired, "CA removed", ca)
	}
	return nil
}

// install asks the guest to install live, the record's CAs, and records
// the install when it reports it (steps 5 and 6 of the boot order). A
// refused or unreported install is logged and audited, and returns a
// *Refusal with KeepsRunning set (S09-07-ca-certificates, "Rotation": a
// guest that doesn't report the install only cuts off its own traffic).
func (k *Keeper) install(ctx context.Context, slot string, generation uint64, g Guest, live []state.CA) error {
	ictx, cancel := context.WithTimeout(ctx, k.cfg.InstallTimeout)
	err := g.InstallTrust(ictx, certs(live))
	timedOut := ictx.Err() != nil
	cancel()
	if err != nil {
		rule, what := RuleInstallRefused, "refused"
		if timedOut || errors.Is(err, context.DeadlineExceeded) {
			rule, what = RuleInstallUnreported, "not reported"
		}
		k.cfg.Log.LogAttrs(ctx, slog.LevelWarn, "CA install "+what, slog.String("vm", slot),
			slog.Uint64("generation", generation), slog.String("decision", "refuse"), slog.String("rule", rule),
			slog.String("error", err.Error()))
		k.changed(slot, generation, "refuse", rule, "CA install "+what, live...)
		return &Refusal{Rule: rule, KeepsRunning: true, Message: fmt.Sprintf(
			"the guest of VM %s %s the install of its CAs, so its inspected streams are refused", slot, what)}
	}
	err = k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		return tx.MarkInstalled(slot, ids(live), generation, k.cfg.Clock())
	})
	if err != nil {
		return fmt.Errorf("carecord: record the install: %w", err)
	}
	k.changed(slot, generation, "accept", RuleRotation, "CA installed", live...)
	return nil
}

// recordSigner records signing as slot's signing CA when it changed,
// with its 5019 record: a switch, or no signer until the successor's
// install is recorded (S09-07-ca-certificates, "Rotation", day 58).
func (k *Keeper) recordSigner(ctx context.Context, slot string, generation uint64, r dueResult, signing string) error {
	if signing == r.prevSigner {
		return nil
	}
	if err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error { return tx.SetSigning(slot, signing) }); err != nil {
		return fmt.Errorf("carecord: record the signing CA: %w", err)
	}
	if signing == "" {
		k.changed(slot, generation, "refuse", egressmsg.RuleCANotInstalled,
			"CA signing waits for the successor's install", r.live...)
		return nil
	}
	msg := "CA signing switched"
	if r.prevSigner == "" {
		msg = "CA signs"
	}
	for _, ca := range r.live {
		if ca.ID == signing {
			k.changed(slot, generation, "accept", RuleRotation, msg, ca)
		}
	}
	return nil
}
