package vmhandoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// result is what a waiting request gets: the connection or the packet
// transport, or why there is none.
type result struct {
	conn      net.Conn
	transport *Transport
	err       error
}

// close closes what the result holds.
func (res result) close() {
	if res.conn != nil {
		_ = res.conn.Close()
	}
	if res.transport != nil {
		_ = res.transport.Close()
	}
}

// waiter holds done, which gets the request's one result. It has room
// for it, so a delivery never blocks.
type waiter struct {
	done chan result
}

func newWaiter() waiter { return waiter{done: make(chan result, 1)} }

// cancel closes request id if it is still the open request r.
func (t *table) cancel(id uint64, r *request) {
	if t.open[id] == r {
		delete(t.open, id)
	}
}

// cancelNetwork closes the request for the packet transport if it is
// still the open request r. Its generation gets no other request, and a
// record of it that comes late is refused as late.
func (t *table) cancelNetwork(r *request) {
	if t.network != r {
		return
	}
	t.network = nil
	if r.generation > t.networkDone {
		t.networkDone = r.generation
		t.networkCancelled = r.generation
	}
}

// closeAll closes every open request and returns them.
func (t *table) closeAll() []*request {
	var all []*request
	for id, r := range t.open {
		all = append(all, r)
		delete(t.open, id)
	}
	if t.network != nil {
		all = append(all, t.network)
		t.network = nil
	}
	return all
}

// Receiver is wb-hostd's end of one wb-vmd's hand-off channel. Expect
// opens a request before its ConnectGuest call, and Run hands each
// connection that passes the checks to its request.
type Receiver struct {
	ch  *fdchan.Channel
	log *slog.Logger

	mu     sync.Mutex // guards t and closed
	t      *table
	closed bool
}

// NewReceiver makes a receiver of f, wb-hostd's end of the hand-off
// socketpair. It takes f and closes it, also when it fails: the channel
// holds its own copy.
func NewReceiver(f *os.File, log *slog.Logger) (*Receiver, error) {
	ch, err := fdchan.New(f, MaxRecord)
	if err != nil {
		return nil, fmt.Errorf("vmhandoff: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Receiver{ch: ch, log: log, t: newTable()}, nil
}

// Pending is an open request.
type Pending struct {
	rx  *Receiver
	id  uint64
	req *request
}

// Expect opens request id for a connection to port in the guest of
// generation, before the caller sends its ConnectGuest request: wb-vmd
// sends the record before it answers. A generation newer than the
// current one becomes the current one, and every request of an older
// generation fails with RuleGeneration.
func (r *Receiver) Expect(id, generation uint64, port uint32) (*Pending, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, &Refusal{Rule: RuleClosed, msg: "the receiver is closed"}
	}
	req, stale, ref := r.t.expect(id, generation, port)
	if ref != nil {
		r.logRefusal(ref, slog.Uint64("request_id", id), slog.Uint64("generation", generation))
		return nil, ref
	}
	failStale(stale, generation)
	return &Pending{rx: r, id: id, req: req}, nil
}

// failStale fails the requests a newer generation closed.
func failStale(stale []*request, generation uint64) {
	for _, s := range stale {
		s.done <- result{err: &Refusal{Rule: RuleGeneration, msg: fmt.Sprintf("generation %d replaced generation %d", generation, s.generation)}}
	}
}

// PendingNetwork is the open request for a generation's packet
// transport.
type PendingNetwork struct {
	rx  *Receiver
	req *request
}

// ExpectNetwork opens the request for the packet transport of
// generation, before the caller sends its Start request: wb-vmd sends
// the record before the VM starts (S07-02-packet-path, "Packet
// transport"). A generation gets one. A generation newer than the
// current one becomes the current one, as in Expect.
func (r *Receiver) ExpectNetwork(generation uint64) (*PendingNetwork, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, &Refusal{Rule: RuleClosed, msg: "the receiver is closed"}
	}
	req, stale, ref := r.t.expectNetwork(generation)
	if ref != nil {
		r.logRefusal(ref, slog.Uint64("generation", generation), slog.String("kind", vmdv1.Kind_KIND_NETWORK.String()))
		return nil, ref
	}
	failStale(stale, generation)
	return &PendingNetwork{rx: r, req: req}, nil
}

