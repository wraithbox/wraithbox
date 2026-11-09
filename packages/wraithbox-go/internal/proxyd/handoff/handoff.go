// Package handoff is wb-proxyd's end of each VM's hand-off socket
// (S07-05-stream-hand-off, S07-06-name-mapping): the sockets wb-hostd
// gives it on its control channel, the copy of each VM's name mapping
// built from checked name reports, and the checks on each stream before
// anything goes upstream. It serves SEC08-proj-isolation,
// SEC04-no-guest-secrets, SEC05-default-deny and SEC13-bounded-resources.
//
// wb-netd parses raw guest frames, so every byte read from a hand-off
// socket is guest-influenced input. A stream's VM comes from the socket
// it arrived on, which wb-hostd labeled. Its name comes from this
// package's copy of the mapping, never from the stream message. A stream
// that passes goes to a StreamHandler with that VM and name. A stream
// that doesn't is reset before anything resolves a name, opens an
// upstream connection or fetches a binding.
//
// The decisions are in copy.go, pure functions over a decoded message
// and a copy with no OS code, so the fuzz smoke run on
// Linux covers them (FuzzHandoffDecision). The socket I/O is a thin
// layer on internal/platform/fdchan, which returns an error that wraps
// errors.ErrUnsupported where it isn't built.
package handoff

import (
	"context"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"google.golang.org/protobuf/proto"
)

// The rules of this package. Each log record of a decision names one
// in its "rule" attribute, and the check that failed in "check".
const (
	// RuleHandoffSocketRefused: a HandoffSocket from wb-hostd refused
	// (S07-05-stream-hand-off, "From `wb-hostd`").
	RuleHandoffSocketRefused = "handoff-socket-refused"
	// RuleControlRefused: another message on the control channel
	// refused: one before ProxydStart, a second ProxydStart, a record
	// that doesn't decode to a kind, or a PolicyChange whose payload or
	// sequence is refused. wb-proxyd then ends the control channel
	// (control.proto).
	RuleControlRefused = "control-message-refused"
	// RuleNameReportRefused: a name report dropped
	// (S07-06-name-mapping, "Name reports").
	RuleNameReportRefused = "name-report-refused"
	// RuleNameReportMalformed: a malformed message from wb-netd closed
	// the VM's hand-off socket (S07-06-name-mapping, "Malformed
	// reports"; S07-05-stream-hand-off, "Message form").
	RuleNameReportMalformed = egressmsg.RuleNameReportMalformed
	// RuleReleaseUnheld: a release for an address the copy doesn't hold,
	// logged and ignored (S07-06-name-mapping, "Releases").
	RuleReleaseUnheld = "release-unheld"
	// RuleStreamHandoffRefused: a stream refused and reset
	// (S07-05-stream-hand-off, "Streams").
	RuleStreamHandoffRefused = "stream-handoff-refused"
	// RulePolicyRecomputed: an open stream that the new effective policy
	// no longer allows, reset (S07-01-per-vm-policy, "Open streams").
	RulePolicyRecomputed = "policy-recomputed"
	// RuleAddressConflict: an open stream to an address a conflicting
	// report removed, reset (S07-06-name-mapping, "Address conflicts").
	RuleAddressConflict = "address-conflict"
	// RuleStreamResetFailed: a StreamReset that couldn't be sent. The
	// VM's hand-off socket is closed before the stream's socket
	// (S07-05-stream-hand-off, "Resets").
	RuleStreamResetFailed = "stream-reset-failed"
	// RuleHandoffSocketEnded: wb-netd closed its end, or the socket ended
	// with a receive error. Every stream that came on it is closed.
	RuleHandoffSocketEnded = "handoff-socket-ended"
	// RuleFreshStart: a fresh HandoffSocket cleared the VM's copy, its
	// wildcard names and its policy (S07-06-name-mapping, "Lifetime").
	RuleFreshStart = "vm-fresh-start"
	// RuleActivePeriodStart: an ActivePeriodStart cleared the VM's
	// wildcard names (S07-06-name-mapping, "Wildcard budget, second
	// layer").
	RuleActivePeriodStart = "active-period-start"
)

// The checks this package adds to those of egressmsg (message-form,
// name-form, synthetic-range, vm-slot).
const (
	// CheckSocketType: a descriptor isn't the AF_UNIX socket its message
	// takes, SOCK_DGRAM for a hand-off socket and SOCK_STREAM for a
	// stream.
	CheckSocketType egressmsg.Check = "socket-type"
	// CheckGenerationOlder: a HandoffSocket older than the generation
	// held for the VM.
	CheckGenerationOlder egressmsg.Check = "generation-older"
	// CheckGeneration: a stream whose generation isn't its socket's.
	CheckGeneration egressmsg.Check = "generation"
	// CheckNoEntry: a stream to an address the copy doesn't hold.
	CheckNoEntry egressmsg.Check = "no-entry"
	// CheckNotAllowed: a name the VM's effective policy doesn't allow.
	CheckNotAllowed egressmsg.Check = "not-allowed"
	// CheckPortNotAllowed: a port the policy doesn't allow for the name.
	CheckPortNotAllowed egressmsg.Check = "port-not-allowed"
	// CheckStreamCap: the socket's open streams are at Caps.Streams.
	CheckStreamCap egressmsg.Check = "stream-cap"
	// CheckAddressConflict: an address the copy holds for another name.
	CheckAddressConflict egressmsg.Check = "address-conflict"
	// CheckWildcardBudget: a new name under a wildcard past the budget,
	// or whose ProxydWildcardName write failed or took over
	// WildcardWriteTimeout.
	CheckWildcardBudget egressmsg.Check = "wildcard-budget"
	// CheckVersion: the first record on a hand-off socket isn't
	// wb-netd's version record of this build. It is reported to wb-hostd
	// as message-form, the check HandoffSocketClosed takes.
	CheckVersion egressmsg.Check = "version-record"
	// CheckSequence: a PolicyChange whose sequence isn't above the last
	// one applied for the VM.
	CheckSequence egressmsg.Check = "sequence"
	// CheckPolicy: a PolicyChange whose policy policy.FromProto refused.
	CheckPolicy egressmsg.Check = "policy"
)

