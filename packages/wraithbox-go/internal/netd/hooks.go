package netd

import (
	"log/slog"
	"net"
	"net/netip"
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
// (S07-02-packet-path, "Connections"). The DNS server's name mapping and
// port check implement it. It is called on its own goroutine per SYN,
// at most Caps.HalfOpenTCP at once.
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
