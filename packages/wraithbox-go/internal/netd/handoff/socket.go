package handoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// socket is one hand-off socket and the streams sent on it.
type socket struct {
	s          *Sender
	conn       Conn
	generation uint64

	ready     chan struct{} // closed when wb-proxyd's version record passed
	readyOnce sync.Once
	done      chan struct{} // closed when the socket ended
	endOnce   sync.Once

	mu      sync.Mutex
	err     error              // why the socket ended
	streams map[uint64]*stream // the open streams, by id
	drains  []chan struct{}    // the drain requests the reader hasn't served
	ended   bool
}

func newSocket(s *Sender, conn Conn, generation uint64) *socket {
	return &socket{
		s: s, conn: conn, generation: generation,
		ready: make(chan struct{}), done: make(chan struct{}),
		streams: map[uint64]*stream{},
	}
}

// send sends one record, with fd when it isn't nil. The caller holds the
// Sender's lock. A send that returns after ctx's deadline failed, even
// when the socket took it (S07-05-stream-hand-off, "Transport").
func (k *socket) send(ctx context.Context, rec []byte, fd syscall.Conn) error {
	if err := k.conn.Send(rec, fd); err != nil {
		return fmt.Errorf("handoff: send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("handoff: send: %w", context.Cause(ctx))
	}
	return nil
}

// cause returns why the socket ended.
func (k *socket) cause() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.err
}

// end closes the socket and resets every stream of it
// (S07-05-stream-hand-off, "Resets"). It resets the streams before it
// answers the drain requests, so a stream whose drain returns sees its
// reset. Only the first call does anything.
func (k *socket) end(why error) {
	k.endOnce.Do(func() {
		k.mu.Lock()
		k.err = why
		k.ended = true
		streams := k.streams
		k.streams = map[uint64]*stream{}
		drains := k.drains
		k.drains = nil
		k.mu.Unlock()
		// Noted before any stream's reset reaches the guest: the resets
		// below come after it, and a stream that asks isEnded waits for
		// done, closed after it (SEC10-audit).
		k.s.rec.Note(netd.RuleHandoffSocketEnded,
			slog.Uint64("generation", k.generation), slog.Int("streams_reset", len(streams)),
			slog.String("reason", why.Error()))
		close(k.done)
		_ = k.conn.Close()
		for _, st := range streams {
			st.reset()
		}
		for _, ch := range drains {
			close(ch)
		}
	})
}

// isEnded reports whether the socket ended. When it did, it first waits
// for done, which end closes after it noted handoff-socket-ended, so a
// stream that resets its guest on the answer does so after the note
// (SEC10-audit). end never waits on a stream, so the wait can't
// deadlock.
func (k *socket) isEnded() bool {
	k.mu.Lock()
	ended := k.ended
	k.mu.Unlock()
	if ended {
		<-k.done
	}
	return ended
}

// isReady reports whether wb-proxyd's version record passed.
func (k *socket) isReady() bool {
	select {
	case <-k.ready:
		return true
	default:
		return false
	}
}

// add registers an open stream. It fails when the socket ended.
func (k *socket) add(id uint64, st *stream) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.ended {
		return false
	}
	k.streams[id] = st
	return true
}

// remove takes a stream off the list.
func (k *socket) remove(id uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.streams, id)
}

// drain returns once the reader has read every message that was queued
// on the socket when drain was called, or the socket ended. So a
// StreamReset that wb-proxyd sent before it closed a stream's socket has
// been read when drain returns (S07-05-stream-hand-off, "Resets").
func (k *socket) drain() {
	ch := make(chan struct{})
	k.mu.Lock()
	if k.ended {
		k.mu.Unlock()
		return
	}
	k.drains = append(k.drains, ch)
	k.mu.Unlock()
	// Wake the reader: its Recv returns os.ErrDeadlineExceeded, and it
	// serves the drain requests then. An error here is a closed socket,
	// and end answers the request.
	_ = k.conn.SetReadDeadline(time.Now())
	<-ch
}

