package egressmsg

import (
	"fmt"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The rules a CA reply of wb-proxyd carries (control.proto,
// S09-06-ca-keys). wb-proxyd sends them and wb-hostd reads them, so
// both take them from here.
const (
	// RuleCAKeyRestore fails an issue or a restore whose round trip
	// through the store and the key store failed (S09-06-ca-keys,
	// "Valid CA").
	RuleCAKeyRestore = "ca-key-restore"
	// RuleCANeedsTeam takes the place of RuleCAKeyRestore when the store
	// refused the call because the build is ad-hoc signed
	// (S09-06-ca-keys, "Ad-hoc signed builds").
	RuleCANeedsTeam = string(storeitem.RuleNeedsTeam)
	// RuleCAIssueRefused refuses a CaIssue for a CA id that is in use
	// (S09-06-ca-keys, "Issue").
	RuleCAIssueRefused = "ca-issue-refused"
	// RuleCARemovalRefused refuses a whole removal batch
	// (S15-03-access-rules, "Removing CA keys").
	RuleCARemovalRefused = "ca-removal-refused"
)

// The checks of a refused removal entry (S15-03-access-rules, "Removing
// CA keys").
const (
	// CheckCAItemForm: the entry isn't `ca/<vm>/<ca>` with a VM slot and
	// a CA id.
	CheckCAItemForm = "item-form"
	// CheckCAInRecord: the entry's CA is a CA of a VM's last CaSet.
	CheckCAInRecord = "in-record"
)

// MaxCAItem is the longest entry of a CaRemoved: Prefix and three parts
// of at most storeitem.MaxPart characters, with their separators. A
// CaRemove entry wb-hostd sends is shorter.
const MaxCAItem = len(storeitem.Prefix) + 3*storeitem.MaxPart + 2

// caIssued checks a CaIssued: its VM and CA id, and exactly one of a
// certificate of 1 to MaxCACert bytes and a rule of an issue that
// failed. Whether the VM and the id are the ones wb-hostd asked for,
// and whether the certificate has the CA profile, is wb-hostd's check.
func caIssued(m *egressv1.CaIssued, hasFD bool) *Refusal {
	if r := descriptors("ca_issued", false, hasFD); r != nil {
		return r
	}
	if _, r := VM("ca_issued.vm", m.GetVm()); r != nil {
		return r
	}
	if r := caID("ca_issued.ca_id", m.GetCaId()); r != nil {
		return r
	}
	cert, rule := m.GetCertificate(), m.GetRule()
	switch {
	case len(cert) > 0 && rule != "":
		return refuse(CheckMessageForm, "ca_issued.rule", "is set with a certificate")
	case len(cert) > MaxCACert:
		return refuse(CheckMessageForm, "ca_issued.certificate", fmt.Sprintf("is %d bytes, the message takes 1 to %d", len(cert), MaxCACert))
	case len(cert) == 0 && !issueRules[rule]:
		return refuse(CheckMessageForm, "ca_issued.rule", "isn't a rule of a failed issue with no certificate")
	}
	return nil
}

// issueRules are the rules of a CaIssued with no certificate.
var issueRules = map[string]bool{RuleCAKeyRestore: true, RuleCANeedsTeam: true, RuleCAIssueRefused: true}

func caLoaded(m *egressv1.CaLoaded, hasFD bool) *Refusal {
	return vmGeneration("ca_loaded", m.GetVm(), m.GetGeneration(), false, hasFD)
}

// caRestoreFailed checks a CaRestoreFailed: its VM, generation, CA id,
// and a rule of a restore that failed.
func caRestoreFailed(m *egressv1.CaRestoreFailed, hasFD bool) *Refusal {
	if r := vmGeneration("ca_restore_failed", m.GetVm(), m.GetGeneration(), false, hasFD); r != nil {
		return r
	}
	if r := caID("ca_restore_failed.ca_id", m.GetCaId()); r != nil {
		return r
	}
	if rule := m.GetRule(); rule != RuleCAKeyRestore && rule != RuleCANeedsTeam {
		return refuse(CheckMessageForm, "ca_restore_failed.rule", "isn't "+RuleCAKeyRestore+" or "+RuleCANeedsTeam)
	}
	return nil
}

// caRemoved checks a CaRemoved: at most MaxRemoveItems entries deleted,
// each 1 to MaxCAItem bytes, or none and the batch refused with
// RuleCARemovalRefused and 1 to MaxRemoveItems entries, each with an
// index below MaxRemoveItems and a check of a removal entry. Whether
// each entry is one wb-hostd sent is wb-hostd's check.
func caRemoved(m *egressv1.CaRemoved, hasFD bool) *Refusal {
	if r := descriptors("ca_removed", false, hasFD); r != nil {
		return r
	}
	deleted := m.GetDeleted()
	if n := len(deleted); n > MaxRemoveItems {
		return refuse(CheckMessageForm, "ca_removed.deleted", fmt.Sprintf("holds %d entries, the message takes 0 to %d", n, MaxRemoveItems))
	}
	for i, s := range deleted {
		if n := len(s); n < 1 || n > MaxCAItem {
			return refuse(CheckMessageForm, fmt.Sprintf("ca_removed.deleted[%d]", i), fmt.Sprintf("is %d bytes, the message takes 1 to %d", n, MaxCAItem))
		}
	}
	switch m.GetRule() {
	case "":
		if len(m.GetRefused()) > 0 {
			return refuse(CheckMessageForm, "ca_removed.refused", "is set with no rule")
		}
		return nil
	case RuleCARemovalRefused:
		if len(deleted) > 0 {
			return refuse(CheckMessageForm, "ca_removed.deleted", "is set in a refused batch")
		}
		return removalRefusals(m.GetRefused())
	default:
		return refuse(CheckMessageForm, "ca_removed.rule", "isn't \"\" or "+RuleCARemovalRefused)
	}
}

// removalRefusals checks the entries of a refused removal batch.
func removalRefusals(refused []*egressv1.CaRemovalRefusal) *Refusal {
	if n := len(refused); n < 1 || n > MaxRemoveItems {
		return refuse(CheckMessageForm, "ca_removed.refused", fmt.Sprintf("holds %d entries, the message takes 1 to %d", n, MaxRemoveItems))
	}
	for i, r := range refused {
		field := fmt.Sprintf("ca_removed.refused[%d]", i)
		if r.GetIndex() >= MaxRemoveItems {
			return refuse(CheckMessageForm, field+".index", fmt.Sprintf("is %d, past the %d entries of a batch", r.GetIndex(), MaxRemoveItems))
		}
		if c := r.GetCheck(); c != CheckCAItemForm && c != CheckCAInRecord {
			return refuse(CheckMessageForm, field+".check", "isn't "+CheckCAItemForm+" or "+CheckCAInRecord)
		}
	}
	return nil
}
