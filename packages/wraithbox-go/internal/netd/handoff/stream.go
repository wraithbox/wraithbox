package handoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// GuestWriteTimeout bounds each write to the guest connection. A guest
// that takes no bytes for that long is reset, so it can't hold a stream
// and its place under the stack's cap (netd.StreamSink,
// SEC13-bounded-resources).
const GuestWriteTimeout = 60 * time.Second

// StreamHoldTimeout bounds how long wb-netd keeps its copy of the end
// of a stream's socketpair it sent to wb-proxyd. It keeps the copy until
// wb-proxyd's first byte on the stream or a StreamReset for it shows
// that wb-proxyd holds the end (S07-05-stream-hand-off, "Transport"):
// on macOS the kernel can collect a socket whose every reference is in
// a message, and wb-proxyd then reads end of file at once (I637). The
// deadline, counted from the send, keeps a wb-proxyd that never answers
// from tying up a descriptor of wb-netd for the stream's life
// (SEC13-bounded-resources). wb-proxyd reads messages ahead of its
// decisions (its Pending cap), so it normally holds the end long before
// the deadline.
//
// While wb-netd keeps the copy, a wb-proxyd that closes the stream
// without writing gives wb-netd no end of file: the guest gets its FIN
// once the deadline passes.
const StreamHoldTimeout = 10 * time.Second

// copyBuffer is the size of the buffer of each direction of a stream.
const copyBuffer = 32 << 10

// halfCloser is a connection that sends a FIN without closing, as
// *net.UnixConn does.
type halfCloser interface {
	net.Conn
	CloseWrite() error
}

// stream is one guest connection handed to wb-proxyd.
type stream struct {
	id    uint64
	flow  netd.NamedFlow
	guest netd.StreamConn
	ours  halfCloser // our end of the stream's socketpair
	// theirs is our copy of wb-proxyd's end, kept until wb-proxyd holds
	// it, and nil once closed (StreamHoldTimeout).
	theirs atomic.Pointer[os.File]

	resetByProxyd atomic.Bool // a StreamReset named the stream
	resetOnce     sync.Once
}

// reset sends the guest an RST and closes our end, which ends both
// copies.
func (st *stream) reset() {
	st.resetOnce.Do(func() {
		st.guest.Reset()
		_ = st.ours.Close()
	})
}

// release closes our copy of wb-proxyd's end, once wb-proxyd holds it,
// at the hold deadline, or when the stream ends. It reports whether
// this call closed it.
func (st *stream) release() bool {
	f := st.theirs.Swap(nil)
	if f == nil {
		return false
	}
	_ = f.Close()
	return true
}

// HandOffStream hands a guest connection to wb-proxyd
// (S07-05-stream-hand-off, "Streams"). It makes a SOCK_STREAM
// socketpair, sends one end in a Stream message with the socket's
// generation, the address and port, and a new stream id. It keeps its
// copy of that end until wb-proxyd holds it, within the Sender's hold
// deadline (StreamHoldTimeout). Then it copies bytes both ways until
// both are done.
// When the send fails or takes more than SendTimeout, it resets the
// guest connection, logged with handoff-send-failed. It owns conn and
// closes it.
func (s *Sender) HandOffStream(conn netd.StreamConn, f netd.NamedFlow) {
	defer func() { _ = conn.Close() }()
	sock, st, err := s.sendStream(conn, f)
	if err != nil {
		// Record before the guest sees the reset. The Sender's recorder
		// logs and counts it, and doesn't raise an audit event yet (I730).
		s.rec.Record(netd.Reset, netd.RuleHandoffSendFailed,
			append(f.LogAttrs(), slog.String("name", f.Name.String()), slog.String("error", err.Error()))...)
		conn.Reset()
		return
	}
	defer sock.remove(st.id)
	defer func() { _ = st.ours.Close() }()
	defer st.release()
	hold := time.AfterFunc(s.streamHold, func() {
		if st.release() {
			s.rec.Note(netd.RuleStreamHoldExpired,
				append(f.LogAttrs(), slog.Uint64("stream_id", st.id), slog.Duration("hold", s.streamHold))...)
		}
	})
	defer hold.Stop()
	up := make(chan struct{})
	go func() {
		defer close(up)
		st.toProxyd()
	}()
	st.toGuest(sock)
	<-up
}

// sendStream makes the stream's socketpair and sends wb-proxyd its end,
// all within SendTimeout.
func (s *Sender) sendStream(conn netd.StreamConn, f netd.NamedFlow) (*socket, *stream, error) {
	ctx, cancel := context.WithTimeout(context.Background(), SendTimeout)
	defer cancel()
	wire, ok := egressmsg.AddrToWire(f.Dest.Addr())
	if !ok {
		return nil, nil, fmt.Errorf("handoff: %v isn't an IPv4 address", f.Dest.Addr())
	}
	oursFile, theirs, err := s.streamPair()
	if err != nil {
		return nil, nil, fmt.Errorf("handoff: %w", err)
	}
	ours, err := fileConn(oursFile)
	if err != nil {
		_ = theirs.Close()
		return nil, nil, err
	}
	// The stream keeps theirs after the send, so a StreamReset that
	// comes before HandOffStream starts the hold deadline releases it.
	st := &stream{id: s.nextID.Add(1), flow: f, guest: conn, ours: ours}
	st.theirs.Store(theirs)
	sock, err := s.acquire(ctx)
	if err != nil {
		st.release()
		_ = ours.Close()
		return nil, nil, err
	}
	defer s.unlock()
	// The generation is the socket's, known only now. The stream is
	// registered before the send, so a StreamReset for it is never
	// unknown.
	rec, err := egressmsg.Marshal(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Stream{Stream: &egressv1.Stream{
		Generation: sock.generation, Address: wire, Port: uint32(f.Dest.Port()), StreamId: st.id,
	}}})
	if err == nil && !sock.add(st.id, st) {
		err = fmt.Errorf("%w: %w", ErrNoSocket, sock.cause())
	}
	if err == nil {
		err = sock.send(ctx, rec, theirs)
	}
	if err != nil {
		sock.remove(st.id)
		st.release()
		_ = ours.Close()
		return nil, nil, err
	}
	return sock, st, nil
}

