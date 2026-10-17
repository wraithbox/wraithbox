package vm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
)

// ResetData replaces slot's data disk for `wb vm reset-data`
// (S05-03-management-commands, "`wb vm reset-data`"). It is refused with
// vm-running unless the VM is stopped, and with vm-resetting while
// another reset of the slot runs. It holds the slot's start lock until
// it is done, so a start meanwhile is refused with vm-resetting. The
// reset runs to its end when the caller goes away, so its records match
// the bundle.
//
// wb asks for it only after a person confirmed in wb's own terminal
// (SEC14-no-fake-approvals). No guest RPC reaches it.
func (m *Manager) ResetData(ctx context.Context, slot Slot) (datadisk.ResetResult, error) {
	if r := m.checkRequest(slot); r != nil {
		return datadisk.ResetResult{}, m.refuse(ctx, "reset-data", r)
	}
	if r := m.beginReset(slot); r != nil {
		return datadisk.ResetResult{}, m.refuse(ctx, "reset-data", r)
	}
	defer m.endReset(slot)
	// wb-diskfmt refuses a missing bundle, so a reset before the first
	// start makes it, as a start does.
	if _, r := ensureBundle(slot, m.bundlePath(slot)); r != nil {
		return datadisk.ResetResult{}, m.refuse(ctx, "reset-data", r)
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm data disk reset",
		slog.String("decision", "accept"), slog.String("slot", string(slot)))
	res, err := m.cfg.DataDisks.Reset(context.WithoutCancel(ctx), string(slot))
	if err != nil {
		return res, m.refuse(ctx, "reset-data", dataDiskRefusal(slot, err))
	}
	return res, nil
}

// beginReset takes a stopped slot's start lock for a reset.
func (m *Manager) beginReset(slot Slot) *Refusal {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.slots[slot]
	if rec.resetting {
		return resettingRefusal(slot, "reset the data disk of")
	}
	if rec.phase != PhaseStopped {
		return &Refusal{
			Rule: RuleRunning, Slot: slot, Phase: rec.phase, Generation: rec.generation,
			Message: fmt.Sprintf("can't reset the %s VM's data disk: the VM is %s. Stop it with wb vm stop %s first", slot, rec.phase, slot),
		}
	}
	rec.resetting = true
	return nil
}

func (m *Manager) endReset(slot Slot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slots[slot].resetting = false
}
