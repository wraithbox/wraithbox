package egressmsg

import (
	"fmt"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// The bounds of the CA messages of control.proto (S09-07-ca-certificates,
// "Messages"). Each message fits in one record of MaxRecord bytes:
// MaxCAs certificates of MaxCACert bytes, or MaxRemoveItems item names,
// take less than 4096 bytes with their framing.
const (
	// MaxCAs is the most CAs of one VM in a CaSet. A VM has two from day
	// 30 of each CA on (S09-07-ca-certificates, "Scope"), and the third
	// leaves room for a successor that wb-hostd records before it
	// removes an expired one.
	MaxCAs = 3
	// MaxCACert is the largest CA certificate, DER. A certificate of the
	// CA profile of S09-07-ca-certificates is under 600 bytes.
	MaxCACert = 1024
	// MaxRemoveItems is the most entries of one CaRemove.
	MaxRemoveItems = 64
	// CAIDLen is the length of a CA id: 32 lowercase hexadecimal
	// characters, 128 random bits.
	CAIDLen = 32
)

// RuleCANotInstalled is the rule of a CaSet whose VM has no signing CA
// (S09-07-ca-certificates, "Rotation", day 58).
const RuleCANotInstalled = "ca-not-installed"

// ValidCAID reports whether s is a CA id: CAIDLen characters of 0 to 9
// and a to f. Such an id is a valid part of an item name
// (storeitem.ValidPart), so `ca/<vm>/<ca>` parses.
func ValidCAID(s string) bool {
	if len(s) != CAIDLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// caID checks a CA id field.
func caID(field, s string) *Refusal {
	if !ValidCAID(s) {
		return refuse(CheckMessageForm, field, fmt.Sprintf("isn't %d characters of 0-9 and a-f", CAIDLen))
	}
	return nil
}

func caIssue(m *egressv1.CaIssue, hasFD bool) *Refusal {
	if r := descriptors("ca_issue", false, hasFD); r != nil {
		return r
	}
	if _, r := VM("ca_issue.vm", m.GetVm()); r != nil {
		return r
	}
	return caID("ca_issue.ca_id", m.GetCaId())
}

func caAccept(m *egressv1.CaAccept, hasFD bool) *Refusal {
	return vmGeneration("ca_accept", m.GetVm(), m.GetGeneration(), false, hasFD)
}

// caSet checks a CaSet: its VM and generation, 1 to MaxCAs CAs with
// distinct ids and certificates of 1 to MaxCACert bytes, and a signing
// CA that is one of them, or none with RuleCANotInstalled.
func caSet(m *egressv1.CaSet, hasFD bool) *Refusal {
	if r := vmGeneration("ca_set", m.GetVm(), m.GetGeneration(), false, hasFD); r != nil {
		return r
	}
	cas := m.GetCas()
	if n := len(cas); n < 1 || n > MaxCAs {
		return refuse(CheckMessageForm, "ca_set.cas", fmt.Sprintf("holds %d CAs, the message takes 1 to %d", n, MaxCAs))
	}
	ids := make(map[string]bool, len(cas))
	for i, ca := range cas {
		field := fmt.Sprintf("ca_set.cas[%d]", i)
		if r := caID(field+".ca_id", ca.GetCaId()); r != nil {
			return r
		}
		if ids[ca.GetCaId()] {
			return refuse(CheckMessageForm, field+".ca_id", "names a CA twice")
		}
		ids[ca.GetCaId()] = true
		if n := len(ca.GetCertificate()); n < 1 || n > MaxCACert {
			return refuse(CheckMessageForm, field+".certificate", fmt.Sprintf("is %d bytes, the message takes 1 to %d", n, MaxCACert))
		}
	}
	return signing(m, ids)
}

// signing checks the signing CA of a CaSet whose CA ids are ids.
func signing(m *egressv1.CaSet, ids map[string]bool) *Refusal {
	id, rule := m.GetSigningCaId(), m.GetNoSigningRule()
	switch {
	case id == "" && rule != RuleCANotInstalled:
		return refuse(CheckMessageForm, "ca_set.no_signing_rule", "isn't "+RuleCANotInstalled+" with no signing CA")
	case id != "" && rule != "":
		return refuse(CheckMessageForm, "ca_set.no_signing_rule", "is set with a signing CA")
	case id != "" && !ids[id]:
		return refuse(CheckMessageForm, "ca_set.signing_ca_id", "isn't one of the CAs")
	}
	return nil
}

// caRemove checks the count of a CaRemove's entries. The form of each
// entry is the receiver's check, so that one entry of another form
// refuses the batch with its rule (S15-03-access-rules, "Removing CA
// keys"), and doesn't end the channel.
func caRemove(m *egressv1.CaRemove, hasFD bool) *Refusal {
	if r := descriptors("ca_remove", false, hasFD); r != nil {
		return r
	}
	if n := len(m.GetItems()); n < 1 || n > MaxRemoveItems {
		return refuse(CheckMessageForm, "ca_remove.items", fmt.Sprintf("holds %d entries, the message takes 1 to %d", n, MaxRemoveItems))
	}
	return nil
}
