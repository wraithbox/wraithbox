package synth

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// DecideStream is the stream policy of S07-02-packet-path,
// "Connections" (SEC05-default-deny). TCP to a synthetic address is
// reset at its first SYN with:
//   - synthetic-unmapped, when the address has no live entry;
//   - port-not-allowed, when the effective policy doesn't allow the port
//     for the entry's name;
//   - handoff-unavailable, when no stream hand-off is bound.
//
// Otherwise it is accepted, and its connection goes to the hand-off with
// the name. It takes only the pool's state lock, never one held across
// a write or send, so it returns promptly (netd.StreamPolicy).
func (p *Pool) DecideStream(f netd.Flow) netd.StreamDecision {
	name, ok := p.lookup(f.Dest.Addr())
	switch {
	case !ok:
		return netd.StreamDecision{Rule: netd.RuleSyntheticUnmapped}
	case p.deps.Ports == nil || !p.deps.Ports.PortAllowed(name, f.Dest.Port()):
		return netd.StreamDecision{Rule: netd.RulePortNotAllowed}
	case p.deps.HandOff == nil:
		return netd.StreamDecision{Rule: netd.RuleHandoffUnavailable}
	}
	return netd.StreamDecision{Sink: &pinSink{p: p, name: name}}
}

// pinSink takes one accepted connection. It pins the entry when it gets
// the connection, not at the decision: the stack doesn't call a sink
// whose handshake failed, and a pin taken at the decision would then
// never end.
type pinSink struct {
	p    *Pool
	name dnsname.Name
}

// ServeStream pins the entry of the connection's address and hands the
// connection on with the name. When the entry was released, or the
// address went to another name, after the decision, it closes the
// connection and logs synthetic-unmapped.
func (s *pinSink) ServeStream(conn net.Conn, f netd.Flow) {
	name, done, ok := s.p.Open(f.Dest.Addr())
	if ok && name != s.name {
		done()
		ok = false
	}
	if !ok {
		_ = conn.Close()
		attrs := append([]slog.Attr{
			slog.String("decision", string(netd.Reset)),
			slog.String("rule", string(netd.RuleSyntheticUnmapped)),
		}, f.LogAttrs()...)
		s.p.log.LogAttrs(context.Background(), slog.LevelInfo, "netd reset", attrs...)
		return
	}
	s.p.deps.HandOff.HandOffStream(&pinnedConn{Conn: conn, done: done}, netd.NamedFlow{Flow: f, Name: name})
}

// pinnedConn is a connection that holds its entry's pin until it is
// closed.
type pinnedConn struct {
	net.Conn
	done func()
	once sync.Once
}

// Close closes the connection and ends the pin.
func (c *pinnedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.done)
	return err
}
