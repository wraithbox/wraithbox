// Package fdchan is the host's descriptor channel: a datagram socketpair
// that carries one record and at most one descriptor per message, and
// refuses anything else, closing every descriptor of a refused message.
//
// It is the transport of the wb-vmd hand-off (S04-02-vm-provider,
// "Hand-off on macOS"), the wb-launcher request channel
// (S04-03-self-sandboxing, "What keeps wb-launcher narrow"), the stream
// hand-off (S07-05-stream-hand-off, "Transport" and "Message form") and
// the accounting and event channels (S04-08-records-and-bounds) as those
// are built. The record bytes are the caller's: fdchan has no message
// format, and whether a kind of message may carry a descriptor is the
// caller's check. A caller refuses a record whose kind needs a
// descriptor when Message.File is nil, and calls Message.Close on a
// record whose kind allows none. A message can arrive without the
// descriptor its sender attached: the kernel discards descriptors that
// don't fit in the receiver's descriptor table, and Recv then refuses
// and drops the message, but a caller doesn't count on that.
//
// It is in internal/platform because the mechanism is per OS
// (S12-01-platform-interfaces, "Rules"). macOS has no SOCK_SEQPACKET for
// Unix domain sockets, so the darwin channel is SOCK_DGRAM. Linux and
// Windows return an error that wraps errors.ErrUnsupported. The package
// imports only the standard library, because wb-launcher uses it and
// must use only the standard library (S04-03-self-sandboxing).
package fdchan

import "os"

// MaxRecordLimit is the largest maximum record size a channel takes.
// The records of S04-08-records-and-bounds and S07-05-stream-hand-off are
// 4 KiB. The limit keeps a mistaken size from raising the socket buffers
// without bound (SEC13-bounded-resources).
const MaxRecordLimit = 64 << 10

// Rule names the rule behind a refusal, for the log and the caller's
// audit record.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleRecordSize: a record is empty, over the channel's maximum, or
	// arrived with MSG_TRUNC (S07-05-stream-hand-off, "Message form",
	// SEC13-bounded-resources).
	RuleRecordSize Rule = "fdchan-record-size"
	// RuleControlTruncated: the control data of a message arrived with
	// MSG_CTRUNC, or cut off inside a control message.
	RuleControlTruncated Rule = "fdchan-control-truncated"
	// RuleFDCount: a message carried two or more descriptors. No message
	// kind allows more than one (X18-vsock-handoff, condition 2).
	RuleFDCount Rule = "fdchan-fd-count"
	// RuleControlUnexpected: a message carried a control message other
	// than SCM_RIGHTS.
	RuleControlUnexpected Rule = "fdchan-control-unexpected"
	// RuleControlMalformed: the control data doesn't parse as control
	// messages, such as a header length shorter than the header or an
	// SCM_RIGHTS payload that isn't a whole number of descriptors.
	RuleControlMalformed Rule = "fdchan-control-malformed"
	// RuleSocketType: an inherited end isn't an AF_UNIX SOCK_DGRAM socket.
	RuleSocketType Rule = "fdchan-socket-type"
	// RuleSocketUnconnected: an inherited end has no peer.
	RuleSocketUnconnected Rule = "fdchan-socket-unconnected"
	// RuleFDTableFull: the descriptors of a message didn't fit in the
	// receiver's descriptor table. The kernel discards them, and Recv
	// drops the message.
	RuleFDTableFull Rule = "fdchan-fd-table-full"
)

// Refusal is the error of every refusal in this package.
type Refusal struct {
	Rule Rule

	msg string
	err error
}

func (r *Refusal) Error() string { return "fdchan: " + string(r.Rule) + ": " + r.msg }

// Unwrap returns the cause, such as the error of a failed getsockopt.
func (r *Refusal) Unwrap() error { return r.err }

// Message is one received message: the record and the descriptor that
// came with it, or nil. The caller owns both. A caller that refuses the
// message on its own rule (a kind that allows no descriptor, say) calls
// Close.
type Message struct {
	Record []byte
	File   *os.File
}

// Close closes the message's descriptor, if it has one.
func (m *Message) Close() error {
	if m.File == nil {
		return nil
	}
	return m.File.Close()
}
