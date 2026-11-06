package carecord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// SendRemovals sends the removal list to wb-proxyd, in batches of the
// oldest entries that fit one CaRemove (S15-03-access-rules, "Removing
// CA keys"). It runs at each wb-proxyd start, when an entry is added,
// and after a CaSet. An entry stays until wb-proxyd reports it deleted
// or refuses it:
//
//   - a deleted entry leaves the list, with the store access record of
//     the delete;
//   - an entry refused for its form leaves the list, logged with the
//     rule, and the rest go in the next batch;
//   - an entry refused because its CA is in a CA set stays, logged,
//     and is held back until the next CaSet of its VM.
//
// An entry whose CA is in a CA set wb-proxyd holds stays back: its
// check would refuse it. A failure is logged, and the next call tries
// again.
func (k *Keeper) SendRemovals(ctx context.Context) {
	for {
		more, err := k.sendBatch(ctx)
		if err != nil {
			k.cfg.Log.Error("CA removal batch failed, the entries stay on the list", "rule", RuleRemoved,
				"error", err.Error())
			return
		}
		if !more {
			return
		}
	}
}

// sendBatch sends one batch, and reports whether an entry left the
// list or was held back, so the next batch differs.
func (k *Keeper) sendBatch(ctx context.Context) (bool, error) {
	var list []state.Removal
	err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		var err error
		list, err = tx.Removals()
		return err
	})
	if err != nil {
		return false, fmt.Errorf("read the removal list: %w", err)
	}
	batch, rec, err := k.callRemove(ctx, list)
	if err != nil || batch == nil {
		return false, err
	}
	m, err := decodeReply(rec)
	var r *egressv1.CaRemoved
	if err == nil {
		r = m.GetCaRemoved()
		if r == nil {
			err = invalid("answered a CaRemove with %s", kindName(m))
		}
	}
	var gone []string
	held := 0
	if err == nil {
		gone, held, err = k.removed(batch, r)
	}
	if err != nil {
		return false, k.replyInvalid("", 0, "CaRemove", err)
	}
	if len(gone) == 0 {
		return held > 0, nil
	}
	err = k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		for _, it := range gone {
			if err := tx.DropRemoval(it); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("drop entries of the removal list: %w", err)
	}
	return true, nil
}

// callRemove sends the oldest entries of list that may go, at most
// egressmsg.MaxRemoveItems, and returns them with the answer. It
// returns no batch when none may go, or with no wb-proxyd channel: the
// list then waits for ProxydStarted.
func (k *Keeper) callRemove(ctx context.Context, list []state.Removal) ([]string, []byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cfg.Proxyd == nil {
		return nil, nil, nil
	}
	var batch []string
	for _, r := range list {
		if len(batch) == egressmsg.MaxRemoveItems {
			break
		}
		if !k.inASet(r.Item) {
			batch = append(batch, r.Item)
		}
	}
	if len(batch) == 0 {
		return nil, nil, nil
	}
	rec, err := k.cfg.Proxyd.Call(ctx, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaRemove{
		CaRemove: &egressv1.CaRemove{Items: batch},
	}})
	if err != nil {
		return nil, nil, fmt.Errorf("CaRemove: %w", err)
	}
	return batch, rec, nil
}

// removed checks r as the answer to batch, and returns the entries that
// leave the list: those deleted, or those refused for their form. An
// entry refused because its CA is in a CA set stays, held back until
// the next set. Each refused entry is logged with the rule, and each
// deleted one gets the store access record of its delete
// (S15-03-access-rules, rule 6, and "Removing CA keys").
func (k *Keeper) removed(batch []string, r *egressv1.CaRemoved) (gone []string, held int, err error) {
	sent := setFrom(batch)
	for _, it := range r.GetDeleted() {
		if !sent[it] {
			return nil, 0, invalid("CaRemoved names an entry not in the batch")
		}
	}
	for _, ref := range r.GetRefused() {
		if int(ref.GetIndex()) >= len(batch) {
			return nil, 0, invalid("CaRemoved refuses entry %d of a batch of %d", ref.GetIndex(), len(batch))
		}
	}
	for _, it := range r.GetDeleted() {
		slot := itemSlot(it)
		k.accessed(slot, 0, RuleRemoved, it, audit.StoreDelete)
		gone = append(gone, it)
	}
	for _, ref := range r.GetRefused() {
		it := batch[ref.GetIndex()]
		msg := "CA removal entry refused, dropped from the list"
		if ref.GetCheck() == egressmsg.CheckCAInRecord {
			msg = "CA removal entry refused, held back until the next CA set"
			k.holdBack(it)
			held++
		} else {
			gone = append(gone, it)
		}
		k.cfg.Log.LogAttrs(context.Background(), slog.LevelWarn, msg,
			slog.String("item", it), slog.String("check", ref.GetCheck()),
			slog.String("decision", "refuse"), slog.String("rule", egressmsg.RuleCARemovalRefused))
	}
	return gone, held, nil
}

// holdBack keeps item on the removal list out of the batches until
// the next CaSet of its VM, or a new wb-proxyd: wb-proxyd refused it
// because its CA is in a CA set it holds.
func (k *Keeper) holdBack(it string) {
	slot := itemSlot(it)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inSet[slot] == nil {
		k.inSet[slot] = map[string]bool{}
	}
	k.inSet[slot][it[strings.LastIndexByte(it, '/')+1:]] = true
}

// inASet reports whether the CA of item is in a CA set wb-proxyd holds.
// The caller holds k.mu.
func (k *Keeper) inASet(it string) bool {
	id := it[strings.LastIndexByte(it, '/')+1:]
	for _, set := range k.inSet {
		if set[id] {
			return true
		}
	}
	return false
}

// itemSlot returns the VM slot of an item `wraithbox/ca/<vm>/<ca>`, or
// "" for another form.
func itemSlot(it string) string {
	rest, ok := strings.CutPrefix(it, storeitem.Prefix+"ca/")
	if !ok {
		return ""
	}
	slot, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return slot
}
