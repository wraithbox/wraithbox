package netd

import (
	"context"
	"log/slog"
	"net"
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

// Flow is a TCP connection the guest opens: its source, which the link
// filter has checked is the leased address, and its destination.
type Flow struct {
	Guest netip.AddrPort
	Dest  netip.AddrPort
}

// LogAttrs returns the flow as log attributes.
func (f Flow) LogAttrs() []slog.Attr {
	return []slog.Attr{slog.String("src", f.Guest.String()), slog.String("dst", f.Dest.String())}
}

// StreamDecision is a stream policy's answer for one SYN: accept, with
// the sink that takes the connection, or refuse, with the rule the
// reset is logged with.
type StreamDecision struct {
	Sink StreamSink
	Rule Rule
}

// StreamPolicy decides TCP to a synthetic address at its first SYN
// (S07-02-packet-path, "Connections"). The synthetic pool of
// internal/netd/synth implements it with its name mapping and the port
// check. It is called on its own goroutine per SYN, at most
// Caps.HalfOpenTCP at once.
//
// DecideStream returns promptly: it never waits on a peer, an approval
// or a lock held across I/O. The stack's Run waits for every decision in
// progress before it returns, so a decision that blocks keeps wb-netd
// from stopping.
type StreamPolicy interface {
	DecideStream(f Flow) StreamDecision
}

// StreamSink takes an accepted connection: the stream hand-off to
// wb-proxyd (S07-05-stream-hand-off). ServeStream owns conn and closes
// it when it is done. The connection keeps its place under
// Caps.EstablishedTCP until both the hook has closed conn and the stack
// has closed the TCP connection, whichever comes later. The stack sets
// no timeout while the hook holds conn, so the hook bounds a guest that
// stops acknowledging, with a write deadline or an idle timeout. It is
// called on its own goroutine.
type StreamSink interface {
	ServeStream(conn net.Conn, f Flow)
}

// DNSOverTCP serves DNS over TCP on the gateway's port 53 (S07-03-dns).
// ServeDNSOverTCP owns conn and closes it when it is done. As for
// StreamSink, the connection keeps its place under Caps.EstablishedTCP
// until both the hook and the stack have closed it, and the stack sets
// no timeout while the hook holds conn, so the hook bounds a guest that
// stops acknowledging, with a write deadline or an idle timeout. It is
// called on its own goroutine per connection.
type DNSOverTCP interface {
	ServeDNSOverTCP(conn net.Conn, f Flow)
}

// DNSOverUDP serves DNS over UDP on the gateway's port 53 (S07-03-dns).
// ServeDNSOverUDP runs on its own goroutine until conn is closed, which
// the stack does when it stops.
type DNSOverUDP interface {
	ServeDNSOverUDP(conn net.PacketConn)
}

// Hooks are what later parts of wb-netd plug into the stack. A nil hook
// refuses everything it would decide: TCP to a synthetic address is
// reset with synthetic-unmapped, TCP to the gateway's port 53 with
// gateway-port-refused, and UDP to port 53 is dropped with udp-unbound.
type Hooks struct {
	Stream     StreamPolicy
	DNSOverTCP DNSOverTCP
	DNSOverUDP DNSOverUDP
	// Events gets every drop and reset. Nil means they are only logged.
	Events Events
	// Log is the logger drops and resets are written to, rate-limited
	// per rule. Nil means slog.Default.
	Log *slog.Logger
}

// The interfaces below are what the synthetic pool of internal/netd/synth
// needs from the rest of wb-netd: the accounting channel, the hand-off
// socket and the effective policy (S04-08-records-and-bounds,
// S07-05-stream-hand-off). The caller of each method that takes a context
// sets a deadline of 100 ms on it. The method returns once the deadline
// passes. An error, or a return after the deadline, counts as a failed
// write or send.

// Accounting writes wb-netd's accounting records to wb-hostd
// (S04-08-records-and-bounds, "Accounting records"). wb-hostd keeps the
// name mapping from them (S04-07-supervision, "The kept name mapping").
type Accounting interface {
	// Entry records a new or renewed mapping entry. It is written before
	// the DNS answer that gives the address.
	Entry(ctx context.Context, name dnsname.Name, addr netip.Addr) error
	// Release records that addr no longer maps to a name. It is written
	// before the address goes to another name.
	Release(ctx context.Context, addr netip.Addr) error
	// Debit records a wildcard budget debit (S07-04-dns-refusals, "Order
	// at arrival").
	Debit(ctx context.Context, name dnsname.Name) error
}

// NameReports sends name reports and releases to wb-proxyd on the VM's
// hand-off socket (S07-06-name-mapping, "Name reports", "Releases").
type NameReports interface {
	// Report sends a name and its address. It is sent before the answer
	// that gives the address leaves.
	Report(ctx context.Context, name dnsname.Name, addr netip.Addr) error
	// Release sends that addr no longer maps to a name.
	Release(ctx context.Context, addr netip.Addr) error
}

// PortPolicy says whether the VM's effective policy allows a port for a
// name: in an endpoint's port or ports, or in a raw TCP entry
// (S07-02-packet-path, "Connections"). PortAllowed returns promptly,
// because the stream policy calls it at a SYN (StreamPolicy).
type PortPolicy interface {
	PortAllowed(name dnsname.Name, port uint16) bool
}

// NamedFlow is a flow to a synthetic address, with the name the address
// mapped to when the stream was accepted.
type NamedFlow struct {
	Flow
	Name dnsname.Name
}

// StreamHandOff takes an accepted connection to a synthetic address and
// hands it to wb-proxyd (S07-05-stream-hand-off). HandOffStream owns
// conn and closes it when it is done, as StreamSink.ServeStream does.
// The address stays mapped to the name until conn is closed. It is
// called on its own goroutine.
type StreamHandOff interface {
	HandOffStream(conn net.Conn, f NamedFlow)
}
