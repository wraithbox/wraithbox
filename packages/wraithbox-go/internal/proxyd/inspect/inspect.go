// Package inspect terminates the guest's TLS on an inspected host and
// port, and opens wb-proxyd's own TLS connection upstream
// (S07-07-hosts-and-modes, "inspect"; S07-15-non-http-streams, "Inside
// TLS on an inspected host"; S07-16-tls-and-tunnels). Its Terminator is
// the stream package's Inspector.
//
// The guest's handshake uses a leaf from the VM's CA, offers only
// http/1.1, asks for no client certificate, holds no ECH keys and issues
// no session tickets. The handshake and the first request's line run
// under the first-bytes deadline from the hand-off and within the
// first-bytes cap (SEC13-bounded-resources). A handshake that fails
// resets the stream with a rule that says why: the client's ECH
// (tls-ech-refused), the client's rejection of the leaf
// (tls-client-rejected-leaf), or bytes that aren't TLS and HTTP/1
// (stream-protocol-mismatch). Then the stream goes to the HTTP handler,
// which calls Upstream at its first request that passes its checks, so
// a refused request opens no upstream connection.
//
// Upstream validates the server against the host's system trust store
// and never presents a client certificate (SEC04-no-guest-secrets). Its
// refusals carry their rule (upstream-tls-verify,
// tls-client-cert-unsupported, upstream-tls-failed), which the HTTP
// handler names in its 502.
//
// Every byte read here is the guest's, or a server's the guest picked,
// so each check fails closed (SEC05-default-deny), and each refusal is
// logged with its rule (SEC10-audit). Audit mode relaxes none of them
// (S07-16-tls-and-tunnels, "Not relaxed by audit mode or approvals").
// The package has no OS code.
package inspect

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/ca"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// The rules of this package (S07-16-tls-and-tunnels, S07-07-hosts-and-modes).
const (
	// RuleECHRefused: the guest's client sent a real ECH extension, and
	// aborted with an ech_required alert when the handshake went on with
	// its outer ClientHello (S07-16-tls-and-tunnels, "Encrypted
	// ClientHello").
	RuleECHRefused = "tls-ech-refused"
	// RuleClientRejectedLeaf: the guest's client sent an alert during the
	// handshake, or closed after the server's flight and before its first
	// request byte. Most often it doesn't trust the VM's CA, the known gap
	// of FR08-no-proxy-config (S09-08-guest-trust, "Fails closed").
	RuleClientRejectedLeaf = "tls-client-rejected-leaf"
	// RuleClientCertUnsupported: the upstream server asked for a client
	// certificate, and failed the handshake or its first read without one
	// (S07-16-tls-and-tunnels, "TLS with a client certificate").
	RuleClientCertUnsupported = "tls-client-cert-unsupported"
	// RuleUpstreamVerify: the upstream server's certificate failed
	// verification against the system trust store.
	RuleUpstreamVerify = "upstream-tls-verify"
	// RuleUpstreamFailed: the upstream handshake failed for another
	// reason, such as no common version or a timeout.
	RuleUpstreamFailed = "upstream-tls-failed"
)

// CheckHandshake: the guest's handshake failed for a reason that isn't
// an alert or a close, such as a malformed record. It goes with
// stream-protocol-mismatch.
const CheckHandshake egressmsg.Check = "tls-handshake"

// knownGap is the requirement whose known gap explains a rejected leaf,
// for `wb policy explain` to print (S09-08-guest-trust, "Fails closed").
const knownGap = "FR08-no-proxy-config"

// upstreamHandshakeTimeout bounds the upstream TLS handshake
// (SEC13-bounded-resources).
const upstreamHandshakeTimeout = 10 * time.Second

// Leaves gives the leaf for an inspected stream, or a refusal with its
// rule. *ca.Authority implements it (S09-07-ca-certificates, "Leaf
// issuance").
type Leaves interface {
	For(req ca.LeafRequest) (*tls.Certificate, *ca.Refusal)
}

var _ Leaves = (*ca.Authority)(nil)

// HTTPHandler takes each stream whose first request line arrived: M3.6b
// serves HTTP/1.1 on it. It owns the stream, and ends it through
// s.Inspected.Control. Its contract:
//
//   - It applies http-header-timeout to the first request's headers too:
//     the first-byte deadline is cleared once the request line arrived,
//     by Inspect on TLS and by the stream package on plain HTTP
//     (S07-15-non-http-streams, "Inside TLS on an inspected host").
//   - It calls Terminator.Upstream only after a request passed its
//     checks, so no byte of a refused request leaves the host.
type HTTPHandler interface {
	Serve(s *Stream)
}

// Stream is an inspected stream after its handshake, or a plain HTTP
// one.
type Stream struct {
	Inspected *stream.Inspected
	// Conn carries the guest's HTTP: the terminated TLS connection, or
	// the stream's own on plain HTTP. Its reads return the first request
	// line's bytes first.
	Conn net.Conn
	// TLS reports whether the guest's stream was TLS, so Upstream opens
	// TLS upstream.
	TLS bool
}

