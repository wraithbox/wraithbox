package stream

import (
	"bytes"
	"errors"
	"net"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
)

// http2Preface is the start of the HTTP/2 connection preface (RFC 9113,
// section 3.4), which port 80 resets (S07-15-non-http-streams, "First
// bytes").
var http2Preface = []byte("PRI * HTTP/2.0\r\n")

// The protocols a reset names as what the first bytes looked like
// (S07-15-non-http-streams, "Logging a reset").
const (
	looksSSH     = "ssh"
	looksTLS     = "tls"
	looksHTTP    = "http"
	looksHTTP2   = "http2"
	looksUnknown = "unknown"
)

// LooksLike names the protocol the first bytes look like, for the log
// only: it decides nothing.
func LooksLike(b []byte) string {
	switch {
	case len(b) > 0 && b[0] == tlsparse.TypeHandshake:
		return looksTLS
	case bytes.HasPrefix(b, []byte("SSH-")):
		return looksSSH
	case bytes.HasPrefix(b, http2Preface[:len("PRI * HTTP/2")]):
		return looksHTTP2
	case methodThenSpace(b):
		return looksHTTP
	}
	return looksUnknown
}

// methodThenSpace reports whether b starts with a token and a space, as
// an HTTP/1 request line does.
func methodThenSpace(b []byte) bool {
	i := 0
	for i < len(b) && isTchar(b[i]) {
		i++
	}
	return i > 0 && i < len(b) && b[i] == ' '
}

// verdict is a first-byte decision on the bytes so far.
type verdict int

// The verdicts.
const (
	verdictMore verdict = iota
	verdictAccept
	verdictRefuse
)

// firstBytes is what the first-byte check read and decided.
type firstBytes struct {
	// buf is every byte read from the stream so far.
	buf []byte
	// n is the length of buf that the check took: the ClientHello's
	// records, or the request line.
	n int
	// hello is the ClientHello on a TLS port.
	hello *tlsparse.ClientHello
}

// decision is what a decide function returns.
type decision struct {
	verdict verdict
	check   egressmsg.Check
	n       int
	hello   *tlsparse.ClientHello
}

// decideTLS decides the first bytes on a TLS port: a handshake record
// with a ClientHello, reassembled across records, within the byte cap
// (S07-15-non-http-streams, "First bytes").
func (h *Handler) decideTLS(b []byte) decision {
	return decideTLS(b, h.firstBytesCap)
}

func decideTLS(b []byte, maxBytes int) decision {
	ch, n, err := tlsparse.ReadClientHello(b, maxBytes)
	switch {
	case errors.Is(err, tlsparse.ErrShort):
		return decision{verdict: verdictMore}
	case err != nil:
		return decision{verdict: verdictRefuse, check: egressmsg.Check(tlsparse.CheckOf(err))}
	}
	return decision{verdict: verdictAccept, n: n, hello: ch}
}

// decideHTTP decides the first bytes on port 80: an HTTP/1.1 or
// HTTP/1.0 request line within the byte cap. The HTTP/2 preface fails
// it, as its version is HTTP/2.0.
func (h *Handler) decideHTTP(b []byte) decision {
	return decideHTTP(b, h.firstBytesCap)
}

// RequestLine decides the first bytes of an HTTP/1 stream, on port 80
// or inside terminated TLS. It returns done false while b, within
// maxBytes, can still start an HTTP/1.1 or HTTP/1.0 request line. Once
// done, check is "" when b starts with one, and otherwise names what
// failed: tlsparse.CheckCap at the cap, or CheckRequestLine.
func RequestLine(b []byte, maxBytes int) (done bool, check egressmsg.Check) {
	d := decideHTTP(b, maxBytes)
	return d.verdict != verdictMore, d.check
}

func decideHTTP(b []byte, maxBytes int) decision {
	if len(b) > maxBytes {
		b = b[:maxBytes]
	}
	n, v := scanRequestLine(b)
	switch {
	case v == verdictMore && len(b) >= maxBytes:
		return decision{verdict: verdictRefuse, check: tlsparse.CheckCap}
	case v == verdictRefuse:
		return decision{verdict: verdictRefuse, check: CheckRequestLine}
	}
	return decision{verdict: v, n: n}
}

