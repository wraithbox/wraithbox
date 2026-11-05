package dns

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// Handle reads one message that arrived at arrival, decides it, and
// sends its response with send: at once, or held until Hold after
// arrival. A message that fails the form, and a refusal over the
// held-query cap, get no response. Each decision is logged with its
// rule, and each refusal and drop counted through Events
// (S07-02-packet-path, "Events and rate limits", SEC10-audit). from
// names the sender in the log.
func (r *Resolver) Handle(ctx context.Context, msg []byte, arrival time.Time, from string, send func([]byte)) Outcome {
	src := slog.String("from", from)
	q, err := ParseQuery(msg)
	if err != nil {
		r.rec.Record(netd.Drop, netd.RuleDNSMessageForm, src, slog.Int("size", len(msg)), slog.String("reason", err.Error()))
		return Outcome{When: SendNever, Rule: netd.RuleDNSMessageForm}
	}
	o := r.Decide(ctx, q)
	attrs := append([]slog.Attr{src}, o.Attrs...)
	switch o.When {
	case SendNow:
		r.sendNow(o, attrs, send)
	case SendHeld:
		h := &HeldQuery{Query: q, Name: o.Name, Arrival: arrival, Rule: o.Rule, Response: o.Response, Debited: o.Debited}
		// S07-04-dns-refusals, "Over the cap": the debit and its record
		// were made in Decide. A query that doesn't fit raises no event.
		if !r.hold.add(h, send) {
			r.rec.Record(netd.Drop, netd.RuleDNSHoldFull, append(attrs, slog.String("reason", string(o.Rule)))...)
			return Outcome{When: SendNever, Rule: netd.RuleDNSHoldFull, Name: o.Name, Debited: o.Debited}
		}
		r.rec.Record(netd.Refuse, o.Rule, attrs...)
		if o.Approve && r.cfg.Approvals != nil {
			r.cfg.Approvals.NotAllowed(o.Name)
		}
	case SendNever:
		r.rec.Record(netd.Drop, o.Rule, attrs...)
	}
	return o
}

// sendNow sends a response that isn't held. The local answers and the
// answers to allowed names are notes, and SERVFAIL for a full pool is a
// refusal.
func (r *Resolver) sendNow(o Outcome, attrs []slog.Attr, send func([]byte)) {
	if o.Rule == netd.RuleSyntheticPoolFull {
		r.rec.Record(netd.Refuse, o.Rule, attrs...)
	} else {
		r.rec.Note(o.Rule, attrs...)
	}
	send(o.Response)
}

// HeldCount returns how many queries are held.
func (r *Resolver) HeldCount() int { return r.hold.len() }

// Close stops the timers of the held queries, so none of them is sent,
// and drops every later refusal with dns-hold-full.
func (r *Resolver) Close() { r.hold.close() }

// ServeDNSOverUDP answers the queries on conn, the gateway's UDP port 53,
// one at a time, until a read fails, which it does once the stack closes
// conn (netd.DNSOverUDP). A query waits for the one before it: the debit
// and the pool's records are each bounded by their deadlines, and the
// socket's receive buffer bounds what waits. A held refusal is sent by
// its own timer.
func (r *Resolver) ServeDNSOverUDP(conn net.PacketConn) {
	// One byte over MaxQuery, so a larger datagram fills the buffer and
	// ParseQuery drops it.
	buf := make([]byte, MaxQuery+1)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		arrival := time.Now()
		r.Handle(context.Background(), buf[:n], arrival, from.String(), r.sender(conn, from))
	}
}

// sender returns the function that sends a response to from, once.
func (r *Resolver) sender(conn net.PacketConn, from net.Addr) func([]byte) {
	var once sync.Once
	return func(b []byte) {
		once.Do(func() {
			if _, err := conn.WriteTo(b, from); err != nil {
				r.rec.Record(netd.Drop, netd.RuleDNSSendFailed, slog.String("from", from.String()), slog.String("error", err.Error()))
			}
		})
	}
}
