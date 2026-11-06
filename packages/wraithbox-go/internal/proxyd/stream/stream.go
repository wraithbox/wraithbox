// Package stream decides what each stream wb-netd hands to wb-proxyd is
// before any byte of it leaves the host (S07-15-non-http-streams,
// S07-07-hosts-and-modes, S07-16-tls-and-tunnels). It implements
// handoff.StreamHandler.
//
// The stream's host and port pick one protocol and mode from the
// effective policy, and nothing on the stream chooses it
// (S07-15-non-http-streams, "One protocol per host and port"):
//
//   - raw TCP: dialed and relayed both ways unchanged, with no
//     first-byte check ("Raw TCP entries");
//   - plain HTTP on port 80: the first bytes must be an HTTP/1.1 or
//     HTTP/1.0 request line, and the stream goes to the Inspector;
//   - TLS: the first bytes must be a ClientHello whose server name is
//     the stream's name (S07-07-hosts-and-modes, "Name binding"). A pass
//     host's stream goes to the pass relay, which checks the client's
//     records up to the server's ServerHello (S07-16-tls-and-tunnels,
//     "Pass"). Any other goes to the Inspector, once its ALPN list allows
//     HTTP (S07-15-non-http-streams, "Inside TLS on an inspected host").
//
// Every byte read here is the guest's, or an upstream server's the guest
// picked, so each check fails closed (SEC05-default-deny). Each
// stream's final decision is logged with the rule that made it
// (SEC10-audit): a reset through ratelog per rule, with the protocol the
// first bytes looked like and never the bytes, and given to the audit
// hook; an allowed stream as one record per stream. The first-byte read
// is bounded by a byte cap and a deadline (SEC13-bounded-resources).
// Audit mode relaxes none of this (S07-16-tls-and-tunnels, "Not relaxed
// by audit mode or approvals"). The package has no OS code.
package stream

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/upstream"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// The rules of this package. Each log record of a decision names one in
// its "rule" attribute.
const (
	// RuleProtocolMismatch: the first bytes aren't the protocol the host
	// and port expect, or aren't decided within the byte cap; or a pass
	// relay's client sent early data, a handshake record that isn't a
	// ClientHello, application data, or a second change cipher spec
	// before the ServerHello (S07-15-non-http-streams, "First bytes";
	// S07-16-tls-and-tunnels, "Pass").
	RuleProtocolMismatch = "stream-protocol-mismatch"
	// RuleFirstBytesTimeout: the first bytes weren't decided by the
	// deadline from the hand-off (S07-15-non-http-streams, "First
	// bytes").
	RuleFirstBytesTimeout = "stream-first-bytes-timeout"
	// RuleSNIMismatch: a ClientHello with no server name, one outside
	// the name form, or another name than the stream's
	// (S07-07-hosts-and-modes, "Name binding").
	RuleSNIMismatch = "tls-sni-mismatch"
	// RuleECHPass: an encrypted_client_hello or encrypted SNI extension
	// to a pass host (S07-16-tls-and-tunnels, "Pass").
	RuleECHPass = "tls-ech-pass" //nolint:gosec // G101: a rule name, not a credential.
	// RuleExtensionUnknown: a ClientHello extension to a pass host
	// outside the list of tlsparse.KnownExtension
	// (S07-16-tls-and-tunnels, "Pass").
	RuleExtensionUnknown = "tls-extension-unknown"
	// RuleClientAlert: an alert from a pass relay's client before the
	// ServerHello. The relay closes both ways and relays nothing
	// (S07-16-tls-and-tunnels, "Pass").
	RuleClientAlert = "tls-client-alert"

	// RulePassAllowed: a pass relay's checks up to its dial passed, and
	// the ClientHello goes upstream.
	RulePassAllowed = "pass-allowed"
	// RuleRawAllowed: a raw TCP entry's relay connected upstream.
	RuleRawAllowed = "raw-tcp-allowed"
	// RuleInspectAllowed: an inspected stream passed its first-byte
	// checks and went to the Inspector, which decides each request.
	RuleInspectAllowed = "inspect-allowed"
)