// scanRequestLine reads an HTTP/1 request line (RFC 9112, section 3):
// a method of token characters, a space, a request target of visible
// ASCII, a space, HTTP/1.1 or HTTP/1.0, and CRLF. It refuses as soon as
// the bytes can't start one, so a stream of another protocol doesn't
// wait for the cap. The Inspector parses the request again in full.
func scanRequestLine(b []byte) (int, verdict) {
	i := 0
	for i < len(b) && isTchar(b[i]) {
		i++
	}
	if i == len(b) {
		return 0, verdictMore
	}
	if i == 0 || b[i] != ' ' {
		return 0, verdictRefuse
	}
	i++
	start := i
	for i < len(b) && b[i] > ' ' && b[i] < 0x7f {
		i++
	}
	if i == len(b) {
		return 0, verdictMore
	}
	if i == start || b[i] != ' ' {
		return 0, verdictRefuse
	}
	rest := b[i+1:]
	for _, version := range []string{"HTTP/1.1\r\n", "HTTP/1.0\r\n"} {
		if bytes.HasPrefix(rest, []byte(version)) {
			return i + 1 + len(version), verdictAccept
		}
		if bytes.HasPrefix([]byte(version), rest) {
			return 0, verdictMore
		}
	}
	return 0, verdictRefuse
}

// isTchar reports whether c is a token character (RFC 9110, section
// 5.6.2).
func isTchar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return bytes.IndexByte([]byte("!#$%&'*+-.^_`|~"), c) >= 0
}

// readFirst reads the stream's first bytes until decide accepts or
// refuses them, within the byte cap and by the deadline from the
// hand-off (S07-15-non-http-streams, "First bytes";
// SEC13-bounded-resources). Nothing is resolved or dialed before it
// returns. A refusal resets the stream and returns false.
func (h *Handler) readFirst(st *stream, decide func([]byte) decision) (firstBytes, bool) {
	_ = st.conn.SetReadDeadline(st.began.Add(h.firstBytesTimeout))
	buf := make([]byte, 0, initialRead)
	for {
		d := decide(buf)
		st.looks = LooksLike(buf)
		switch d.verdict {
		case verdictAccept:
			_ = st.conn.SetReadDeadline(time.Time{})
			return firstBytes{buf: buf, n: d.n, hello: d.hello}, true
		case verdictRefuse:
			h.reset(st, RuleProtocolMismatch, d.check)
			return firstBytes{}, false
		case verdictMore:
		}
		var err error
		buf, err = ReadMore(st.conn, buf, h.firstBytesCap)
		if err != nil {
			h.firstReadFailed(st, err)
			return firstBytes{}, false
		}
	}
}

// initialRead is the first-byte buffer's first size. It grows to the cap
// only for a stream that sends that much.
const initialRead = 2 << 10

// ReadMore reads once into buf's spare room, first growing it up to
// maxBytes. The decide functions and RequestLine refuse a buffer at the
// cap that isn't decided, so ReadMore is never called with a full one.
// Bytes that came with an error are returned with a nil error, and the
// next read returns the error again.
func ReadMore(c net.Conn, buf []byte, maxBytes int) ([]byte, error) {
	if len(buf) == cap(buf) {
		grown := make([]byte, len(buf), min(2*cap(buf), maxBytes))
		copy(grown, buf)
		buf = grown
	}
	n, err := c.Read(buf[len(buf):cap(buf)])
	buf = buf[:len(buf)+n]
	if n > 0 {
		// Decide on the bytes that came with an error first. The next
		// read returns the error again.
		return buf, nil
	}
	return buf, err
}

// firstReadFailed resets a stream whose read ended before a decision:
// with stream-first-bytes-timeout at the deadline, and otherwise, at the
// client's end or a read error, with stream-protocol-mismatch.
func (h *Handler) firstReadFailed(st *stream, err error) {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		h.reset(st, RuleFirstBytesTimeout, "deadline")
		return
	}
	h.reset(st, RuleProtocolMismatch, CheckEnded)
}
