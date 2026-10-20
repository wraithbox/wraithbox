// Package vmhandoff is wb-hostd's end of the wb-vmd hand-off channel on
// macOS: it receives the host-guest socket connections wb-vmd opened at
// wb-hostd's request, each as one HandoffMessage record with its
// descriptor attached (S04-02-vm-provider, "Hand-off on macOS";
// proto/wraithbox/vmd/v1).
//
// wb-vmd runs Virtualization framework code on the guest's behalf, so a
// record from it is checked as untrusted input
// (SEC12-least-privilege). The transport is internal/platform/fdchan,
// which refuses a record over MaxRecord bytes, a truncated record or
// control data, and two or more descriptors, with its own rules. This
// package adds the checks of the hand-off message itself: exactly one
// descriptor, an AF_UNIX stream socket, a record that decodes, the
// current VM generation, an open request, the direction, and the guest
// port that request asked for. Each refusal closes every descriptor
// that came with the record, and is logged with its rule. Routing uses
// only the destination port, which wb-hostd chose (SEC08-proj-isolation,
// SEC10-audit, X18-vsock-handoff condition 1).
//
// It is in internal/platform because the hand-off is per OS
// (S12-01-platform-interfaces, "Host-guest socket on macOS"): Linux and
// Windows hosts reach the guest another way, and there every function
// returns an error that wraps errors.ErrUnsupported. The checks in this
// file have no OS code, so the fuzz smoke run, which runs on Linux,
// covers them.
package vmhandoff

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// MaxRecord is the largest hand-off record, as the comment on
// HandoffMessage in proto/wraithbox/vmd/v1 states. An encoded message is
// at most 45 bytes.
const MaxRecord = 256

// Rule names the rule behind a refusal, for the log and the caller.
type Rule string

// The rules of this package. A record fdchan refuses keeps fdchan's
// rule: fdchan-record-size for a record that is empty, over MaxRecord
// or truncated, fdchan-control-truncated, and fdchan-fd-count for two
// or more descriptors.
const (
	// RuleFDCount: the record came without a descriptor.
	RuleFDCount Rule = "handoff-fd-count"
	// RuleSocketType: the descriptor isn't an AF_UNIX stream socket,
	// which a vsock connection's descriptor is on macOS
	// (X18-vsock-handoff).
	RuleSocketType Rule = "handoff-socket-type"
	// RuleMalformed: the record doesn't decode as a HandoffMessage, or
	// holds a field the message doesn't have.
	RuleMalformed Rule = "handoff-malformed"
	// RuleGeneration: the generation is 0, or isn't the current one. A
	// record from an older generation is refused after a restore
	// (S04-02-vm-provider, "Restore").
	RuleGeneration Rule = "handoff-generation"
	// RuleUnexpected: the request ID is 0 or names no open request, or
	// the destination port isn't the guest port the request asked for.
	RuleUnexpected Rule = "handoff-unexpected"
	// RuleDirection: the direction isn't DIRECTION_HOST_TO_GUEST. The
	// host opens every connection (S04-02-vm-provider, "The host opens
	// every connection").
	RuleDirection Rule = "handoff-direction"
	// RuleRequest: Expect was asked for request ID 0, generation 0, a
	// generation older than the current one, or a request ID that is
	// already open. That is the caller's mistake, not wb-vmd's.
	RuleRequest Rule = "handoff-request-invalid"
	// RuleClosed: the receiver was closed while the request was open.
	RuleClosed Rule = "handoff-closed"
)

// Refusal is the error of every refusal in this package.
type Refusal struct {
	Rule Rule

	msg string
	err error
}

func (r *Refusal) Error() string { return "vmhandoff: " + string(r.Rule) + ": " + r.msg }

// Unwrap returns the cause, such as the error of a failed getsockopt.
func (r *Refusal) Unwrap() error { return r.err }

// request is one open ConnectGuest request.
type request struct {
	generation uint64
	port       uint32
	// waiter is how the request gets its connection, on the OS where
	// the receiver is built.
	waiter
}

// table is the receiver's state: the current VM generation and the open
// requests by request ID. Its methods don't lock; the receiver does.
type table struct {
	generation uint64
	open       map[uint64]*request
}

func newTable() *table {
	return &table{open: make(map[uint64]*request)}
}

// expect opens a request. A newer generation becomes the current one,
// and the requests of older generations are closed and returned, so the
// caller can fail them.
func (t *table) expect(id, generation uint64, port uint32) (*request, []*request, *Refusal) {
	switch {
	case id == 0:
		return nil, nil, &Refusal{Rule: RuleRequest, msg: "request ID 0"}
	case generation == 0:
		return nil, nil, &Refusal{Rule: RuleRequest, msg: "generation 0"}
	case generation < t.generation:
		return nil, nil, &Refusal{Rule: RuleRequest, msg: fmt.Sprintf("generation %d is older than the current %d", generation, t.generation)}
	case t.open[id] != nil:
		return nil, nil, &Refusal{Rule: RuleRequest, msg: fmt.Sprintf("request ID %d is already open", id)}
	}
	var stale []*request
	if generation > t.generation {
		t.generation = generation
		for oid, r := range t.open {
			stale = append(stale, r)
			delete(t.open, oid)
		}
	}
	r := &request{generation: generation, port: port, waiter: newWaiter()}
	t.open[id] = r
	return r, stale, nil
}

// check decodes a record and applies the message checks, in the order
// of the comment on HandoffMessage. It returns the message and its
// request on success. On a refusal it returns the rule, and the request
// when the record named an open request of the current generation: that
// request gets the refusal instead of waiting for a connection that
// won't come. Either way the request is no longer open.
func (t *table) check(record []byte) (*vmdv1.HandoffMessage, *request, *Refusal) {
	msg := &vmdv1.HandoffMessage{}
	if err := proto.Unmarshal(record, msg); err != nil {
		return nil, nil, &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("record of %d bytes doesn't decode", len(record)), err: err}
	}
	if n := len(msg.ProtoReflect().GetUnknown()); n > 0 {
		return nil, nil, &Refusal{Rule: RuleMalformed, msg: fmt.Sprintf("%d bytes of fields HandoffMessage doesn't have", n)}
	}
	if g := msg.GetGeneration(); g == 0 || g != t.generation {
		return msg, nil, &Refusal{Rule: RuleGeneration, msg: fmt.Sprintf("generation %d, the current one is %d", g, t.generation)}
	}
	id := msg.GetRequestId()
	r := t.open[id]
	if id == 0 || r == nil {
		return msg, nil, &Refusal{Rule: RuleUnexpected, msg: fmt.Sprintf("request ID %d is not open", id)}
	}
	delete(t.open, id)
	if r.generation != msg.GetGeneration() {
		// expect closes the requests of an older generation, so this
		// can't happen. It is checked anyway.
		return msg, r, &Refusal{Rule: RuleGeneration, msg: fmt.Sprintf("request %d is of generation %d", id, r.generation)}
	}
	if d := msg.GetDirection(); d != vmdv1.Direction_DIRECTION_HOST_TO_GUEST {
		return msg, r, &Refusal{Rule: RuleDirection, msg: "direction " + d.String()}
	}
	if p := msg.GetDestinationPort(); p != r.port {
		return msg, r, &Refusal{Rule: RuleUnexpected, msg: fmt.Sprintf("destination port %d, request %d asked for %d", p, id, r.port)}
	}
	return msg, r, nil
}
