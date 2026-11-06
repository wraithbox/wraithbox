package carecord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// call sends m to wb-proxyd and returns its answer, one exchange at a
// time on the control channel.
func (k *Keeper) call(ctx context.Context, m *egressv1.HostdToProxyd) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cfg.Proxyd.Call(ctx, m)
}

// replyInvalid ends wb-proxyd for a reply that broke its contract
// (S04-08-records-and-bounds), logs it with the rule, and returns the
// refusal of the boot or step that sent the message.
func (k *Keeper) replyInvalid(slot string, generation uint64, sent string, err error) *Refusal {
	k.cfg.Log.LogAttrs(context.Background(), slog.LevelError, "wb-proxyd's CA reply refused",
		slog.String("vm", slot), slog.Uint64("generation", generation), slog.String("sent", sent),
		slog.String("decision", "refuse"), slog.String("rule", RuleReplyInvalid), slog.String("reason", err.Error()))
	k.cfg.Ender.End(RuleReplyInvalid)
	return &Refusal{Rule: RuleReplyInvalid, Message: fmt.Sprintf("wb-proxyd answered the %s of VM %s with an invalid reply, and was ended", sent, slot)}
}

// issue asks wb-proxyd for a new CA of slot and records it
// (S09-06-ca-keys, "Issue": wb-hostd records only after the round
// trip). A failed issue returns a *Refusal with the rule. Its item, which
// wb-proxyd may have written, goes on the removal list unless wb-proxyd
// refused the id as one in use.
func (k *Keeper) issue(ctx context.Context, slot string, generation uint64) (state.CA, error) {
	id, err := k.cfg.NewID()
	if err != nil {
		return state.CA{}, fmt.Errorf("carecord: new CA id: %w", err)
	}
	rec, err := k.call(ctx, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaIssue{CaIssue: &egressv1.CaIssue{Vm: slot, CaId: id}}})
	if err != nil {
		k.failedIssue(ctx, slot, generation, id, RuleIssueFailed)
		return state.CA{}, fmt.Errorf("carecord: CaIssue: %w", err)
	}
	now := k.cfg.Clock()
	got, err := CheckIssued(rec, slot, id, now)
	if err != nil {
		k.failedIssue(ctx, slot, generation, id, RuleReplyInvalid)
		return state.CA{}, k.replyInvalid(slot, generation, "CaIssue", err)
	}
	if got.Rule != "" {
		k.failedIssueAccesses(slot, generation, id, got.Rule)
		if got.Rule != egressmsg.RuleCAIssueRefused {
			k.failedIssue(ctx, slot, generation, id, got.Rule)
			k.SendRemovals(ctx)
		} else {
			k.changed(slot, generation, "refuse", got.Rule, "CA issue refused")
		}
		return state.CA{}, &Refusal{Rule: got.Rule, Message: fmt.Sprintf("wb-proxyd couldn't issue a CA for VM %s (%s)", slot, got.Rule)}
	}
	ca := state.CA{
		ID: id, Slot: slot, Certificate: got.Cert.Raw, SHA256: got.SHA256,
		Issued: now, NotBefore: got.Cert.NotBefore, NotAfter: got.Cert.NotAfter,
	}
	if err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error { return tx.Insert(ca) }); err != nil {
		k.failedIssue(ctx, slot, generation, id, RuleIssueFailed)
		return state.CA{}, fmt.Errorf("carecord: record the CA: %w", err)
	}
	k.changed(slot, generation, "accept", RuleRotation, "CA issued", ca)
	// The round trip of S09-06-ca-keys, "Issue": wb-proxyd wrote the
	// item and fetched it back (S15-03-access-rules, rule 6).
	k.accessed(slot, generation, RuleRotation, item(slot, id), audit.StoreWrite)
	k.accessed(slot, generation, RuleRotation, item(slot, id), audit.StoreFetch)
	k.mu.Lock()
	k.setOf(k.held, slot)[id] = true
	k.mu.Unlock()
	return ca, nil
}

// failedIssue writes the 5019 record of an issue that failed with rule,
// and puts the CA's item on the removal list.
func (k *Keeper) failedIssue(ctx context.Context, slot string, generation uint64, id, rule string) {
	k.changed(slot, generation, "refuse", rule, "CA issue failed, its item goes on the removal list")
	err := k.cfg.Records.CAs(ctx, func(tx *state.CATx) error {
		return tx.AddRemoval(item(slot, id), state.RemovalFailedIssue, k.cfg.Clock())
	})
	if err != nil {
		k.cfg.Log.Error("CA item of a failed issue not on the removal list", "vm", slot, "item", item(slot, id),
			"rule", RuleIssueFailed, "reason", err.Error())
	}
}

// failedIssueAccesses writes the store access records of an issue
// wb-proxyd failed with rule (S15-03-access-rules, rule 6). Its check
// that the id is free fetched the item, and ca-issue-refused stops
// there. Otherwise a step of the round trip failed after it wrote and
// fetched the item, and the item was deleted (S09-06-ca-keys, "Issue").
func (k *Keeper) failedIssueAccesses(slot string, generation uint64, id, rule string) {
	it := item(slot, id)
	if rule == egressmsg.RuleCAIssueRefused {
		k.accessed(slot, generation, rule, it, audit.StoreFetch)
		return
	}
	for _, access := range []string{audit.StoreWrite, audit.StoreFetch, audit.StoreDelete} {
		k.accessed(slot, generation, rule, it, access)
	}
}

