// Package handoff is wb-netd's end of its VM's hand-off socket to
// wb-proxyd (S07-05-stream-hand-off, S07-06-name-mapping). A Sender
// sends name reports, releases and streams in one order, under one
// lock, and reads the StreamReset messages wb-proxyd sends back.
//
// It implements netd.NameReports and netd.StreamHandOff. The synthetic
// pool (internal/netd/synth) calls Report and Release in the order
// S07-06-name-mapping asks for. The Sender keeps that order on the
// socket and adds the streams to it.
package handoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// SendTimeout bounds each send on the hand-off socket, the wait for the
// lock and the peer's version record included. A send that fails or
// takes longer counts as failed and is never retried out of order
// (S07-05-stream-hand-off, "Transport").
const SendTimeout = 100 * time.Millisecond

// ErrNoSocket is the error of a send while wb-netd holds no hand-off
// socket, or after the one it held ended.
var ErrNoSocket = errors.New("handoff: no hand-off socket")

// Conn is a hand-off socket: an *fdchan.Channel, or a fake in tests.
type Conn interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	TryRecv() (*fdchan.Message, error)
	SetReadDeadline(t time.Time) error
	Close() error
}

// Config is what a Sender needs.
type Config struct {
	// Own is the build version the version record carries.
	Own string
	// Log takes the Sender's log records. Records with a rule are
	// rate-limited per rule (netd.Recorder). Nil is slog.Default.
	Log *slog.Logger
	// StreamPair makes the socketpair of one stream. Nil is
	// fdchan.StreamPair.
	StreamPair func() (ours, theirs *os.File, err error)
	// VersionTimeout bounds the wait for wb-proxyd's version record on
	// each socket. Zero is hostversion.Timeout.
	VersionTimeout time.Duration
}

// Sender is wb-netd's end of the hand-off socket. It is safe for
// concurrent use.
type Sender struct {
	own        string
	log        *slog.Logger
	rec        *netd.Recorder
	streamPair func() (ours, theirs *os.File, err error)
	// versionTimeout bounds the wait for the peer's version record
	// (S04-architecture, "The version record").
	versionTimeout time.Duration

	// lock is held across each send, so the messages of all callers go
	// out in one order (S07-05-stream-hand-off, "Transport"). It is a
	// channel so a caller waits for it within its deadline.
	lock chan struct{}

	nextID atomic.Uint64 // the last stream id given out

	mu   sync.Mutex
	sock *socket // the current socket, or nil
}

// New returns a Sender that holds no hand-off socket yet. Until Replace
// gives it one, every send fails with ErrNoSocket.
func New(cfg Config) *Sender {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	pair := cfg.StreamPair
	if pair == nil {
		pair = fdchan.StreamPair
	}
	timeout := cfg.VersionTimeout
	if timeout == 0 {
		timeout = hostversion.Timeout
	}
	return &Sender{
		own:            cfg.Own,
		log:            log,
		rec:            netd.NewRecorder(log, nil),
		streamPair:     pair,
		versionTimeout: timeout,
		lock:           make(chan struct{}, 1),
	}
}

// Recorder returns the Sender's recorder, for its counts.
func (s *Sender) Recorder() *netd.Recorder { return s.rec }

// Replace makes conn the hand-off socket for generation, the VM
// generation it was made for. It sends the version record on it first,
// and reads wb-proxyd's messages from it until it ends. The old socket
// is closed and every stream of it reset (S04-07-supervision, "New
// hand-off sockets"). The caller checked the socket's type and
// generation.
//
// Replay of the live mapping on a new socket (S07-06-name-mapping,
// "Replay") isn't built here.
func (s *Sender) Replace(conn Conn, generation uint64) {
	sock := newSocket(s, conn, generation)
	s.lock <- struct{}{}
	s.mu.Lock()
	old := s.sock
	s.sock = sock
	s.mu.Unlock()
	if err := conn.Send(hostversion.Record(s.own), nil); err != nil {
		sock.end(fmt.Errorf("send the version record: %w", err))
	}
	<-s.lock
	if old != nil {
		old.end(errors.New("replaced by a new hand-off socket"))
	}
	go sock.read()
}

// Close closes the hand-off socket, if there is one, and resets every
// stream of it. Later sends fail with ErrNoSocket.
func (s *Sender) Close() {
	s.mu.Lock()
	sock := s.sock
	s.sock = nil
	s.mu.Unlock()
	if sock != nil {
		sock.end(errors.New("closed"))
	}
}

// Report sends a name report (S07-06-name-mapping, "Name reports").
func (s *Sender) Report(ctx context.Context, name dnsname.Name, addr netip.Addr) error {
	wire, ok := egressmsg.AddrToWire(addr)
	if !ok {
		return fmt.Errorf("handoff: %v isn't an IPv4 address", addr)
	}
	return s.sendMessage(ctx, &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
		NameReport: &egressv1.NameReport{Name: name.String(), Address: wire},
	}})
}

// Release sends a release (S07-06-name-mapping, "Releases").
func (s *Sender) Release(ctx context.Context, addr netip.Addr) error {
	wire, ok := egressmsg.AddrToWire(addr)
	if !ok {
		return fmt.Errorf("handoff: %v isn't an IPv4 address", addr)
	}
	return s.sendMessage(ctx, &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{
		Release: &egressv1.Release{Address: wire},
	}})
}

// sendMessage sends one message without a descriptor under the lock.
func (s *Sender) sendMessage(ctx context.Context, m *egressv1.NetdMessage) error {
	rec, err := egressmsg.Marshal(m)
	if err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	sock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer s.unlock()
	return sock.send(ctx, rec, nil)
}

// acquire takes the lock and returns the current socket once its peer's
// version record passed, all within ctx. The caller calls unlock.
func (s *Sender) acquire(ctx context.Context) (*socket, error) {
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("handoff: wait for the send lock: %w", context.Cause(ctx))
	}
	s.mu.Lock()
	sock := s.sock
	s.mu.Unlock()
	if sock == nil {
		s.unlock()
		return nil, ErrNoSocket
	}
	// A socket that ended fails the send, even when its version record
	// passed: select picks among ready cases at random.
	select {
	case <-sock.done:
		s.unlock()
		return nil, fmt.Errorf("%w: %w", ErrNoSocket, sock.cause())
	default:
	}
	select {
	case <-sock.ready:
		return sock, nil
	case <-sock.done:
		s.unlock()
		return nil, fmt.Errorf("%w: %w", ErrNoSocket, sock.cause())
	case <-ctx.Done():
		s.unlock()
		return nil, fmt.Errorf("handoff: wait for wb-proxyd's version record: %w", context.Cause(ctx))
	}
}

func (s *Sender) unlock() { <-s.lock }
