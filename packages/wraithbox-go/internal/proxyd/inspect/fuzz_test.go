package inspect

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
)

// TestHandshakeErrorRule: each alert crypto/tls can report, received
// during the handshake, is tls-ech-refused for ech_required and
// tls-client-rejected-leaf with its description for any other, and the
// other errors classify as handshakeErrorRule says.
func TestHandshakeErrorRule(t *testing.T) {
	for code := range 256 {
		err := &net.OpError{Op: "remote error", Err: tls.AlertError(code)}
		got := handshakeErrorRule(fmt.Errorf("handshake: %w", err), false)
		want := RuleClientRejectedLeaf
		if code == alertECHRequired {
			want = RuleECHRefused
		}
		if got.rule != want || got.alert == "" {
			t.Errorf("alert %d: %+v, want %s with the description", code, got, want)
		}
	}
	for _, c := range []struct {
		what   string
		err    error
		flight bool
		want   refusal
	}{
		{"the deadline", os.ErrDeadlineExceeded, true, refusal{rule: stream.RuleFirstBytesTimeout, check: "deadline"}},
		{"a close after the flight", io.EOF, true, refusal{rule: RuleClientRejectedLeaf, alert: alertClosed}},
		{"a cut record after the flight", io.ErrUnexpectedEOF, true, refusal{rule: RuleClientRejectedLeaf, alert: alertClosed}},
		{"a close before the flight", io.EOF, false, refusal{rule: stream.RuleProtocolMismatch, check: CheckHandshake}},
		{"a malformed record", errors.New("tls: first record does not look like a TLS handshake"), true, refusal{rule: stream.RuleProtocolMismatch, check: CheckHandshake}},
		{"an alert's text alone", errors.New("remote error: tls: bad certificate"), true, refusal{rule: stream.RuleProtocolMismatch, check: CheckHandshake}},
		{"a local alert", &net.OpError{Op: "local error", Err: tls.AlertError(42)}, true, refusal{rule: stream.RuleProtocolMismatch, check: CheckHandshake}},
	} {
		if got := handshakeErrorRule(c.err, c.flight); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.what, got, c.want)
		}
	}
}

// FuzzHandshakeErrorRule: arbitrary error text, plain, wrapped or in a
// *net.OpError with any Op, never classifies as tls-ech-refused or
// tls-client-rejected-leaf unless it is a received alert: a
// *net.OpError with Op "remote error" and an alert's text.
func FuzzHandshakeErrorRule(f *testing.F) {
	f.Add("remote error", "tls: bad certificate", uint8(2), true)
	f.Add("remote error", "tls: encrypted client hello required", uint8(2), false)
	f.Add("local error", "tls: unknown certificate authority", uint8(2), true)
	f.Add("", "remote error: tls: bad certificate", uint8(0), true)
	f.Add("read", "EOF", uint8(1), true)
	f.Fuzz(func(t *testing.T, op, text string, shape uint8, flight bool) {
		inner := errors.New(text)
		var err error
		switch shape % 3 {
		case 0:
			err = inner
		case 1:
			err = fmt.Errorf("wrapped: %w", inner)
		default:
			err = fmt.Errorf("wrapped: %w", &net.OpError{Op: op, Err: inner})
		}
		got := handshakeErrorRule(err, flight)
		_, isAlert := alertTexts[text]
		received := shape%3 == 2 && op == "remote error" && isAlert
		if (got.rule == RuleECHRefused || got.rule == RuleClientRejectedLeaf) && !received {
			t.Fatalf("%q in shape %d, op %q: %+v, but it isn't a received alert", text, shape%3, op, got)
		}
		if received && got.rule != RuleECHRefused && got.rule != RuleClientRejectedLeaf {
			t.Fatalf("a received alert %q: %+v", text, got)
		}
	})
}
