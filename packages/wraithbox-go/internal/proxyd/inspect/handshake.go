package inspect

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
)

// handshake runs the guest's handshake with leaf, under the first-bytes
// deadline from the hand-off (S07-15-non-http-streams, "Inside TLS on an
// inspected host"). A failure resets the stream with the rule
// handshakeErrorRule gives, and returns false.
func (t *Terminator) handshake(s *stream.Inspected, leaf *tls.Certificate) (*tls.Conn, bool) {
	_ = s.Conn.SetDeadline(s.Began.Add(t.firstBytesTimeout))
	counted := &writeCounter{Conn: s.Conn}
	conn := tls.Server(counted, serverConfig(leaf))
	// The connection's deadline bounds the handshake, and the stream's end
	// closes the connection, so no context needs to.
	if err := conn.HandshakeContext(context.Background()); err != nil {
		t.refuseHandshake(s, handshakeErrorRule(err, counted.wrote > 0))
		return nil, false
	}
	return conn, true
}

// writeCounter counts the bytes written to its connection, so a close
// after the server's flight can be told from one before it.
type writeCounter struct {
	net.Conn
	wrote int
}

func (c *writeCounter) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.wrote += n
	return n, err
}

// firstRequest reads the first request line inside TLS, within the
// first-bytes cap and by the deadline the handshake ran under. It returns
// the bytes read. A failure resets the stream and returns false.
func (t *Terminator) firstRequest(s *stream.Inspected, conn *tls.Conn) ([]byte, bool) {
	buf := make([]byte, 0, firstRead)
	for {
		done, check := stream.RequestLine(buf, t.firstBytesCap)
		switch {
		case done && check == "":
			return buf, true
		case done:
			t.reset(s, stream.RuleProtocolMismatch, check, slog.String("looks_like", stream.LooksLike(buf)))
			return nil, false
		}
		var err error
		buf, err = stream.ReadMore(conn, buf, t.firstBytesCap)
		if err == nil {
			continue
		}
		if len(buf) == 0 {
			// Nothing after the handshake: the server's flight came first,
			// so an alert or a close here rejects the leaf as one during
			// the handshake does.
			t.refuseHandshake(s, handshakeErrorRule(err, true))
			return nil, false
		}
		r := handshakeErrorRule(err, false)
		if r.rule != stream.RuleFirstBytesTimeout {
			r = refusal{rule: stream.RuleProtocolMismatch, check: stream.CheckEnded}
		}
		t.refuseHandshake(s, r)
		return nil, false
	}
}

// firstRead is the first request line buffer's first size, as in the
// stream package.
const firstRead = 2 << 10

// refusal is what handshakeErrorRule makes of an error: the rule, the
// check, and the alert's description or "closed".
type refusal struct {
	rule  string
	check egressmsg.Check
	alert string
}

// alertECHRequired is the code of the ech_required alert.
const alertECHRequired = 121

// alertClosed is the alert attribute of a client that closed after the
// server's flight with no alert.
const alertClosed = "closed"

// handshakeErrorRule classifies an error of the guest's handshake, or of
// the first read after it. flightSent reports whether the server's
// flight was written before it.
//
//   - The deadline is stream-first-bytes-timeout.
//   - A received ech_required alert is tls-ech-refused.
//   - Any other received alert is tls-client-rejected-leaf with its
//     description, and so is the client's end after the server's flight,
//     with "closed" (S09-08-guest-trust, "Fails closed").
//   - Anything else is stream-protocol-mismatch.
//
// A received alert is told apart by its structure, as crypto/tls makes
// it: a *net.OpError whose Op is "remote error" and whose error is an
// alert's text. Error text alone never makes one.
func handshakeErrorRule(err error, flightSent bool) refusal {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return refusal{rule: stream.RuleFirstBytesTimeout, check: "deadline"}
	}
	if code, ok := receivedAlert(err); ok {
		desc := strings.TrimPrefix(tls.AlertError(code).Error(), "tls: ")
		if code == alertECHRequired {
			return refusal{rule: RuleECHRefused, alert: desc}
		}
		return refusal{rule: RuleClientRejectedLeaf, alert: desc}
	}
	if flightSent && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		return refusal{rule: RuleClientRejectedLeaf, alert: alertClosed}
	}
	return refusal{rule: stream.RuleProtocolMismatch, check: CheckHandshake}
}

// alertTexts maps the text of each alert crypto/tls can report to its
// code. A received alert's error is the alert, whose text is
// tls.AlertError's.
var alertTexts = func() map[string]uint8 {
	m := make(map[string]uint8, 256)
	for code := range 256 {
		m[tls.AlertError(code).Error()] = uint8(code)
	}
	return m
}()

// receivedAlert returns the code of the alert err reports receiving.
func receivedAlert(err error) (uint8, bool) {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "remote error" || op.Err == nil {
		return 0, false
	}
	code, ok := alertTexts[op.Err.Error()]
	return code, ok
}

// refuseHandshake resets s with r, and names the known gap of a rejected
// leaf.
func (t *Terminator) refuseHandshake(s *stream.Inspected, r refusal) {
	var attrs []slog.Attr
	if r.alert != "" {
		attrs = append(attrs, slog.String("alert", r.alert))
	}
	if r.rule == RuleClientRejectedLeaf {
		attrs = append(attrs, slog.String("known_gap", knownGap))
	}
	t.reset(s, r.rule, r.check, attrs...)
}
