// Package egressmsg holds the form checks of the wraithbox.egress.v1
// contract (proto/wraithbox/egress/v1): the hand-off socket between
// wb-netd and wb-proxyd, the control channels between wb-hostd and each
// daemon, the accounting channel, and the payloads that cross a control
// channel as a pipe (S07-05-stream-hand-off, S07-06-name-mapping,
// S04-07-supervision, S04-08-records-and-bounds).
//
// Each decoder takes one received record and whether a descriptor came
// with it, and returns the checked message or a *Refusal that names the
// check that failed. It checks what the comments in the .proto files
// state: the record's size, that it decodes with no field the message
// doesn't have at any depth, that its kind is set, the descriptor count
// of its kind, and each field's bound. A name goes through
// internal/dnsname and must already be in canonical form, an address
// must be inside 198.18.0.0/15, and a VM must be a slot of
// internal/vmbundle. A decoder never repairs a field.
//
// What a field means to its receiver (whether a name is allowed, a
// generation current, a stream id held) is the receiver's check, on top
// of these. The receivers log each refusal with their own rule and the
// check, as internal/dnsname's callers do: this package doesn't log.
//
// The package has no OS code, so the fuzz smoke run, which runs on
// Linux, covers every decoder (S11-verification-and-spikes, "Fuzzing").
package egressmsg

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/netip"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// MaxRecord is the largest record on every channel of the contract,
// fixed in code (S07-05-stream-hand-off, "Message form";
// S04-08-records-and-bounds, "Accounting and event channels"). Each
// channel is made with fdchan.Socketpair(MaxRecord).
const MaxRecord = 4096

// SyntheticRange is the synthetic pool of S07-03-dns, "Synthetic
// addresses". Every address in a message is inside it. A test checks it
// against netd.DefaultSynthetic.
var SyntheticRange = netip.MustParsePrefix("198.18.0.0/15")

// MaxEntries is the most live mapping entries of one VM: the addresses
// of SyntheticRange (S04-08-records-and-bounds, "Mapping entries are
// checked"; S07-06-name-mapping, "Releases").
const MaxEntries = 1 << (32 - 15)

// MaxWildcardNames is the most names of one VM's active period in a
// start payload: the largest wildcard budget, policy.MaxWildcardBudget,
// which a test checks.
const MaxWildcardNames = 65535

// Check names the check that failed. The receivers log it next to their
// own rule, such as name-report-refused or accounting-record-malformed.
type Check string

// The checks, as the .proto comments name them.
const (
	// CheckMessageForm: the record's size, its decoding, a field the
	// message doesn't have, no kind set, a descriptor its kind doesn't
	// take or a missing one, a kind its sender doesn't send, or a field
	// outside its bound that isn't a name, an address or a VM.
	CheckMessageForm Check = "message-form"
	// CheckNameForm: a name outside the canonical name form
	// (S07-03-dns, "Name form"; S07-06-name-mapping, "Name reports").
	CheckNameForm Check = "name-form"
	// CheckSyntheticRange: an address outside 198.18.0.0/15.
	CheckSyntheticRange Check = "synthetic-range"
	// CheckVMSlot: a VM that isn't a slot of S06-04-vms-and-placement.
	CheckVMSlot Check = "vm-slot"
	// CheckPayload: a payload pipe that isn't a pipe, is over its cap,
	// isn't whole by its deadline, or has bytes after the message.
	CheckPayload Check = "payload-read"
)

// Refusal is the error of every refusal in this package. Its message
// never holds a name from a message, only the check, the field, and a
// dnsname fault. A VM is in it escaped and cut (internal/guesttext).
//
// Compare a *Refusal with nil before storing it in an error, so a nil
// one doesn't become a non-nil error.
type Refusal struct {
	Check Check
	// Field names the field at fault, such as "name_report.name", or ""
	// for the whole record.
	Field string

	msg string
	err error
}

func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	if r.Field == "" {
		return "egressmsg: " + string(r.Check) + ": " + r.msg
	}
	return "egressmsg: " + string(r.Check) + ": " + r.Field + ": " + r.msg
}

// Unwrap returns the cause, such as the protobuf decoding error.
func (r *Refusal) Unwrap() error { return r.err }

// LogAttrs returns the refusal as log attributes, for the receiver's
// log record next to its rule and decision.
func (r *Refusal) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String("check", string(r.Check)),
		slog.String("field", r.Field),
		slog.String("reason", r.msg),
	}
}

func refuse(c Check, field, msg string) *Refusal {
	return &Refusal{Check: c, Field: field, msg: msg}
}