// Wait waits for the packet transport. When ctx ends first, the request
// is closed: a record for it that comes later is refused with
// RuleNetwork.
func (p *PendingNetwork) Wait(ctx context.Context) (*Transport, error) {
	select {
	case res := <-p.req.done:
		return res.transport, res.err
	case <-ctx.Done():
		p.Cancel()
		return nil, fmt.Errorf("vmhandoff: packet transport of generation %d: %w", p.req.generation, ctx.Err())
	}
}

// Cancel closes the request, and a transport that came for it.
func (p *PendingNetwork) Cancel() {
	p.rx.mu.Lock()
	p.rx.t.cancelNetwork(p.req)
	p.rx.mu.Unlock()
	select {
	case res := <-p.req.done:
		res.close()
	default:
	}
}

// Wait waits for the request's connection. When ctx ends first, the
// request is closed: a record for it that comes later is refused with
// RuleUnexpected.
func (p *Pending) Wait(ctx context.Context) (net.Conn, error) {
	select {
	case res := <-p.req.done:
		return res.conn, res.err
	case <-ctx.Done():
		p.Cancel()
		return nil, fmt.Errorf("vmhandoff: request %d: %w", p.id, ctx.Err())
	}
}

// Cancel closes the request, and a connection that came for it.
func (p *Pending) Cancel() {
	p.rx.mu.Lock()
	p.rx.t.cancel(p.id, p.req)
	p.rx.mu.Unlock()
	// A delivery happens under the lock, so after it the result is
	// either in done or never comes.
	select {
	case res := <-p.req.done:
		res.close()
	default:
	}
}

// Run receives records until the channel ends, and returns nil when
// wb-vmd closed its end or Close was called. A refused record is logged
// and skipped. A transport refusal after which fdchan closed the channel
// ends Run with that refusal, as does any other receive error.
//
// When Run returns, the receiver is closed: Expect refuses with
// RuleClosed, and every open request fails at once.
func (r *Receiver) Run() error {
	defer func() { _ = r.Close() }()
	var last *fdchan.Refusal
	for {
		err := r.Receive()
		switch {
		case err == nil:
		case errors.Is(err, io.EOF), r.isClosed():
			return nil
		case fatal(err, last):
			return err
		}
		last, _ = errors.AsType[*fdchan.Refusal](err)
	}
}

// fatal reports whether a receive error ends the receiver: anything but
// the refusal of one record. fdchan ends its channel after
// RuleControlTruncated, and after a RuleFDTableFull whose message it
// couldn't drop. An ended channel returns the refusal that ended it
// again on every Recv, so the same refusal twice means it ended.
func fatal(err error, last *fdchan.Refusal) bool {
	if _, ok := errors.AsType[*Refusal](err); ok {
		return false
	}
	fr, ok := errors.AsType[*fdchan.Refusal](err)
	if !ok {
		return true
	}
	return fr.Rule == fdchan.RuleControlTruncated || fr == last
}

func (r *Receiver) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// Receive reads one record and checks it. On success the connection
// goes to its request and Receive returns nil. It also returns nil when
// the record passed but no connection could be made of its descriptor:
// the request gets that error. A refusal closes every
// descriptor of the record, is logged with its rule, and is returned:
// a *Refusal, or fdchan's own *fdchan.Refusal for the transport checks.
// io.EOF means wb-vmd closed its end.
func (r *Receiver) Receive() error {
	m, err := r.ch.Recv()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The record is decoded first, fdchan having bounded it at
	// MaxRecord bytes, so that a refusal of its descriptor reaches the
	// request it names instead of leaving that request to time out.
	msg, req, ref := r.t.check(m.Record)
	if ref == nil {
		ref = checkDescriptor(m, msg.GetKind())
	}
	if ref != nil {
		if req != nil {
			req.done <- result{err: ref}
		}
		return r.refuse(m, msg, ref)
	}
	if msg.GetKind() == vmdv1.Kind_KIND_NETWORK {
		// The request takes the record's file, so m isn't closed.
		req.done <- result{transport: &Transport{f: m.File}}
		r.log.LogAttrs(context.Background(), slog.LevelInfo, "packet transport handed off",
			append([]slog.Attr{slog.String("decision", "accept")}, messageAttrs(msg)...)...)
		return nil
	}
	// FileConn takes a copy, so the record's file is closed either way.
	conn, err := net.FileConn(m.File)
	_ = m.Close()
	if err != nil {
		// Such as EMFILE. The request gets the error, and the channel
		// goes on.
		err = fmt.Errorf("vmhandoff: request %d: %w", msg.GetRequestId(), err)
		req.done <- result{err: err}
		r.log.LogAttrs(context.Background(), slog.LevelWarn, "hand-off failed",
			append([]slog.Attr{slog.String("error", err.Error())}, messageAttrs(msg)...)...)
		return nil
	}
	req.done <- result{conn: conn}
	r.log.LogAttrs(context.Background(), slog.LevelInfo, "guest connection handed off",
		append([]slog.Attr{slog.String("decision", "accept")}, messageAttrs(msg)...)...)
	return nil
}