// read reads wb-proxyd's messages until the socket ends: the version
// record first, within the Sender's version timeout, then StreamReset
// messages. It is the only reader of the socket.
func (k *socket) read() {
	// version clears the deadline once the record passed. Until then no
	// stream exists, so no drain sets it (S04-architecture, "The version
	// record").
	_ = k.conn.SetReadDeadline(time.Now().Add(k.s.versionTimeout))
	for {
		m, err := k.conn.Recv()
		if errors.Is(err, os.ErrDeadlineExceeded) && !k.isReady() {
			k.refuse(hostversion.RuleTimeout, hostversion.RecordTimeout(k.s.log, k.s.own, err))
			return
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			if !k.serveDrains() {
				return
			}
			continue
		}
		if !k.take(m, err) {
			return
		}
	}
}

// serveDrains reads every queued message, then answers the drain
// requests made before it started. It reports whether the socket goes
// on.
func (k *socket) serveDrains() bool {
	// Clear the deadline before taking the requests: a request made
	// after this sets it again, and is served on the next wake.
	_ = k.conn.SetReadDeadline(time.Time{})
	k.mu.Lock()
	reqs := k.drains
	k.drains = nil
	k.mu.Unlock()
	defer func() {
		for _, ch := range reqs {
			close(ch)
		}
	}()
	for {
		m, err := k.conn.TryRecv()
		if errors.Is(err, fdchan.ErrNoMessage) {
			return true
		}
		if !k.take(m, err) {
			return false
		}
	}
}

// take decides one message the socket returned, or the error of the
// read. It reports whether the socket goes on.
func (k *socket) take(m *fdchan.Message, err error) bool {
	var refusal *fdchan.Refusal
	switch {
	case errors.Is(err, io.EOF):
		k.end(errors.New("wb-proxyd closed the hand-off socket"))
		return false
	case errors.As(err, &refusal):
		// fdchan logged it and closed every descriptor of it. A message
		// wb-proxyd can't have sent ends the socket (fail closed).
		k.refuse(string(refusal.Rule), err)
		return false
	case err != nil:
		k.end(fmt.Errorf("read: %w", err))
		return false
	}
	select {
	case <-k.ready:
	default:
		return k.version(m)
	}
	return k.streamReset(m)
}

// version checks wb-proxyd's version record, the first record on the
// socket (S04-architecture, "The version record").
func (k *socket) version(m *fdchan.Message) bool {
	hasFD := m.File != nil
	_ = m.Close()
	if hasFD {
		k.refuse("version-record", errors.New("the version record carries a descriptor"))
		return false
	}
	if _, err := hostversion.CheckRecord(k.s.log, k.s.own, m.Record); err != nil {
		k.refuse("version-record", err)
		return false
	}
	if err := k.conn.SetReadDeadline(time.Time{}); err != nil {
		k.end(fmt.Errorf("clear the version deadline: %w", err))
		return false
	}
	k.readyOnce.Do(func() { close(k.ready) })
	return true
}

// streamReset marks the stream a StreamReset names, so its end of file
// becomes a reset of the guest connection.
func (k *socket) streamReset(m *fdchan.Message) bool {
	hasFD := m.File != nil
	_ = m.Close()
	id, ref := egressmsg.DecodeProxydMessage(m.Record, hasFD)
	if ref != nil {
		k.refuse(string(ref.Check), ref)
		return false
	}
	k.mu.Lock()
	st, ok := k.streams[id]
	k.mu.Unlock()
	if !ok {
		k.s.rec.Note(netd.RuleStreamResetUnknown, slog.Uint64("stream_id", id), slog.String("decision", "ignore"))
		return true
	}
	st.resetByProxyd.Store(true)
	return true
}

// refuse logs a message from wb-proxyd that failed check, and ends the
// socket.
func (k *socket) refuse(check string, err error) {
	k.s.rec.Note(netd.RuleProxydMessageRefused, slog.String("check", check),
		slog.String("decision", "close-socket"), slog.String("error", err.Error()))
	k.end(fmt.Errorf("a refused message from wb-proxyd: %w", err))
}