// load sends slot's CA set and waits for wb-proxyd's answer
// (S09-07-ca-certificates, "Messages"). signing is the CA id that signs,
// or "" for none, which wb-proxyd refuses streams with
// ca-not-installed. A CA that didn't restore refuses the start with
// wb-proxyd's rule, and names `wb vm reset-ca` (S09-06-ca-keys,
// "Restore"): it never leads to a new CA.
func (k *Keeper) load(ctx context.Context, slot string, generation uint64, live []state.CA, signing string) error {
	set := &egressv1.CaSet{Vm: slot, Generation: generation, SigningCaId: signing}
	for _, ca := range live {
		set.Cas = append(set.Cas, &egressv1.InspectionCa{CaId: ca.ID, Certificate: ca.Certificate})
	}
	if signing == "" {
		set.NoSigningRule = egressmsg.RuleCANotInstalled
	}
	rec, err := k.call(ctx, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaSet{CaSet: set}})
	if err != nil {
		return fmt.Errorf("carecord: CaSet: %w", err)
	}
	m, err := decodeReply(rec)
	if err == nil {
		err = k.loaded(m, slot, generation, live)
	}
	if _, ok := errors.AsType[*Refusal](err); ok {
		return err
	}
	if err != nil {
		return k.replyInvalid(slot, generation, "CaSet", err)
	}
	k.SendRemovals(ctx)
	return nil
}

// loaded checks m as the answer to a CaSet of live, and keeps which
// keys wb-proxyd holds.
func (k *Keeper) loaded(m *egressv1.ProxydToHostd, slot string, generation uint64, live []state.CA) error {
	setIDs := ids(live)
	switch r := m.GetKind().(type) {
	case *egressv1.ProxydToHostd_CaLoaded:
		if r.CaLoaded.GetVm() != slot || r.CaLoaded.GetGeneration() != generation {
			return invalid("CaLoaded of another VM or generation")
		}
		k.mu.Lock()
		fetched := fetches(setIDs, k.setOf(k.held, slot), "")
		k.held[slot] = setFrom(setIDs)
		k.inSet[slot] = setFrom(setIDs)
		k.mu.Unlock()
		for _, id := range fetched {
			k.accessed(slot, generation, RuleRotation, item(slot, id), audit.StoreFetch)
		}
		return nil
	case *egressv1.ProxydToHostd_CaRestoreFailed:
		f := r.CaRestoreFailed
		if f.GetVm() != slot || f.GetGeneration() != generation || !slices.Contains(setIDs, f.GetCaId()) {
			return invalid("CaRestoreFailed of another VM, generation or CA")
		}
		k.mu.Lock()
		fetched := fetches(setIDs, k.setOf(k.held, slot), f.GetCaId())
		k.held[slot] = map[string]bool{}
		k.inSet[slot] = setFrom(setIDs)
		k.mu.Unlock()
		for _, id := range fetched {
			k.accessed(slot, generation, f.GetRule(), item(slot, id), audit.StoreFetch)
		}
		return k.restoreFailed(slot, generation, live, f)
	default:
		return invalid("answered a CaSet with %s", kindName(m))
	}
}

// fetches returns the ids of setIDs whose items wb-proxyd fetches to
// restore a CA set: those whose keys it doesn't hold, in the set's
// order. With last set, it stops after last, the CA that didn't
// restore.
func fetches(setIDs []string, held map[string]bool, last string) []string {
	var out []string
	for _, id := range setIDs {
		if !held[id] {
			out = append(out, id)
		}
		if id == last {
			break
		}
	}
	return out
}

// restoreFailed logs a CA whose key didn't restore and returns the
// refusal of the start.
func (k *Keeper) restoreFailed(slot string, generation uint64, live []state.CA, f *egressv1.CaRestoreFailed) *Refusal {
	sha := ""
	for _, ca := range live {
		if ca.ID == f.GetCaId() {
			sha = ca.SHA256
		}
	}
	k.cfg.Log.LogAttrs(context.Background(), slog.LevelError, "CA key didn't restore, the VM doesn't start",
		slog.String("vm", slot), slog.Uint64("generation", generation), slog.String("sha256", sha),
		slog.String("decision", "refuse"), slog.String("rule", f.GetRule()))
	return &Refusal{Rule: f.GetRule(), Message: fmt.Sprintf(
		"the key of VM %s's CA %s didn't restore (%s). `wb vm reset-ca %s` drops the VM's CAs, and its next boot issues a new one",
		slot, sha, f.GetRule(), slot)}
}

// accept lets wb-proxyd accept slot's streams of generation
// (S09-07-ca-certificates, "Boot, restore and host wake", step 7).
func (k *Keeper) accept(ctx context.Context, slot string, generation uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	err := k.cfg.Proxyd.Send(ctx, &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaAccept{
		CaAccept: &egressv1.CaAccept{Vm: slot, Generation: generation},
	}})
	if err != nil {
		return fmt.Errorf("carecord: CaAccept: %w", err)
	}
	return nil
}

// setOf returns m[slot], made if missing. The caller holds k.mu.
func (k *Keeper) setOf(m map[string]map[string]bool, slot string) map[string]bool {
	s, ok := m[slot]
	if !ok {
		s = map[string]bool{}
		m[slot] = s
	}
	return s
}

func setFrom(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}