// checkDescriptor refuses a record without a descriptor, or with one
// that isn't the socket its kind names: an AF_UNIX stream socket for a
// connection, which a vsock connection's descriptor is on macOS
// (X18-vsock-handoff), and an AF_UNIX datagram socket for the packet
// transport (S07-02-packet-path, "Packet transport").
func checkDescriptor(m *fdchan.Message, kind vmdv1.Kind) *Refusal {
	if m.File == nil {
		return &Refusal{Rule: RuleFDCount, msg: "the record came without a descriptor"}
	}
	want, rule, name := syscall.SOCK_STREAM, RuleSocketType, "stream"
	if kind == vmdv1.Kind_KIND_NETWORK {
		want, rule, name = syscall.SOCK_DGRAM, RuleNetwork, "datagram"
	}
	family, sockType, err := fdchan.SocketType(m.File)
	if err != nil || family != syscall.AF_UNIX || sockType != want {
		return &Refusal{
			Rule: rule,
			msg:  fmt.Sprintf("the descriptor is not an AF_UNIX %s socket (family %d, type %d)", name, family, sockType),
			err:  err,
		}
	}
	return nil
}

// refuse closes the record's descriptor, and logs and returns ref.
func (r *Receiver) refuse(m *fdchan.Message, msg *vmdv1.HandoffMessage, ref *Refusal) error {
	closed := 0
	if m.File != nil {
		closed = 1
	}
	_ = m.Close()
	attrs := []slog.Attr{slog.Int("bytes", len(m.Record)), slog.Int("closed", closed)}
	if msg != nil {
		attrs = append(attrs, messageAttrs(msg)...)
	}
	r.logRefusal(ref, attrs...)
	return ref
}

func (r *Receiver) logRefusal(ref *Refusal, attrs ...slog.Attr) {
	level := slog.LevelWarn
	if ref.late {
		level = slog.LevelInfo
	}
	r.log.LogAttrs(context.Background(), level, "hand-off refused",
		append([]slog.Attr{
			slog.String("rule", string(ref.Rule)),
			slog.String("decision", "refuse"),
			slog.String("reason", ref.msg),
		}, attrs...)...)
}

// messageAttrs are the fields of a message for the log. The source port
// is only logged: routing uses the destination port.
func messageAttrs(msg *vmdv1.HandoffMessage) []slog.Attr {
	return []slog.Attr{
		slog.String("kind", msg.GetKind().String()),
		slog.Uint64("request_id", msg.GetRequestId()),
		slog.Uint64("generation", msg.GetGeneration()),
		slog.String("direction", msg.GetDirection().String()),
		slog.Uint64("source_port", uint64(msg.GetSourcePort())),
		slog.Uint64("destination_port", uint64(msg.GetDestinationPort())),
	}
}

// Close closes the channel, which ends Run, and fails every open
// request with RuleClosed.
func (r *Receiver) Close() error {
	r.mu.Lock()
	r.closed = true
	open := r.t.closeAll()
	r.mu.Unlock()
	for _, req := range open {
		req.done <- result{err: &Refusal{Rule: RuleClosed, msg: "the receiver was closed"}}
	}
	return r.ch.Close()
}
