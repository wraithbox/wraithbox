// Package egress is wb-hostd's side of the egress daemons' channels:
// the accounting records it reads from wb-netd and wb-proxyd and the
// name mapping it keeps from them (S04-08-records-and-bounds,
// "Accounting and event channels"; S04-07-supervision, "The kept name
// mapping"), and its end of wb-proxyd's control channel, where it sets
// up each VM's hand-off socket and reads wb-proxyd's notices
// (S07-05-stream-hand-off, "One hand-off socket per VM").
//
// Both daemons read guest-shaped input, so every record and message
// they send is treated as adversarial (SEC08-proj-isolation,
// SEC13-bounded-resources). What wb-hostd refuses is a crash of the
// sender: it is logged with its rule and check, and the sender is ended
// through its Ender, which also stops the VMs it serves. Nothing from a
// refused record is kept.
package egress

import (
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// The rules of this package's log records.
const (
	// RuleRecordMalformed: an accounting record that isn't a mapping
	// entry failed a check, or the channel refused a datagram. It ends
	// the sender (S04-08-records-and-bounds, "Accounting and event
	// channels"; S07-06-name-mapping, "Kept by wb-hostd").
	RuleRecordMalformed = "accounting-record-malformed"
	// RuleEntryInvalid: a mapping entry failed a check
	// (S04-08-records-and-bounds, "Mapping entries are checked";
	// S07-06-name-mapping, "Kept by wb-hostd"). It ends the wb-netd that
	// sent it. Its log records are rate-limited.
	RuleEntryInvalid = "mapping-entry-invalid"
	// RuleReleaseUnheld: a release for an address the kept mapping
	// doesn't hold, logged and ignored (S07-06-name-mapping, "Kept by
	// wb-hostd"). Its log records are rate-limited.
	RuleReleaseUnheld = "mapping-release-unheld"
	// RuleAccountingFailed: reading an accounting channel failed with an
	// error that is neither a refusal nor the sender's exit, or the
	// version record couldn't be sent. It ends the sender.
	RuleAccountingFailed = "accounting-channel-failed"
	// RuleControlRefused: wb-hostd refused a message from wb-proxyd on
	// its control channel. It ends wb-proxyd (S04-architecture, "Control
	// channels").
	RuleControlRefused = "control-message-refused"
	// RuleControlFailed: a send, a read or a payload write on wb-proxyd's
	// control channel failed. It ends wb-proxyd.
	RuleControlFailed = "control-channel-failed"
	// RuleSetupFailed: a VM start's message to wb-proxyd got no
	// acknowledgment, or couldn't be sent, or wb-proxyd closed the new
	// hand-off socket before wb-netd held its end. The start fails. A
	// Handoff that fails closes both ends of the new hand-off socket
	// (S07-05-stream-hand-off, "One hand-off socket per VM").
	RuleSetupFailed = "proxyd-setup-failed"
	// RuleNoticeStale: a HandoffSocketClosed for a socket that isn't the
	// VM's current one, or one wb-hostd already acted on. It is logged
	// and ignored (S07-06-name-mapping, "Malformed reports").
	RuleNoticeStale = "handoff-notice-stale"
	// RuleNameUnserved: a wb-proxyd wildcard name for a VM that is
	// stopped, or whose hand-off socket wb-proxyd hasn't acknowledged
	// since its start. It is logged, rate-limited, and kept, or dropped
	// after a start that cleared the VM's state (S07-06-name-mapping,
	// "Kept by wb-hostd").
	RuleNameUnserved = "proxyd-name-unserved"
)

// The checks this package adds to those of egressmsg.
const (
	// CheckVMNotServed: a record for a VM slot wb-hostd never started.
	CheckVMNotServed = "vm-not-served"
	// CheckEntryCount: a new mapping entry past the bound on live
	// entries per VM, egressmsg.MaxEntries.
	CheckEntryCount = "entry-count"
	// CheckMappingConflict: a mapping entry whose address the kept
	// mapping holds for another name, or whose name it holds at another
	// address. wb-netd's pool releases an address before it gives it out
	// again.
	CheckMappingConflict = "mapping-conflict"
	// CheckWildcardCount: a new wildcard debit or wb-proxyd wildcard name
	// past egressmsg.MaxWildcardNames for the VM.
	CheckWildcardCount = "wildcard-count"
	// CheckDescriptor: a version record with a descriptor.
	CheckDescriptor = "descriptor"
)

// Ender ends a daemon: wb-hostd's supervisor stops the process, stops
// the VMs it serves, and applies its restart rules (S04-07-supervision;
// the restarts are #480).
type Ender interface {
	End(rule string)
}

// Conn is a channel to wb-netd or wb-proxyd: an *fdchan.Channel, or a
// fake in tests.
type Conn interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	SetReadDeadline(t time.Time) error
	Close() error
}
