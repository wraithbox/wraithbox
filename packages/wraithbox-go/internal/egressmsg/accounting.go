package egressmsg

import (
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// Sender is the daemon at the other end of an accounting channel.
type Sender int

// The senders of accounting records.
const (
	SenderNetd Sender = iota + 1
	SenderProxyd
)

// AccountingKind is the kind of an AccountingRecord.
type AccountingKind int

// The kinds of AccountingRecord. The zero AccountingKind is no kind.
const (
	KindWildcardDebit AccountingKind = iota + 1
	KindMappingEntry
	KindMappingRelease
	KindProxydWildcardName
)

// String returns the kind's name, for log records.
func (k AccountingKind) String() string {
	switch k {
	case KindWildcardDebit:
		return "wildcard-debit"
	case KindMappingEntry:
		return "mapping-entry"
	case KindMappingRelease:
		return "mapping-release"
	case KindProxydWildcardName:
		return "proxyd-wildcard-name"
	}
	return "none"
}

// AccountingRecord is a checked accounting record. Only the fields of
// its kind are set.
type AccountingRecord struct {
	Kind AccountingKind
	// Name is the name of a wildcard debit, a mapping entry or a
	// wb-proxyd wildcard name, in canonical form.
	Name dnsname.Name
	// Addr is the address of a mapping entry or release, inside
	// SyntheticRange.
	Addr netip.Addr
	// VM is the slot of a wb-proxyd wildcard name. wb-hostd checks it
	// against the VMs it gave wb-proxyd hand-off sockets for.
	VM string
}

// DecodeAccountingRecord checks one record that wb-hostd read from the
// accounting channel of from, and whether a descriptor came with it,
// against the comments in accounting.proto. No kind takes a descriptor,
// and a kind the sender doesn't send is refused with CheckMessageForm.
// wb-hostd treats any refusal as a crash of the sender
// (S04-08-records-and-bounds, "Mapping entries are checked").
//
// With a refusal, the record returned holds only its Kind, when the
// record decoded to one, and no fields. So wb-hostd can log a refused
// mapping entry with the rule mapping-entry-invalid and any other
// refusal as a malformed record.
func DecodeAccountingRecord(record []byte, hasFD bool, from Sender) (AccountingRecord, *Refusal) {
	m := &egressv1.AccountingRecord{}
	if r := decode(record, m); r != nil {
		return AccountingRecord{}, r
	}
	if r := descriptors("accounting_record", false, hasFD); r != nil {
		return AccountingRecord{}, r
	}
	switch k := m.GetKind().(type) {
	case *egressv1.AccountingRecord_WildcardDebit:
		return fromNetd(from, KindWildcardDebit, "wildcard_debit", func() (AccountingRecord, *Refusal) {
			n, r := Name("wildcard_debit.name", k.WildcardDebit.GetName())
			return AccountingRecord{Kind: KindWildcardDebit, Name: n}, r
		})
	case *egressv1.AccountingRecord_MappingEntry:
		return fromNetd(from, KindMappingEntry, "mapping_entry", func() (AccountingRecord, *Refusal) {
			return mappingEntry(k.MappingEntry)
		})
	case *egressv1.AccountingRecord_MappingRelease:
		return fromNetd(from, KindMappingRelease, "mapping_release", func() (AccountingRecord, *Refusal) {
			a, r := Addr("mapping_release.address", k.MappingRelease.GetAddress())
			return AccountingRecord{Kind: KindMappingRelease, Addr: a}, r
		})
	case *egressv1.AccountingRecord_ProxydWildcardName:
		refused := AccountingRecord{Kind: KindProxydWildcardName}
		if from != SenderProxyd {
			return refused, refuse(CheckMessageForm, "proxyd_wildcard_name", "a kind only wb-proxyd sends")
		}
		rec, r := proxydWildcardName(k.ProxydWildcardName)
		if r != nil {
			return refused, r
		}
		return rec, nil
	}
	return AccountingRecord{}, refuse(CheckMessageForm, "kind", "no kind set")
}

// fromNetd refuses a kind only wb-netd sends from another sender, and
// checks the record's fields otherwise. A refused record holds only its
// kind.
func fromNetd(from Sender, kind AccountingKind, field string, fields func() (AccountingRecord, *Refusal)) (AccountingRecord, *Refusal) {
	if from != SenderNetd {
		return AccountingRecord{Kind: kind}, refuse(CheckMessageForm, field, "a kind only wb-netd sends")
	}
	rec, r := fields()
	if r != nil {
		return AccountingRecord{Kind: kind}, r
	}
	return rec, nil
}

// MappingEntry checks a mapping entry, as an accounting record or in a
// NetdStartPayload.
func mappingEntry(m *egressv1.MappingEntry) (AccountingRecord, *Refusal) {
	n, r := Name("mapping_entry.name", m.GetName())
	if r != nil {
		return AccountingRecord{}, r
	}
	a, r := Addr("mapping_entry.address", m.GetAddress())
	if r != nil {
		return AccountingRecord{}, r
	}
	return AccountingRecord{Kind: KindMappingEntry, Name: n, Addr: a}, nil
}

func proxydWildcardName(m *egressv1.ProxydWildcardName) (AccountingRecord, *Refusal) {
	vm, r := VM("proxyd_wildcard_name.vm", m.GetVm())
	if r != nil {
		return AccountingRecord{}, r
	}
	n, r := Name("proxyd_wildcard_name.name", m.GetName())
	if r != nil {
		return AccountingRecord{}, r
	}
	return AccountingRecord{Kind: KindProxydWildcardName, Name: n, VM: vm}, nil
}
