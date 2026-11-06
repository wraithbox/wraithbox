package carecord

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// Reset drops every CA of slot's record for `wb vm reset-ca`
// (S09-06-ca-keys, "Recovery"; S05-03-management-commands). It is the
// only drop of a CA that hasn't expired. Its caller runs it while the VM
// is stopped, after the person confirmed the CAs whose fingerprints
// confirmed holds in wb's own terminal (SEC14-no-fake-approvals).
//
// In one transaction it checks that the record still holds exactly the
// confirmed CAs, and otherwise refuses with RuleRecordChanged. It drops
// them, puts their items on the removal list, and writes the 5019
// record with each fingerprint and the command, which the writer must
// accept before the drop commits (SEC10-audit). It then deletes the
// VM's saved state and sends the removal list. It returns the CAs
// dropped. With no CA in the record it changes nothing.
func (k *Keeper) Reset(ctx context.Context, slot string, confirmed []string, command string) ([]state.CA, error) {
	var dropped []state.CA
	err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		var err error
		dropped, err = k.dropAll(ctx, tx, slot, confirmed, command)
		return err
	})
	if err != nil {
		rule := RuleReset
		if ref, ok := errors.AsType[*Refusal](err); ok {
			rule = ref.Rule
		}
		k.cfg.Log.Warn("CA reset refused", "vm", slot, "decision", "refuse", "rule", rule, "error", err.Error())
		return nil, err
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	for _, ca := range dropped {
		k.cfg.Log.Info("CA dropped", "vm", slot, "sha256", ca.SHA256, "decision", "accept", "rule", RuleReset,
			"command", command)
	}
	if k.cfg.DeleteSavedState != nil {
		if err := k.cfg.DeleteSavedState(ctx, slot); err != nil {
			return dropped, fmt.Errorf("carecord: delete the saved state of VM %s: %w", slot, err)
		}
	}
	k.SendRemovals(ctx)
	return dropped, nil
}

// dropAll is the transaction of Reset: the check of the confirmed CAs,
// the drop, the removal entries and the 5019 record.
func (k *Keeper) dropAll(ctx context.Context, tx *state.CATx, slot string, confirmed []string, command string) ([]state.CA, error) {
	cas, err := tx.List(slot)
	if err != nil {
		return nil, err
	}
	if !sameFingerprints(cas, confirmed) {
		return nil, &Refusal{Rule: RuleRecordChanged, Message: fmt.Sprintf(
			"VM %s's CAs changed since they were shown. Run the command again to see them", slot)}
	}
	if len(cas) == 0 {
		return nil, nil
	}
	now := k.cfg.Clock()
	for _, ca := range cas {
		if err := tx.Drop(slot, ca.ID, item(slot, ca.ID), state.RemovalResetCA, now); err != nil {
			return nil, err
		}
	}
	return cas, k.cfg.Audit.Write(ctx, audit.CAChanged(audit.CAChange{
		Slot: slot, Decision: "accept", Rule: RuleReset, Command: command,
		Message: "CA dropped by " + command, CAs: auditCAs(cas),
	}))
}

// sameFingerprints reports whether cas are exactly the CAs whose
// fingerprints confirmed holds.
func sameFingerprints(cas []state.CA, confirmed []string) bool {
	have := make([]string, 0, len(cas))
	for _, ca := range cas {
		have = append(have, ca.SHA256)
	}
	want := slices.Clone(confirmed)
	slices.Sort(have)
	slices.Sort(want)
	return slices.Equal(have, want)
}
