package audit

// CA is an inspection CA a 5019 record is about: its id and the SHA-256
// fingerprint of its certificate, DER, in lowercase hexadecimal
// (S09-07-ca-certificates, "Audit"). The certificate's dates help a
// reader tell two CAs of a VM apart.
type CA struct {
	ID            string `json:"id"`
	SHA256        string `json:"sha256"`
	NotBeforeUnix int64  `json:"not_before_unix,omitempty"`
	NotAfterUnix  int64  `json:"not_after_unix,omitempty"`
}

// CAChange is a change to a VM's inspection CAs.
type CAChange struct {
	// Slot is the VM. Generation is its run, 0 for a stopped VM.
	Slot       string
	Generation uint64
	// Decision is accept, or the failure's: refuse for an install the
	// guest refused or didn't report, or an issue that failed.
	Decision string
	Rule     string
	// Command is set for a drop by a command, and empty otherwise.
	Command string
	Message string
	CAs     []CA
}

// CAChanged is the 5019 record of a change to a VM's inspection CAs:
// an issue, an install, a signing switch, a removal, an install the
// guest refused or didn't report, or a drop by a command
// (S09-07-ca-certificates, "Audit"; S09-06-ca-keys, "Recovery"). A
// decision other than accept is a failure.
func CAChanged(c CAChange) Record {
	f := Fields{Decision: c.Decision, Rule: c.Rule, VM: c.Slot, Generation: c.Generation, Command: c.Command, CAs: c.CAs}
	o := outcomeOK
	if c.Decision != "accept" {
		o = outcomeFailed
	}
	h := newHeader(configLog, o.severity, c.Message, f)
	h.StatusID = o.status
	return Record{Slot: c.Slot, Generation: c.Generation, Event: &ConfigChange{header: h, Device: vmDevice(c.Slot)}}
}

// The store accesses of S15-03-access-rules, rule 6, by their Entity
// Management (3004) activity (S09-11-audit, "Classes"): a fetch is a
// read, a write an update, and a delete a delete of the item.
const (
	StoreFetch  = "fetch"
	StoreWrite  = "write"
	StoreDelete = "delete"
)

var storeActivities = map[string]activity{
	StoreFetch:  {ClassEntityManagement, categoryIAM, 2, "Read"},
	StoreWrite:  {ClassEntityManagement, categoryIAM, 3, "Update"},
	StoreDelete: {ClassEntityManagement, categoryIAM, 4, "Delete"},
}

// entityOther is the OCSF managed_entity type_id Other: a secret store
// item is none of the kinds OCSF names.
const entityOther = 99

// storeItemType is the managed_entity type of a store item.
const storeItemType = "Secret Store Item"

// ManagedEntity is the OCSF managed_entity object: the store item, by
// its name. It never holds the item's value.
type ManagedEntity struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	TypeID int    `json:"type_id"`
}

// Actor is the OCSF actor object: the program that made the access.
type Actor struct {
	AppName string `json:"app_name"`
}

// StoreAccess is an Entity Management (3004) record: one access to a
// secret store item.
type StoreAccess struct {
	header
	Entity ManagedEntity `json:"entity"`
	Actor  Actor         `json:"actor"`
}

// StoreItem is what a store access record holds besides its classes'
// attributes: the program, the item's name and the access kind, never
// the value (S15-03-access-rules, rule 6).
type StoreItem struct {
	Program string `json:"program"`
	Item    string `json:"item"`
	Access  string `json:"access"`
}

// StoreAccessed is the 3004 record of one access a program made to a
// store item of slot's VM. access is StoreFetch, StoreWrite or
// StoreDelete. wb-hostd writes it for the accesses wb-proxyd makes to
// a CA item, from wb-proxyd's reply to the message that asked for them
// (S15-03-access-rules, rule 6).
func StoreAccessed(slot string, generation uint64, rule, program, item, access string) Record {
	a, ok := storeActivities[access]
	if !ok {
		a = activity{ClassEntityManagement, categoryIAM, 99, "Other"}
	}
	f := Fields{
		Decision: "accept", Rule: rule, VM: slot, Generation: generation,
		Store: &StoreItem{Program: program, Item: item, Access: access},
	}
	return Record{Slot: slot, Generation: generation, Event: &StoreAccess{
		header: newHeader(a, severityInformational, "store item "+access, f),
		Entity: ManagedEntity{Name: item, Type: storeItemType, TypeID: entityOther},
		Actor:  Actor{AppName: program},
	}}
}

var _ Event = (*StoreAccess)(nil)
