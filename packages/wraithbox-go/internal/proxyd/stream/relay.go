package stream

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
)

// raw relays a raw TCP entry's stream: no first-byte check, a dial
// through the Dialer to the entry's host and port, and both ways
// unchanged, so a server-first protocol works (S07-15-non-http-streams,
// "Raw TCP entries").
func (h *Handler) raw(st *stream) {
	entry, ok := rawEntry(st.policy, st.name, st.port)
	if !ok {
		h.reset(st, RuleProtocolMismatch, CheckProtocol)
		return
	}
	up, ok := h.dial(st)
	if !ok {
		return
	}
	h.allowed(st, RuleRawAllowed, ModeNone, entryAttrs(entry.Source, entry.Reason)...)
	r := &relay{h: h, st: st, up: up}
	r.run(
		func() error { return r.pipe(up, st.conn, &r.toUpstream) },
		func() error { return r.pipe(st.conn, up, &r.toGuest) },
	)
}

// refusal ends a relay with a rule: a reset, or for a client alert a
// close of both ways. It is the error a direction returns.
type refusal struct {
	rule  string
	check egressmsg.Check
	attrs []slog.Attr
	// close ends the stream with a close instead of a reset.
	close bool
}

func (r *refusal) Error() string { return "stream: refused with " + r.rule }

// decision is how the refusal ends the stream, for its log record.
func (r *refusal) decision() string {
	if r.close {
		return decisionClose
	}
	return decisionReset
}

// endedByHandoff is a relay's end when the hand-off ended its stream: a
// recompute, an address conflict or the socket's end, which logged it.
const endedByHandoff = "hand-off"

// relay is one allowed stream and its upstream connection.
type relay struct {
	h  *Handler
	st *stream
	up net.Conn

	toUpstream atomic.Int64
	toGuest    atomic.Int64
}

// direction copies one way until its source ends, which returns nil, or
// until a *refusal or another error.
type direction func() error

// run runs both directions, each on a goroutine, until both end. A
// direction that ends cleanly has half-closed its peer. The first
// refusal or error ends the stream and the upstream connection, which
// ends the other direction too: a refusal resets the stream, or closes
// it when it asks for a close, and is logged with its rule. Otherwise
// the stream is closed once both directions are done. A stream that the
// hand-off ended first isn't logged as refused.
func (r *relay) run(toUpstream, toGuest direction) {
	results := make(chan error, 2)
	for _, d := range []direction{toUpstream, toGuest} {
		go func() { results <- d() }()
	}
	how := "closed"
	ended := false
	for range 2 {
		err := <-results
		if ended || err == nil {
			continue
		}
		ended = true
		how = r.stop(err)
	}
	if !ended && !r.endStream(true) {
		how = endedByHandoff
	}
	r.h.ended(r.st, how, r.toUpstream.Load(), r.toGuest.Load())
}

// stop ends the stream on a direction's error, logs a refusal, and
// returns how the relay ended.
func (r *relay) stop(err error) string {
	ref, isRefusal := errors.AsType[*refusal](err)
	if !isRefusal {
		if !r.endStream(false) {
			return endedByHandoff
		}
		return "error"
	}
	if !r.endStream(ref.close) {
		return endedByHandoff
	}
	r.h.refused(r.st, ref.decision(), ref.rule, ref.check, ref.attrs...)
	return ref.rule
}

// endStream ends the stream with a close or a reset, closes the
// upstream connection, and reports whether this call ended the stream.
func (r *relay) endStream(closeIt bool) bool {
	var ended bool
	if closeIt {
		ended = r.st.ctl.Close()
	} else {
		ended = r.st.ctl.Reset()
	}
	_ = r.up.Close()
	return ended
}

// pipe copies src to dst unchanged until src ends, and then half-closes
// dst, so its reader sees the end. It returns nil at src's end.
func (r *relay) pipe(dst, src net.Conn, count *atomic.Int64) error {
	n, err := io.Copy(dst, src)
	count.Add(n)
	if err != nil {
		return err
	}
	closeWrite(dst)
	return nil
}

// closeWrite half-closes c where it can: a TCP and a Unix socket can.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// replayConn is a stream's connection whose reads return the bytes the
// first-byte check read first.
type replayConn struct {
	net.Conn
	pending []byte
}

func (c *replayConn) Read(b []byte) (int, error) {
	if len(c.pending) > 0 {
		n := copy(b, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}