// decode checks a record's size and decodes it into m, refusing a field
// m doesn't have at any depth (CheckMessageForm). proto.Unmarshal also
// refuses a string that isn't valid UTF-8.
func decode(record []byte, m proto.Message) *Refusal {
	if n := len(record); n < 1 || n > MaxRecord {
		return refuse(CheckMessageForm, "", fmt.Sprintf("record of %d bytes, the channel takes 1 to %d", n, MaxRecord))
	}
	return unmarshal(record, m)
}

// unmarshal decodes b into m, refusing a field m doesn't have at any
// depth.
func unmarshal(b []byte, m proto.Message) *Refusal {
	if err := proto.Unmarshal(b, m); err != nil {
		return &Refusal{Check: CheckMessageForm, msg: fmt.Sprintf("%d bytes don't decode", len(b)), err: err}
	}
	if path, ok := unknownField(m.ProtoReflect(), ""); ok {
		return refuse(CheckMessageForm, path, "holds a field the message doesn't have")
	}
	return nil
}

// unknownField reports the path of the first message, at any depth,
// that holds a field its type doesn't have. A field of another
// direction's message, or one a newer build added, is one.
func unknownField(m protoreflect.Message, path string) (string, bool) {
	if len(m.GetUnknown()) > 0 {
		return path, true
	}
	found, at := false, ""
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		name := join(path, string(fd.Name()))
		if fd.IsList() {
			l := v.List()
			for i := range l.Len() {
				if at, found = unknownField(l.Get(i).Message(), fmt.Sprintf("%s[%d]", name, i)); found {
					return false
				}
			}
			return true
		}
		at, found = unknownField(v.Message(), name)
		return !found
	})
	return at, found
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// descriptors checks the descriptor count of a kind: want says whether
// the kind takes one.
func descriptors(kind string, want, has bool) *Refusal {
	switch {
	case want && !has:
		return refuse(CheckMessageForm, kind, "came without the descriptor its kind takes")
	case !want && has:
		return refuse(CheckMessageForm, kind, "came with a descriptor its kind doesn't take")
	}
	return nil
}

// Name checks a name from a message: the name form rule of S07-03-dns,
// and already in canonical form, lowercase and without a trailing dot
// (S07-06-name-mapping, "Name reports"). It never folds a name: a
// capital letter or a trailing dot is refused, not repaired.
func Name(field, s string) (dnsname.Name, *Refusal) {
	n, r := dnsname.FromHost(s)
	if r != nil {
		return dnsname.Name{}, &Refusal{
			Check: CheckNameForm, Field: field,
			msg: fmt.Sprintf("%s, fault %s at label %d", dnsname.RuleForm, r.Fault, r.Label), err: r,
		}
	}
	if n.String() != s {
		return dnsname.Name{}, refuse(CheckNameForm, field, "not in canonical form: a capital letter or a trailing dot")
	}
	return n, nil
}

// Addr checks an address from a message and returns it: inside
// SyntheticRange.
func Addr(field string, wire uint32) (netip.Addr, *Refusal) {
	a := AddrFromWire(wire)
	if !SyntheticRange.Contains(a) {
		return netip.Addr{}, refuse(CheckSyntheticRange, field, "address "+a.String()+" is outside "+SyntheticRange.String())
	}
	return a, nil
}

// AddrFromWire returns the IPv4 address of a fixed32 address field,
// whose most significant byte is the first octet.
func AddrFromWire(wire uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], wire)
	return netip.AddrFrom4(b)
}

// AddrToWire returns the fixed32 field of an IPv4 address. ok is false
// for an address that isn't IPv4.
func AddrToWire(a netip.Addr) (wire uint32, ok bool) {
	if !a.Is4() {
		return 0, false
	}
	b := a.As4()
	return binary.BigEndian.Uint32(b[:]), true
}

// maxVMLog is how much of a VM field a refusal holds: more than the
// longest slot name.
const maxVMLog = 32

// VM checks a VM slot from a message.
func VM(field, s string) (string, *Refusal) {
	if !vmbundle.ValidSlot(s) {
		return "", refuse(CheckVMSlot, field, "VM "+guesttext.LogField(s, maxVMLog)+" isn't a VM slot")
	}
	return s, nil
}

// atLeastOne checks a number field that 0 doesn't fill, such as a
// generation, a sequence number or a stream id.
func atLeastOne(field string, v uint64) *Refusal {
	if v == 0 {
		return refuse(CheckMessageForm, field, "is 0, the least is 1")
	}
	return nil
}

// Marshal encodes a message as one record, and refuses one over
// MaxRecord. A sender uses it, so it never sends a record the receiver
// refuses for its size.
func Marshal(m proto.Message) ([]byte, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("egressmsg: %w", err)
	}
	if n := len(b); n < 1 || n > MaxRecord {
		return nil, fmt.Errorf("egressmsg: a record of %d bytes, the channel takes 1 to %d", n, MaxRecord)
	}
	return b, nil
}
