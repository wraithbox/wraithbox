package stream

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
)

// readChunk is the size of one relay read during the pass checks.
const readChunk = 32 << 10

// pass relays a pass host's stream (S07-16-tls-and-tunnels, "Pass"). Its
// ClientHello is checked before the dial, its records up to the
// server's ServerHello that isn't a HelloRetryRequest are checked one by
// one, and after that ServerHello bytes go both ways unchanged
// (S07-07-hosts-and-modes, "pass").
func (h *Handler) pass(st *stream, first firstBytes) {
	if ref := checkPassHello(first.hello, st.name); ref != nil {
		h.reset(st, ref.rule, ref.check, ref.attrs...)
		return
	}
	entry, ok := passEntry(st.policy, st.name)
	if !ok {
		h.reset(st, RuleProtocolMismatch, CheckPassMode)
		return
	}
	up, ok := h.dial(st)
	if !ok {
		return
	}
	r := &relay{h: h, st: st, up: up}
	if !r.sendHello(first.buf[:first.n], entry) {
		return
	}
	p := &passState{
		name:          st.name,
		firstBytesCap: h.firstBytesCap,
		flight:        tlsparse.NewServerFlight(h.serverFlightCap),
	}
	rest := append([]byte(nil), first.buf[first.n:]...)
	r.run(
		func() error { return r.checkedCopy(up, st.conn, &r.toUpstream, rest, p.nextClient) },
		func() error { return r.checkedCopy(st.conn, up, &r.toGuest, nil, p.nextServer) },
	)
}

// sendHello logs the pass relay allowed and sends its checked
// ClientHello upstream, in one step with the check that the stream is
// open (Control.SendWhileOpen). A stream that a recompute, an address
// conflict or the socket's end ended after the dial sends nothing
// upstream and logs nothing more, and its upstream connection is closed
// (S07-15-non-http-streams, "Logging a reset"). The send is bounded by
// firstSendTimeout, as the stream's end waits for it
// (SEC13-bounded-resources). It reports whether the relay goes on.
func (r *relay) sendHello(hello []byte, entry policy.PassHost) bool {
	err := r.st.ctl.SendWhileOpen(func() error {
		r.h.allowed(r.st, RulePassAllowed, ModePass, entryAttrs(entry.Source, entry.Reason)...)
		if err := r.up.SetWriteDeadline(time.Now().Add(r.h.firstSendTimeout)); err != nil {
			return err
		}
		if err := r.send(r.up, hello, &r.toUpstream); err != nil {
			return err
		}
		return r.up.SetWriteDeadline(time.Time{})
	})
	if err == nil {
		return true
	}
	if errors.Is(err, handoff.ErrEnded) {
		_ = r.up.Close()
		return false
	}
	how := "error"
	if errors.Is(err, os.ErrDeadlineExceeded) {
		how = endFirstSendTimeout
	}
	if !r.endStream(false) {
		how = endedByHandoff
	}
	r.h.ended(r.st, how, r.toUpstream.Load(), 0)
	return false
}

// endFirstSendTimeout is a pass relay's end when its ClientHello's
// write upstream didn't finish within firstSendTimeout.
const endFirstSendTimeout = "first-send-timeout"

// checkPassHello checks a ClientHello to a pass host: its server name is
// the stream's name, it holds no encrypted_client_hello or encrypted SNI
// extension (tls-ech-pass), no early_data extension, and no extension
// outside tlsparse.KnownExtension (tls-extension-unknown). It returns
// nil when the ClientHello passes.
func checkPassHello(ch *tlsparse.ClientHello, name dnsname.Name) *refusal {
	got, ok := ch.Name()
	if !ok || got != name {
		return &refusal{rule: RuleSNIMismatch, check: sniCheck(ch, ok)}
	}
	if ch.Has(tlsparse.ExtEncryptedClientHello) || ch.Has(tlsparse.ExtEncryptedServerName) {
		return &refusal{rule: RuleECHPass, check: "extension"}
	}
	if ch.Has(tlsparse.ExtEarlyData) {
		return earlyData()
	}
	for _, e := range ch.Extensions {
		if !tlsparse.KnownExtension(e) {
			return &refusal{
				rule: RuleExtensionUnknown, check: "extension",
				attrs: []slog.Attr{slog.Int("extension", int(e))},
			}
		}
	}
	return nil
}

