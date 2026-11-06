package ca

import (
	"context"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The checks of a refused removal entry (S15-03-access-rules, "Removing
// CA keys").
const (
	// CheckItemForm: the entry isn't `ca/<vm>/<ca>` with a VM slot and a
	// CA id.
	CheckItemForm = "item-form"
	// CheckInRecord: the entry's CA is a CA of a VM's last CaSet.
	CheckInRecord = "in-record"
)

// maxItemLog bounds an entry in a log line: Prefix and three parts of
// at most storeitem.MaxPart characters.
const maxItemLog = len(storeitem.Prefix) + 3*storeitem.MaxPart + 2

// removal is one entry of a batch that passed its checks.
type removal struct {
	item storeitem.Name
	vm   string
	id   string
}

// parseRemoval checks each entry of a removal batch
// (S15-03-access-rules, "Removing CA keys"): it must parse as
// `ca/<vm>/<ca>` with a VM slot and a CA id, which is the check that
// limits a compromised wb-hostd (T16-hostd-landing), and its CA must not
// be one inRecord reports. It returns the entries, or every entry that
// failed. The batch is refused whole when one failed.
func parseRemoval(items []string, inRecord func(id string) bool) ([]removal, []*egressv1.CaRemovalRefusal) {
	var ok []removal
	var refused []*egressv1.CaRemovalRefusal
	var index uint32
	for _, s := range items {
		r, form := parseCAItem(s)
		switch {
		case !form:
			refused = append(refused, &egressv1.CaRemovalRefusal{Index: index, Check: CheckItemForm})
		case inRecord(r.id):
			refused = append(refused, &egressv1.CaRemovalRefusal{Index: index, Check: CheckInRecord})
		default:
			ok = append(ok, r)
		}
		index++
	}
	if len(refused) > 0 {
		return nil, refused
	}
	return ok, nil
}

// parseCAItem parses an entry as `ca/<vm>/<ca>`, and reports whether it
// is one.
func parseCAItem(s string) (removal, bool) {
	item, err := storeitem.Parse(s)
	if err != nil || item.Form() != storeitem.FormCA {
		return removal{}, false
	}
	parts := strings.Split(strings.TrimPrefix(s, storeitem.Prefix), "/")
	vm, id := parts[1], parts[2]
	if !vmbundle.ValidSlot(vm) || !egressmsg.ValidCAID(id) {
		return removal{}, false
	}
	return removal{item: item, vm: vm, id: id}, true
}

// Remove runs a batch of the removal list (S15-03-access-rules,
// "Removing CA keys"). When an entry fails its check, it deletes nothing
// and refuses the batch with ca-removal-refused, and logs each entry
// that failed. Otherwise it deletes each item and closes a key it held
// of it. An item already gone counts as deleted, and an entry whose
// delete failed is logged with store-write-failed and left out of the
// answer, so wb-hostd sends it again.
func (a *Authority) Remove(ctx context.Context, m *egressv1.CaRemove) *egressv1.CaRemoved {
	a.ctl.Lock()
	defer a.ctl.Unlock()
	items := m.GetItems()
	a.mu.Lock()
	ok, refused := parseRemoval(items, a.inRecord)
	a.mu.Unlock()
	if len(refused) > 0 {
		for _, r := range refused {
			a.log.LogAttrs(ctx, slog.LevelWarn, "CA removal entry refused",
				slog.Uint64("index", uint64(r.GetIndex())),
				slog.String("item", guesttext.LogField(items[r.GetIndex()], maxItemLog)),
				slog.String("check", r.GetCheck()), slog.String("rule", RuleRemovalRefused), slog.String("decision", "refuse"))
		}
		return &egressv1.CaRemoved{Rule: RuleRemovalRefused, Refused: refused}
	}
	out := &egressv1.CaRemoved{}
	for _, r := range ok {
		a.forget(r.item)
		if err := a.remove(ctx, r.item); err != nil {
			a.logCA(slog.LevelError, "CA item not deleted", r.vm, r.id, string(storeitem.RuleWriteFailed),
				slog.String("item", r.item.String()), slog.String("reason", err.Error()))
			continue
		}
		a.logCA(slog.LevelInfo, "CA item deleted", r.vm, r.id, "", slog.String("item", r.item.String()))
		out.Deleted = append(out.Deleted, r.item.String())
	}
	return out
}

// inRecord reports whether id is a CA of some VM's last CaSet. The
// caller holds mu.
func (a *Authority) inRecord(id string) bool {
	for _, st := range a.vms {
		if st.record[id] {
			return true
		}
	}
	return false
}

// forget closes and drops the key of item, if this process holds it.
func (a *Authority) forget(item storeitem.Name) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.held[item.String()]; ok {
		h.close()
		delete(a.held, item.String())
	}
}
