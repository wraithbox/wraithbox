package synth

import (
	"log/slog"
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
	case !p.portAllowed(name, f.Dest.Port()):
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

// ServeStream pins the entry of the connection's address, checks the
// decision again, and hands the connection on with the name. The
// handshake took time, so the decision may no longer hold. When the
// entry was released or the address went to another name, it closes the
// connection with synthetic-unmapped. When the policy no longer allows
// the port, it closes it with port-not-allowed. Each close is recorded
// with its rule (SEC05-default-deny, SEC10-audit).
func (s *pinSink) ServeStream(conn netd.StreamConn, f netd.Flow) {
	name, done, ok := s.p.Open(f.Dest.Addr())
	rule := netd.Rule("")
	switch {
	case !ok:
		rule = netd.RuleSyntheticUnmapped
	case name != s.name:
		done()
		rule = netd.RuleSyntheticUnmapped
	case !s.p.portAllowed(name, f.Dest.Port()):
		done()
		rule = netd.RulePortNotAllowed
	}
	if rule != "" {
		_ = conn.Close()
		s.p.rec.Record(netd.Close, rule, append(f.LogAttrs(), slog.String("name", s.name.String()))...)
		return
	}
	s.p.deps.HandOff.HandOffStream(&pinnedConn{StreamConn: conn, done: done}, netd.NamedFlow{Flow: f, Name: name})
}

// portAllowed asks the port policy. Without one no port is allowed.
func (p *Pool) portAllowed(name dnsname.Name, port uint16) bool {
	return p.deps.Ports != nil && p.deps.Ports.PortAllowed(name, port)
}

// pinnedConn is a connection that holds its entry's pin until it is
// closed.
type pinnedConn struct {
	netd.StreamConn
	done func()
	once sync.Once
}

// Close closes the connection and ends the pin.
func (c *pinnedConn) Close() error {
	err := c.StreamConn.Close()
	c.once.Do(c.done)
	return err
}
