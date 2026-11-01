package netd

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// Rule names the rule behind a drop or a reset. It is the "rule"
// attribute of the log record, and the key the drop is counted under.
type Rule string

// The rules of the link endpoint and its filter, in the order of
// S07-02-packet-path, "Link filter" and "Stack".
const (
	// RuleFrameTruncated: a read filled the buffer of MTU + 15 bytes, so
	// the datagram was longer than the largest frame and arrived cut
	// ("Stack").
	RuleFrameTruncated Rule = "frame-truncated"
	// RuleFrameSize: step 1, a frame shorter than 14 or longer than
	// MTU + 14 bytes.
	RuleFrameSize Rule = "frame-size"
	// RuleFrameMAC: step 2, a source MAC other than the guest's, or a
	// destination MAC other than the gateway's or broadcast.
	RuleFrameMAC Rule = "frame-mac"
	// RuleFrameEtherType: step 3, an EtherType other than ARP and IPv4,
	// IPv6 included.
	RuleFrameEtherType Rule = "frame-ethertype"
	// RuleARPForm: step 4, ARP that isn't Ethernet/IPv4, isn't a request
	// or reply, or names a sender other than the guest's MAC and leased
	// address, or a target other than the gateway.
	RuleARPForm Rule = "arp-form"
	// RuleIPv4Form: step 5, an IPv4 header with options, a fragment, or
	// a header that doesn't parse: version, length or checksum.
	RuleIPv4Form Rule = "ipv4-form"
	// RuleIPv4Source: step 5, a source other than the leased address,
	// or 0.0.0.0 on anything but DHCP from port 68 to port 67.
	RuleIPv4Source Rule = "ipv4-source"
	// RuleIPBroadcast: step 6, multicast, or broadcast other than DHCP.
	RuleIPBroadcast Rule = "ip-broadcast"
	// RuleUDPNotDNS: step 6, UDP other than to the gateway's port 53 and
	// other than DHCP from port 68 to port 67 at the gateway or
	// broadcast.
	RuleUDPNotDNS Rule = "udp-not-dns"
	// RuleICMPNotEcho: step 6, ICMP other than an echo request to the
	// gateway.
	RuleICMPNotEcho Rule = "icmp-not-echo"
	// RuleIPProtocol: step 6, an IP protocol other than TCP, UDP and
	// ICMP.
	RuleIPProtocol Rule = "ip-protocol"

	// RuleLinkQueueFull: an outbound frame over the queue's frame or
	// byte cap ("Per-VM caps").
	RuleLinkQueueFull Rule = "link-queue-full"
	// RuleLinkWriteFailed: an outbound frame the transport didn't take,
	// after the retries for a full peer buffer.
	RuleLinkWriteFailed Rule = "link-write-failed"
)

// The rules of the stack, after S07-02-packet-path, "Connections" and
// "Per-VM caps".
const (
	// RuleGatewayPortRefused: TCP to the gateway on a port other than
	// 53, or on port 53 while no DNS-over-TCP server is bound.
	RuleGatewayPortRefused Rule = "gateway-port-refused"
	// RuleSyntheticUnmapped: TCP to a synthetic address while no stream
	// policy is bound.
	RuleSyntheticUnmapped Rule = "synthetic-unmapped"
	// RuleStreamRefused: the stream policy accepted a connection without
	// a sink, or refused it without a rule.
	RuleStreamRefused Rule = "stream-refused"
	// RuleTCPDestinationRefused: TCP to an address that is neither the
	// gateway nor synthetic.
	RuleTCPDestinationRefused Rule = "tcp-destination-refused"
	// RuleTCPCap: a SYN over the cap of established connections.
	RuleTCPCap Rule = "tcp-cap"
	// RuleTCPHalfOpenCap: a SYN over the cap of half-open connections.
	RuleTCPHalfOpenCap Rule = "tcp-half-open-cap"
	// RuleTCPHandshakeFailed: an accepted SYN whose handshake didn't
	// complete.
	RuleTCPHandshakeFailed Rule = "tcp-handshake-failed"
	// RuleUDPUnbound: UDP the filter passed to a port nothing is bound
	// to, such as DHCP before the DHCP server binds port 67. It is
	// dropped without an ICMP port unreachable.
	RuleUDPUnbound Rule = "udp-unbound"
)

// Decision is what happened to a frame or connection: a drop, or a
// reset sent to its SYN.
type Decision string

// The decisions a Recorder counts.
const (
	Drop  Decision = "drop"
	Reset Decision = "reset"
)

// Events receives every drop and reset with its rule, the count of the
// rule so far, and the fields it was logged with (S07-02-packet-path,
// "Events and rate limits"). wb-netd's event channel implements it
// (S04-08-records-and-bounds). It is called on the packet path, so it
// returns at once.
type Events interface {
	Event(d Decision, rule Rule, count uint64, attrs []slog.Attr)
}

// Recorder counts every drop and reset per rule, logs it through a
// ratelog handler, at most one record per rule per window, and passes
// it to Events (S07-02-packet-path, "Events and rate limits",
// SEC10-audit, SEC13-bounded-resources). It is safe for concurrent use.
type Recorder struct {
	log    *slog.Logger
	events Events

	mu     sync.Mutex
	counts map[Rule]uint64
}

// NewRecorder returns a Recorder that logs through log, nil for
// slog.Default, rate-limited per rule, and calls events, which may be
// nil.
func NewRecorder(log *slog.Logger, events Events) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{log: ratelog.New(log), events: events, counts: map[Rule]uint64{}}
}

// NewRecorderWith is NewRecorder with a logger used as it is, without
// the rate limit, for tests.
func NewRecorderWith(log *slog.Logger, events Events) *Recorder {
	return &Recorder{log: log, events: events, counts: map[Rule]uint64{}}
}

// Record counts one drop or reset with its rule, logs it with attrs,
// and passes it to Events.
func (r *Recorder) Record(d Decision, rule Rule, attrs ...slog.Attr) {
	r.mu.Lock()
	r.counts[rule]++
	n := r.counts[rule]
	r.mu.Unlock()
	all := append([]slog.Attr{
		slog.String("decision", string(d)),
		slog.String("rule", string(rule)),
		slog.Uint64("count", n),
	}, attrs...)
	r.log.LogAttrs(context.Background(), slog.LevelInfo, "netd "+string(d), all...)
	if r.events != nil {
		r.events.Event(d, rule, n, attrs)
	}
}

// Count returns how many drops and resets of a rule were recorded.
func (r *Recorder) Count(rule Rule) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[rule]
}