// WildcardWriteTimeout bounds the ProxydWildcardName write. A write
// that fails or returns after it counts as failed (S07-06-name-mapping,
// "Wildcard budget, second layer").
const WildcardWriteTimeout = 100 * time.Millisecond

// Caps are the caps per hand-off socket of S07-05-stream-hand-off,
// "Caps" (SEC13-bounded-resources). Each is configuration with a
// default; M3.12 wires the configuration.
type Caps struct {
	// Streams bounds the streams open from one hand-off socket. A stream
	// past it is refused with stream-cap.
	Streams int
	// Pending bounds the hand-off messages read from one socket and not
	// yet decided. Past it the socket's reader stops reading, so further
	// messages wait in the kernel and wb-netd's sends fail with ENOBUFS
	// (S07-05-stream-hand-off, "Transport").
	Pending int
}

// DefaultCaps returns the default caps. Streams matches wb-netd's
// default cap of established TCP connections (netd.DefaultCaps), so the
// two caps bind at the same load.
func DefaultCaps() Caps {
	return Caps{Streams: 1024, Pending: 64}
}

// Validate checks that each cap is at least one.
func (c Caps) Validate() error {
	if c.Streams < 1 || c.Pending < 1 {
		return fmt.Errorf("handoff: caps %+v: each cap must be at least 1", c)
	}
	return nil
}

// StreamHandler takes each stream that passed the checks.
// internal/proxyd/stream implements it. HandleStream owns the stream: it ends it with
// Stream.Reset or Stream.Close. It is called on the socket's own
// goroutine, which decides the socket's next message only after it
// returns, so it returns promptly and does its work on a goroutine of
// its own.
type StreamHandler interface {
	HandleStream(s *Stream)
}

// Sender sends one record, with a descriptor when fd isn't nil, in one
// attempt that never blocks: the send side of a fdchan.Channel.
type Sender interface {
	Send(record []byte, fd syscall.Conn) error
}

// WildcardRecorder writes a ProxydWildcardName accounting record to
// wb-hostd (S07-06-name-mapping, "Wildcard budget, second layer").
// The caller sets a deadline of WildcardWriteTimeout on ctx. An error,
// or a return after the deadline, counts as a failed write.
type WildcardRecorder interface {
	RecordWildcardName(ctx context.Context, vm string, name dnsname.Name) error
}

// AccountingChannel is the WildcardRecorder of wb-proxyd's accounting
// channel (S04-08-records-and-bounds, "Accounting and event channels").
type AccountingChannel struct {
	Ch Sender
}

// RecordWildcardName sends the record in one attempt.
func (a AccountingChannel) RecordWildcardName(_ context.Context, vm string, name dnsname.Name) error {
	rec := &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_ProxydWildcardName{
		ProxydWildcardName: &egressv1.ProxydWildcardName{Vm: vm, Name: name.String()},
	}}
	b, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("handoff: encode wildcard name: %w", err)
	}
	if err := a.Ch.Send(b, nil); err != nil {
		return fmt.Errorf("handoff: send wildcard name: %w", err)
	}
	return nil
}

// Event is a refusal for the audit log. M3.10's event channel takes
// them. Until then a nil Audit leaves only the log record.
type Event struct {
	VM         string
	Generation uint64
	Rule       string
	Check      egressmsg.Check
}

// Audit takes the refusals of this package. It returns promptly.
type Audit interface {
	Refused(e Event)
}

// Config is what NewReceiver needs.
type Config struct {
	// Version is wb-proxyd's build version, which it sends as the
	// version record on each hand-off socket and expects from wb-netd.
	// Empty means version.Build().
	Version string
	// Caps are the caps per socket. A zero cap takes its DefaultCaps value.
	Caps Caps
	// Handler takes each accepted stream.
	Handler StreamHandler
	// Wildcard writes the second layer of the wildcard budget.
	Wildcard WildcardRecorder
	// Audit takes each refusal, or is nil.
	Audit Audit
	// Log is the logger; nil means slog.Default(). Every record of a
	// rule goes through internal/ratelog with LogWindow.
	Log *slog.Logger
	// LogWindow is ratelog's window; 0 means ratelog.Window.
	LogWindow time.Duration
}

func (c *Config) withDefaults() (Config, error) {
	out := *c
	if out.Version == "" {
		out.Version = version.Build()
	}
	def := DefaultCaps()
	if out.Caps.Streams == 0 {
		out.Caps.Streams = def.Streams
	}
	if out.Caps.Pending == 0 {
		out.Caps.Pending = def.Pending
	}
	if err := out.Caps.Validate(); err != nil {
		return Config{}, err
	}
	if out.Handler == nil || out.Wildcard == nil {
		return Config{}, fmt.Errorf("handoff: a Handler and a Wildcard are required")
	}
	if out.Log == nil {
		out.Log = slog.Default()
	}
	return out, nil
}