// The checks of this package, in a refusal's "check" attribute, with
// the checks of tlsparse.
const (
	// CheckProtocol: the host and port's protocol isn't the one the
	// stream was dispatched by, or there is none.
	CheckProtocol egressmsg.Check = "protocol"
	// CheckPassMode: a TLS host went into or out of pass mode.
	CheckPassMode egressmsg.Check = "pass-mode"
	// CheckRequestLine: not an HTTP/1.1 or HTTP/1.0 request line.
	CheckRequestLine egressmsg.Check = "request-line"
	// CheckEnded: the client ended the stream before its first bytes
	// were decided.
	CheckEnded egressmsg.Check = "first-bytes-ended"
	// CheckALPN: an ALPN list that names neither h2 nor http/1.1.
	CheckALPN egressmsg.Check = "alpn"
	// CheckEarlyData: an early_data extension, or application data,
	// before a pass host's ServerHello.
	CheckEarlyData egressmsg.Check = "early-data"
	// CheckClientRecord: a client record before the ServerHello that a
	// pass relay doesn't take.
	CheckClientRecord egressmsg.Check = "client-record"
	// CheckChangeCipherSpec: a second change cipher spec, or one that
	// isn't the one byte 0x01.
	CheckChangeCipherSpec egressmsg.Check = "change-cipher-spec"
	// CheckServerRecords: the server's records before its ServerHello
	// that tlsparse.ServerFlight refused, or a ServerHello after a
	// HelloRetryRequest before the second ClientHello.
	CheckServerRecords egressmsg.Check = "server-records"
)

// Bounds fixed in code (S07-15-non-http-streams, "First bytes";
// SEC13-bounded-resources).
const (
	// FirstBytesCap is the most wb-proxyd reads of a stream before it
	// decides its first bytes, and the most a ClientHello flight may
	// take, the second one of a pass relay included.
	FirstBytesCap = 16 << 10
	// FirstBytesTimeout runs from the hand-off to the first-byte
	// decision.
	FirstBytesTimeout = 10 * time.Second
	// ServerFlightCap is the most of a pass host's record fragments
	// wb-proxyd reads before its ServerHello.
	ServerFlightCap = 64 << 10
)

// Mode is the mode a stream is handled in.
type Mode int

// The modes. ModeNone is a raw TCP stream's.
const (
	ModeNone Mode = iota
	ModeInspect
	ModeAudit
	ModePass
)

func (m Mode) String() string {
	switch m {
	case ModeInspect:
		return "inspect"
	case ModeAudit:
		return "audit"
	case ModePass:
		return "pass"
	case ModeNone:
	}
	return "none"
}

// Dialer opens upstream connections. *upstream.Dialer is the one
// wb-proxyd uses, which refuses an address of S07-07-hosts-and-modes,
// "Upstream address". A refusal is an *upstream.Refusal.
type Dialer interface {
	Dial(ctx context.Context, host string, port uint16) (net.Conn, error)
}

// Control is what a handler does to a handed-off stream.
// *handoff.Stream implements it.
type Control interface {
	Reset()
	Close()
	AddCheck(check func(*policy.Policy) egressmsg.Check) egressmsg.Check
	Policy() *policy.Policy
}

// Inspected is a stream that passed its first-byte checks on an
// inspected host and port.
type Inspected struct {
	VM         string
	Generation uint64
	ID         uint64
	Name       dnsname.Name
	Port       uint16
	// Protocol is policy.StreamTLS or policy.StreamPlainHTTP.
	Protocol policy.StreamProtocol
	// Mode is ModeInspect or ModeAudit.
	Mode Mode
	// Hello is the ClientHello, and nil on plain HTTP.
	Hello *tlsparse.ClientHello
	// Conn is the stream's connection. Its reads return the bytes
	// wb-proxyd read for the first-byte check first.
	Conn net.Conn
	// Control ends the stream and registers checks on it.
	Control Control
}

// Inspector takes each inspected stream: M3.6 terminates its TLS and
// applies HTTP policy. It owns the stream, and ends it through Control.
// It is called on the stream's own goroutine.
type Inspector interface {
	Inspect(s *Inspected)
}

// Config is what New needs.
type Config struct {
	// Dialer opens every upstream connection.
	Dialer Dialer
	// Inspector takes each inspected stream.
	Inspector Inspector
	// Audit takes each reset, or is nil until M3.10 connects it.
	Audit handoff.Audit
	// Log is the logger; nil means slog.Default(). Resets go through
	// internal/ratelog with LogWindow.
	Log *slog.Logger
	// LogWindow is ratelog's window; 0 means ratelog.Window.
	LogWindow time.Duration
}