// fileConn turns our end of a stream's socketpair into a connection and
// closes the file.
func fileConn(f *os.File) (halfCloser, error) {
	defer func() { _ = f.Close() }()
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("handoff: stream socket: %w", err)
	}
	hc, ok := c.(halfCloser)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("handoff: stream socket is a %T, not a stream socket", c)
	}
	return hc, nil
}

// toProxyd copies the guest's bytes to wb-proxyd. At the guest's FIN it
// sends wb-proxyd a FIN. When the guest resets or the read fails, it
// closes our end (S07-05-stream-hand-off, "Streams"). When a write to
// wb-proxyd fails it stops and leaves our end open, so the rest of
// wb-proxyd's bytes still reaches the guest, and toGuest ends the
// stream.
func (st *stream) toProxyd() {
	buf := make([]byte, copyBuffer)
	for {
		n, err := st.guest.Read(buf)
		if n > 0 {
			if _, err := st.ours.Write(buf[:n]); err != nil {
				return
			}
		}
		switch {
		case errors.Is(err, io.EOF):
			_ = st.ours.CloseWrite()
			return
		case err != nil:
			_ = st.ours.Close()
			return
		}
	}
}

// toGuest copies wb-proxyd's bytes to the guest, each write within
// GuestWriteTimeout. wb-proxyd's first byte shows it holds its end, so
// wb-netd closes its copy then. At end of file from wb-proxyd it reads
// every message queued on the hand-off socket, then sends the guest an
// RST when a StreamReset named the stream, and a FIN otherwise
// (S07-05-stream-hand-off, "Resets"). Any other end resets the guest:
// a guest write that fails or passes GuestWriteTimeout
// (SEC13-bounded-resources) and a failed read from wb-proxyd are
// recorded with their rule first.
func (st *stream) toGuest(sock *socket) {
	buf := make([]byte, copyBuffer)
	for {
		n, err := st.ours.Read(buf)
		if n > 0 {
			st.release()
			if werr := st.writeGuest(buf[:n]); werr != nil {
				st.resetFor(sock, guestWriteRule(werr), werr)
				return
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, net.ErrClosed):
			// Another path closed our end: the guest's reset or failed
			// read, or the hand-off socket's end. That path made the
			// decision.
			st.reset()
			return
		case !errors.Is(err, io.EOF):
			st.resetFor(sock, netd.RuleProxydStreamReadFailed, err)
			return
		}
		st.end(sock)
		return
	}
}

// guestWriteRule names the rule of a failed write to the guest.
func guestWriteRule(err error) netd.Rule {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return netd.RuleGuestWriteTimeout
	}
	return netd.RuleGuestWriteFailed
}

// resetFor records wb-netd's reset of the stream with its rule, then
// resets the guest.
func (st *stream) resetFor(sock *socket, rule netd.Rule, err error) {
	sock.s.rec.Record(netd.Reset, rule,
		append(st.flow.LogAttrs(), slog.Uint64("stream_id", st.id), slog.String("error", err.Error()))...)
	st.reset()
}

// end ends the stream at end of file from wb-proxyd, once every message
// queued on the hand-off socket was read. A StreamReset that named the
// stream, or a hand-off socket that ended, gives the guest an RST:
// wb-proxyd closes the hand-off socket before a stream's socket so that
// wb-netd resets it (S07-05-stream-hand-off, "Resets"). Otherwise the
// guest gets a FIN.
func (st *stream) end(sock *socket) {
	sock.drain()
	switch {
	case st.resetByProxyd.Load():
		sock.s.rec.Record(netd.Reset, netd.RuleStreamReset,
			append(st.flow.LogAttrs(), slog.Uint64("stream_id", st.id))...)
		st.reset()
	case sock.isEnded():
		// isEnded returned after the socket's end noted
		// handoff-socket-ended, so this reset comes after the note. The
		// end resets the stream too, and reset runs once.
		st.reset()
	default:
		_ = st.guest.CloseWrite()
	}
}

// writeGuest writes b to the guest within GuestWriteTimeout.
func (st *stream) writeGuest(b []byte) error {
	if err := st.guest.SetWriteDeadline(time.Now().Add(GuestWriteTimeout)); err != nil {
		return fmt.Errorf("set the write deadline: %w", err)
	}
	_, err := st.guest.Write(b)
	return err
}
