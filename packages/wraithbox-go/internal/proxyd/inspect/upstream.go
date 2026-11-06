package inspect

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync/atomic"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
)

// Refusal is the error of an upstream TLS connection that Upstream
// refused, with its rule. The HTTP handler answers the guest's request
// with a 502 that names Rule only, never Error(): the error can hold
// text from the server, which the log cuts as guest text.
type Refusal struct {
	Rule string
	err  error
}

func (r *Refusal) Error() string { return r.Rule + ": " + r.err.Error() }

// Unwrap returns the TLS error.
func (r *Refusal) Unwrap() error { return r.err }

// Upstream opens the upstream connection of s, through the checked
// dialer. The HTTP handler calls it at the stream's first request that
// passes its checks, and not before (S07-15-non-http-streams, "Inside TLS
// on an inspected host"). ctx ends with the stream.
//
// A plain HTTP stream gets the dialed connection. A TLS stream gets a
// TLS connection that offers http/1.1, whatever the guest negotiated,
// and verifies the server against the system trust store for the
// stream's name (S07-07-hosts-and-modes, "inspect"). It presents no
// client certificate (SEC04-no-guest-secrets): a server that asks for
// one and then fails the handshake, or sends a certRefused alert on the
// first read, gives tls-client-cert-unsupported (S07-16-tls-and-tunnels,
// "TLS with a client certificate"). A certificate that fails
// verification gives upstream-tls-verify, and another handshake failure,
// a timeout included, upstream-tls-failed.
// A dial refusal is the dialer's error, with its rule.
func (t *Terminator) Upstream(ctx context.Context, s *Stream) (net.Conn, error) {
	host := s.Inspected.Name.String()
	raw, err := t.dialer.Dial(ctx, host, s.Inspected.Port)
	if err != nil {
		return nil, err
	}
	if !s.TLS {
		return raw, nil
	}
	asked := &atomic.Bool{}
	conn := tls.Client(raw, &tls.Config{
		ServerName: host,
		RootCAs:    t.roots,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{stream.NextProtoHTTP1},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			asked.Store(true)
			return &tls.Certificate{}, nil
		},
	})
	handshakeCtx, cancel := context.WithTimeout(ctx, upstreamHandshakeTimeout)
	defer cancel()
	if err := conn.HandshakeContext(handshakeCtx); err != nil {
		_ = raw.Close()
		return nil, t.upstreamRefused(s.Inspected, upstreamRule(err, asked.Load()), err)
	}
	return &upstreamConn{Conn: conn, t: t, s: s.Inspected, asked: asked.Load()}, nil
}

// upstreamRule classifies an upstream handshake error. asked reports
// whether the server asked for a client certificate.
// A timeout, or the stream's end, is upstream-tls-failed even after a
// certificate request: the server didn't refuse the missing certificate.
func upstreamRule(err error, asked bool) string {
	var verify *tls.CertificateVerificationError
	switch {
	case errors.As(err, &verify):
		return RuleUpstreamVerify
	case timedOut(err):
		return RuleUpstreamFailed
	case asked:
		return RuleClientCertUnsupported
	}
	return RuleUpstreamFailed
}

// timedOut reports whether err is a deadline, a timeout, or a canceled
// context.
func timedOut(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// certRefused reports whether err is an alert the server sends when it
// refuses a client without a certificate: bad_certificate,
// certificate_required or handshake_failure.
func certRefused(err error) bool {
	code, ok := receivedAlert(err)
	return ok && (code == alertBadCertificate || code == alertCertificateRequired || code == alertHandshakeFailure)
}

// The alert codes of certRefused.
const (
	alertHandshakeFailure    = 40
	alertBadCertificate      = 42
	alertCertificateRequired = 116
)

// upstreamRefused logs a refused upstream connection with its rule, and
// returns its Refusal.
func (t *Terminator) upstreamRefused(s *stream.Inspected, rule string, err error) *Refusal {
	// The error can hold names from the server's certificate, which the
	// guest picked, so it is cut as guest text.
	t.refused(s, decisionUpstream, rule, "", slog.String("error", guesttext.LogField(err.Error(), logCut)))
	return &Refusal{Rule: rule, err: err}
}

// upstreamConn is an upstream TLS connection whose first read fails with
// tls-client-cert-unsupported when the server asked for a client
// certificate and sent a certRefused alert before its first byte. With
// TLS 1.3 the client's handshake completes before the server checks its
// certificate, so the server's refusal arrives on that read. After a
// certificate request, another error there, such as an end or a reset,
// is upstream-tls-failed, and a timeout is returned as it is.
type upstreamConn struct {
	*tls.Conn
	t     *Terminator
	s     *stream.Inspected
	asked bool
	read  bool
}

func (c *upstreamConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	first := !c.read
	c.read = c.read || n > 0 || err != nil
	if err == nil || n > 0 || !first || !c.asked || timedOut(err) {
		return n, err
	}
	rule := RuleUpstreamFailed
	if certRefused(err) {
		rule = RuleClientCertUnsupported
	}
	return 0, c.t.upstreamRefused(c.s, rule, err)
}