// earlyData is the refusal of early data to a pass host, which the
// audit entry explains.
func earlyData() *refusal {
	return &refusal{
		rule: RuleProtocolMismatch, check: CheckEarlyData,
		attrs: []slog.Attr{slog.String("reason", "early data isn't allowed to a pass host")},
	}
}

// step is what a checked direction does with its buffer next.
type step struct {
	// n bytes of the buffer go on.
	n int
	// more: the buffer holds nothing whole past n, so read more.
	more bool
	// through: the checks are over, and the whole buffer and the rest of
	// the source go on unchanged.
	through bool
}

// checkedCopy copies src to dst through next until next says through,
// and then unchanged. next takes the bytes read and not yet sent, and
// says how many go on, or returns a *refusal. Before through, only bytes
// next passed reach dst.
func (r *relay) checkedCopy(dst, src net.Conn, count *atomic.Int64, buf []byte, next func([]byte) (step, error)) error {
	chunk := make([]byte, readChunk)
	for {
		s, err := next(buf)
		if err != nil {
			return err
		}
		if s.through {
			s.n = len(buf)
		}
		if err := r.send(dst, buf[:s.n], count); err != nil {
			return err
		}
		buf = buf[s.n:]
		if s.through {
			return r.pipe(dst, src, count)
		}
		if !s.more {
			continue
		}
		n, err := src.Read(chunk)
		buf = append(buf, chunk[:n]...)
		switch {
		case n > 0:
		case errors.Is(err, io.EOF):
			closeWrite(dst)
			return nil
		case err != nil:
			return err
		}
	}
}

// passState is what the two directions of a pass relay share, under mu.
// The server direction sets retried before it relays a
// HelloRetryRequest, and done before it relays the ServerHello, so the
// client direction sees each before the client can answer it.
type passState struct {
	name          dnsname.Name
	firstBytesCap int
	// flight is the server direction's own.
	flight *tlsparse.ServerFlight

	mu sync.Mutex
	// retried: the server sent a HelloRetryRequest.
	retried bool
	// secondHello: the client's second ClientHello passed its checks.
	secondHello bool
	// changeCipherSpec: the client's one change cipher spec was relayed.
	changeCipherSpec bool
	// done: the server sent its ServerHello, and the checks are over.
	done bool
}

// nextClient decides the client's next record before the ServerHello
// (S07-16-tls-and-tunnels, "Pass"): a second ClientHello after a
// HelloRetryRequest, one change cipher spec of the one byte 0x01, or an
// alert, which closes both ways without being relayed. Anything else
// is refused. After the ServerHello the rest goes through.
func (p *passState) nextClient(buf []byte) (step, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return step{through: true}, nil
	}
	if len(buf) >= tlsparse.HeaderLen && buf[0] == tlsparse.TypeHandshake {
		return p.secondClientHello(buf)
	}
	rec, n, err := tlsparse.ParseRecord(buf)
	switch {
	case errors.Is(err, tlsparse.ErrShort):
		return step{more: true}, nil
	case err != nil:
		return step{}, &refusal{rule: RuleProtocolMismatch, check: egressmsg.Check(tlsparse.CheckOf(err))}
	}
	switch rec.Type {
	case tlsparse.TypeChangeCipherSpec:
		if p.changeCipherSpec || len(rec.Fragment) != 1 || rec.Fragment[0] != 1 {
			return step{}, &refusal{rule: RuleProtocolMismatch, check: CheckChangeCipherSpec}
		}
		p.changeCipherSpec = true
		return step{n: n}, nil
	case tlsparse.TypeAlert:
		return step{}, alertRefusal(rec.Fragment)
	case tlsparse.TypeApplicationData:
		return step{}, earlyData()
	}
	return step{}, &refusal{
		rule: RuleProtocolMismatch, check: CheckClientRecord,
		attrs: []slog.Attr{slog.Int("record_type", int(rec.Type))},
	}
}