// The hand-off's types fit this package's.
var (
	_ handoff.StreamHandler = (*Handler)(nil)
	_ Control               = (*handoff.Stream)(nil)
)

// Handler is wb-proxyd's handoff.StreamHandler.
type Handler struct {
	dialer    Dialer
	inspector Inspector
	audit     handoff.Audit
	// log takes the record of each allowed stream, one per stream, so
	// it isn't rate-limited: the hand-off's stream cap bounds them.
	log *slog.Logger
	// refusals takes resets, rate-limited per rule.
	refusals *slog.Logger

	// The bounds, which only tests in this package change.
	firstBytesTimeout time.Duration
	firstBytesCap     int
	serverFlightCap   int
}

// New returns a Handler.
func New(cfg Config) (*Handler, error) {
	if cfg.Dialer == nil || cfg.Inspector == nil {
		return nil, errors.New("stream: a Dialer and an Inspector are required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	window := cfg.LogWindow
	if window == 0 {
		window = ratelog.Window
	}
	return &Handler{
		dialer:            cfg.Dialer,
		inspector:         cfg.Inspector,
		audit:             cfg.Audit,
		log:               log,
		refusals:          ratelog.NewAt(log, window, time.Now),
		firstBytesTimeout: FirstBytesTimeout,
		firstBytesCap:     FirstBytesCap,
		serverFlightCap:   ServerFlightCap,
	}, nil
}

// HandleStream starts the stream's own goroutine and returns, as
// handoff.StreamHandler asks.
func (h *Handler) HandleStream(s *handoff.Stream) {
	st := &stream{
		vm: s.VM, gen: s.Generation, id: s.ID, name: s.Name, port: s.Port,
		conn: s.Conn, ctl: s, began: time.Now(),
	}
	go h.serve(st)
}

// stream is one stream on its goroutine.
type stream struct {
	vm   string
	gen  uint64
	id   uint64
	name dnsname.Name
	port uint16
	conn net.Conn
	ctl  Control
	// began is the hand-off, which the first-bytes deadline runs from.
	began time.Time
	// looks is what the first bytes looked like, for the log.
	looks string
}

// route is the protocol and mode the policy gives a host and port.
type route struct {
	protocol policy.StreamProtocol
	mode     Mode
}

// routeOf reads the stream's route from the effective policy
// (S07-15-non-http-streams, "One protocol per host and port";
// S07-07-hosts-and-modes, "Modes, per host"). A TLS host in the pass
// list is in pass mode on every TLS port. Otherwise a TLS or plain HTTP
// port is in audit when every endpoint that names it is, and inspected
// otherwise.
func routeOf(p *policy.Policy, name dnsname.Name, port uint16) route {
	if p == nil {
		return route{protocol: policy.StreamNone}
	}
	r := route{protocol: p.Protocol(name, port)}
	switch r.protocol {
	case policy.StreamTLS:
		if isPassHost(p, name) {
			r.mode = ModePass
			return r
		}
		r.mode = inspectMode(p, name, port)
	case policy.StreamPlainHTTP:
		r.mode = inspectMode(p, name, port)
	case policy.StreamRawTCP, policy.StreamNone:
	}
	return r
}

func inspectMode(p *policy.Policy, name dnsname.Name, port uint16) Mode {
	if policy.Mode(p.Endpoints, name, port) == policy.ModeAudit {
		return ModeAudit
	}
	return ModeInspect
}

func isPassHost(p *policy.Policy, name dnsname.Name) bool {
	_, ok := passEntry(p, name)
	return ok
}

func passEntry(p *policy.Policy, name dnsname.Name) (policy.PassHost, bool) {
	for _, h := range p.PassHosts {
		if h.Host == name {
			return h, true
		}
	}
	return policy.PassHost{}, false
}

func rawEntry(p *policy.Policy, name dnsname.Name, port uint16) (policy.RawTCP, bool) {
	for _, r := range p.RawTCP {
		if r.Host == name && r.Port == port {
			return r, true
		}
	}
	return policy.RawTCP{}, false
}

// sameRoute is the check each stream registers for its life
// (S07-01-per-vm-policy, "Open streams"): the host and port keep the
// protocol the stream was dispatched by, and a TLS host stays in, or out
// of, pass mode. So a raw relay ends when its entry is gone, and a pass
// relay when its host leaves the pass list. A move between inspect and
// audit is left to the Inspector, which decides each request by the
// current mode.
func sameRoute(name dnsname.Name, port uint16, was route) func(*policy.Policy) egressmsg.Check {
	return func(p *policy.Policy) egressmsg.Check {
		now := routeOf(p, name, port)
		if now.protocol != was.protocol {
			return CheckProtocol
		}
		if (now.mode == ModePass) != (was.mode == ModePass) {
			return CheckPassMode
		}
		return ""
	}
}

// serve decides one stream by its route.
func (h *Handler) serve(st *stream) {
	r := routeOf(st.ctl.Policy(), st.name, st.port)
	if check := st.ctl.AddCheck(sameRoute(st.name, st.port, r)); check != "" {
		h.reset(st, handoff.RulePolicyRecomputed, check)
		return
	}
	switch r.protocol {
	case policy.StreamRawTCP:
		h.raw(st)
	case policy.StreamTLS:
		h.tls(st, r)
	case policy.StreamPlainHTTP:
		h.plain(st, r)
	case policy.StreamNone:
		h.reset(st, RuleProtocolMismatch, CheckProtocol)
	}
}

// tls checks a TLS stream's ClientHello and its binding, and sends it to
// the pass relay or the Inspector.
func (h *Handler) tls(st *stream, r route) {
	first, ok := h.readFirst(st, h.decideTLS)
	if !ok {
		return
	}
	name, ok := first.hello.Name()
	if !ok || name != st.name {
		h.reset(st, RuleSNIMismatch, sniCheck(first.hello, ok))
		return
	}
	if r.mode == ModePass {
		h.pass(st, first)
		return
	}
	if !alpnAllowsHTTP(first.hello.ALPN) {
		h.reset(st, RuleProtocolMismatch, CheckALPN)
		return
	}
	h.inspect(st, r, first)
}

// sniCheck names what failed in the name binding.
func sniCheck(ch *tlsparse.ClientHello, formOK bool) egressmsg.Check {
	switch {
	case !ch.HasServerName:
		return "no-server-name"
	case !formOK:
		return "server-name-form"
	}
	return "server-name-differs"
}

// alpnAllowsHTTP reports whether an ALPN list is empty or names h2 or
// http/1.1, the only protocols wb-proxyd offers
// (S07-15-non-http-streams, "Inside TLS on an inspected host").
func alpnAllowsHTTP(alpn []string) bool {
	if len(alpn) == 0 {
		return true
	}
	for _, p := range alpn {
		if p == "h2" || p == "http/1.1" {
			return true
		}
	}
	return false
}

// plain checks a plain HTTP stream's request line and sends it to the
// Inspector.
func (h *Handler) plain(st *stream, r route) {
	first, ok := h.readFirst(st, h.decideHTTP)
	if !ok {
		return
	}
	h.inspect(st, r, first)
}

// inspect hands a stream to the Inspector, with its first bytes to read
// again.
func (h *Handler) inspect(st *stream, r route, first firstBytes) {
	h.allowed(st, RuleInspectAllowed, r.mode, slog.String("protocol", r.protocol.String()))
	h.inspector.Inspect(&Inspected{
		VM: st.vm, Generation: st.gen, ID: st.id, Name: st.name, Port: st.port,
		Protocol: r.protocol, Mode: r.mode, Hello: first.hello,
		Conn:    &replayConn{Conn: st.conn, pending: first.buf},
		Control: st.ctl,
	})
}

// dial opens the upstream connection of a pass or raw stream. A refusal
// resets the stream with the dialer's rule (S07-07-hosts-and-modes,
// "Upstream address"), and an error that isn't one with
// upstream-connect-failed.
func (h *Handler) dial(st *stream) (net.Conn, bool) {
	up, err := h.dialer.Dial(context.Background(), st.name.String(), st.port)
	if err == nil {
		return up, true
	}
	rule := string(upstream.RuleConnectFailed)
	if ref, ok := errors.AsType[*upstream.Refusal](err); ok {
		rule = string(ref.Rule)
	}
	h.reset(st, rule, "upstream", slog.String("reason", guesttext.LogField(err.Error(), 2*logCut)))
	return nil, false
}