// Config is what New needs.
type Config struct {
	// Leaves gives each TLS stream's leaf.
	Leaves Leaves
	// Dialer opens every upstream connection.
	Dialer stream.Dialer
	// HTTP takes each stream after its first request line.
	HTTP HTTPHandler
	// Audit takes each refusal, or is nil until M3.10 connects it.
	Audit handoff.Audit
	// Log is the logger; nil means slog.Default(). Refusals go through
	// internal/ratelog with LogWindow.
	Log *slog.Logger
	// LogWindow is ratelog's window; zero means ratelog.Window.
	LogWindow time.Duration
}

// Terminator terminates the TLS of inspected streams. It implements
// stream.Inspector.
type Terminator struct {
	leaves   Leaves
	dialer   stream.Dialer
	http     HTTPHandler
	audit    handoff.Audit
	log      *slog.Logger
	refusals *slog.Logger

	// roots verifies upstream servers. nil means the system trust store
	// (S07-07-hosts-and-modes, "inspect"). Only this package's tests set
	// it, and never to a Wraith Box CA (S09-07-ca-certificates, "Not on
	// the host").
	roots *x509.CertPool
	// firstBytesTimeout and firstBytesCap are the stream package's;
	// tests shorten them.
	firstBytesTimeout time.Duration
	firstBytesCap     int
}

var _ stream.Inspector = (*Terminator)(nil)

// New returns a Terminator, or an error when Leaves, Dialer or HTTP is
// nil.
func New(cfg Config) (*Terminator, error) {
	if cfg.Leaves == nil || cfg.Dialer == nil || cfg.HTTP == nil {
		return nil, errors.New("inspect: Leaves, a Dialer and an HTTP handler are required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	window := cfg.LogWindow
	if window == 0 {
		window = ratelog.Window
	}
	return &Terminator{
		leaves:            cfg.Leaves,
		dialer:            cfg.Dialer,
		http:              cfg.HTTP,
		audit:             cfg.Audit,
		log:               log,
		refusals:          ratelog.NewAt(log, window, time.Now),
		firstBytesTimeout: stream.FirstBytesTimeout,
		firstBytesCap:     stream.FirstBytesCap,
	}, nil
}

// Inspect takes an inspected stream. A plain HTTP stream goes to the
// HTTP handler as it is. A TLS stream gets its leaf, its handshake and
// its first request line, and then goes to the HTTP handler. Any
// refusal on the way resets it with its rule.
func (t *Terminator) Inspect(s *stream.Inspected) {
	if s.Protocol != policy.StreamTLS {
		t.http.Serve(&Stream{Inspected: s, Conn: s.Conn})
		return
	}
	leaf, ref := t.leaves.For(ca.LeafRequest{
		VM:         s.VM,
		Generation: s.Generation,
		Name:       s.Name.String(),
		Port:       s.Port,
		Policy:     s.Control.Policy(),
	})
	if ref == nil && leaf == nil {
		// A Leaves that returns neither breaks its contract. The stream
		// fails closed (S09-07-ca-certificates, "Leaf issuance").
		ref = &ca.Refusal{Rule: ca.RuleLeafRefused, Reason: "no leaf"}
	}
	if ref != nil {
		t.reset(s, ref.Rule, "", slog.String("reason", ref.Reason))
		return
	}
	conn, ok := t.handshake(s, leaf)
	if !ok {
		return
	}
	buf, ok := t.firstRequest(s, conn)
	if !ok {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	t.http.Serve(&Stream{Inspected: s, Conn: stream.Replay(conn, buf), TLS: true})
}

// serverConfig is the guest handshake's configuration
// (S07-07-hosts-and-modes, "inspect"): TLS 1.2 and 1.3, http/1.1 only
// until HTTP/2 (I499), no client certificate, no ECH keys, so a real ECH
// extension is rejected and the client aborts (S07-16-tls-and-tunnels,
// "Encrypted ClientHello"), and no session tickets, so each stream's
// handshake checks the leaf of the CA in use at the time.
func serverConfig(leaf *tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates:           []tls.Certificate{*leaf},
		MinVersion:             tls.VersionTLS12,
		MaxVersion:             tls.VersionTLS13,
		NextProtos:             []string{stream.NextProtoHTTP1},
		ClientAuth:             tls.NoClientCert,
		SessionTicketsDisabled: true,
	}
}

// reset resets s with rule, and logs it if the reset ended the stream.
func (t *Terminator) reset(s *stream.Inspected, rule string, check egressmsg.Check, attrs ...slog.Attr) {
	if s.Control.Reset() {
		t.refused(s, decisionReset, rule, check, attrs...)
	}
}