// secondClientHello decides a handshake record before the ServerHello.
// Only one is taken: the second ClientHello after a HelloRetryRequest,
// with the same checks as the first. Its records go upstream only once
// the whole of it passed.
func (p *passState) secondClientHello(buf []byte) (step, error) {
	if !p.retried || p.secondHello {
		return step{}, &refusal{
			rule: RuleProtocolMismatch, check: CheckClientRecord,
			attrs: []slog.Attr{slog.String("reason", "a handshake record before the ServerHello that isn't a second ClientHello")},
		}
	}
	ch, n, err := tlsparse.ReadClientHello(buf, p.firstBytesCap)
	switch {
	case errors.Is(err, tlsparse.ErrShort):
		return step{more: true}, nil
	case err != nil:
		return step{}, &refusal{rule: RuleProtocolMismatch, check: egressmsg.Check(tlsparse.CheckOf(err))}
	}
	if ref := checkPassHello(ch, p.name); ref != nil {
		return step{}, ref
	}
	p.secondHello = true
	return step{n: n}, nil
}

// alertRefusal is a client alert before the ServerHello: the relay
// closes both ways without relaying it, and logs its level and
// description with tls-client-alert.
func alertRefusal(frag []byte) *refusal {
	if len(frag) != 2 {
		return &refusal{
			rule: RuleProtocolMismatch, check: CheckClientRecord,
			attrs: []slog.Attr{slog.String("reason", fmt.Sprintf("an alert record of %d bytes", len(frag)))},
		}
	}
	return &refusal{
		rule: RuleClientAlert, check: "alert", close: true,
		attrs: []slog.Attr{slog.Int("alert_level", int(frag[0])), slog.Int("alert_description", int(frag[1]))},
	}
}

// nextServer reads the server's next record up to its ServerHello that
// isn't a HelloRetryRequest, within the flight's cap, and updates the
// shared state before the record is relayed. A HelloRetryRequest lets
// the client send one more ClientHello. A ServerHello after it, before
// that ClientHello passed, is refused, so the checks can't end before
// the second ClientHello is read.
func (p *passState) nextServer(buf []byte) (step, error) {
	rec, n, err := tlsparse.ParseRecord(buf)
	switch {
	case errors.Is(err, tlsparse.ErrShort):
		return step{more: true}, nil
	case err != nil:
		return step{}, serverRefusal(err)
	}
	ev, err := p.flight.Record(rec)
	if err != nil {
		return step{}, serverRefusal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev {
	case tlsparse.ServerRetry:
		p.retried = true
	case tlsparse.ServerHello:
		if p.retried && !p.secondHello {
			return step{}, &refusal{
				rule: RuleProtocolMismatch, check: CheckServerRecords,
				attrs: []slog.Attr{slog.String("reason", "a ServerHello before the second ClientHello")},
			}
		}
		p.done = true
		return step{through: true}, nil
	case tlsparse.ServerNothing:
	}
	return step{n: n}, nil
}

// serverRefusal is a server record the flight checks refused.
func serverRefusal(err error) *refusal {
	return &refusal{
		rule: RuleProtocolMismatch, check: CheckServerRecords,
		attrs: []slog.Attr{slog.String("server_check", tlsparse.CheckOf(err))},
	}
}

// send writes b to c and counts it.
func (r *relay) send(c net.Conn, b []byte, count *atomic.Int64) error {
	if len(b) == 0 {
		return nil
	}
	n, err := c.Write(b)
	count.Add(int64(n))
	return err
}
