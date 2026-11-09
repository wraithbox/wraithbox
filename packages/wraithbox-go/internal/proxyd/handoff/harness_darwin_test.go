package handoff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// The harness of the socket tests: wb-hostd's end of the control
// channel and wb-netd's end of each hand-off socket, over real fdchan
// socketpairs, around a Receiver with a fake handler, wildcard writer
// and audit hook.

// testVersion is the build version both ends send.
const testVersion = "1.2.3"

// wait bounds every wait for something the receiver sends.
const wait = 3 * time.Second

type fakeHandler struct{ streams chan *Stream }

func (h *fakeHandler) HandleStream(s *Stream) { h.streams <- s }

// fakeWildcard records each name. A gate holds the write until it is
// closed, and a delay makes it slow.
type fakeWildcard struct {
	mu    sync.Mutex
	names []string
	gate  chan struct{}
	delay time.Duration
}

func (w *fakeWildcard) RecordWildcardName(_ context.Context, vm string, name dnsname.Name) error {
	w.mu.Lock()
	gate, delay := w.gate, w.delay
	w.mu.Unlock()
	if gate != nil {
		<-gate
	}
	time.Sleep(delay)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names = append(w.names, vm+"/"+name.String())
	return nil
}

func (w *fakeWildcard) written() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.names...)
}

type fakeAudit struct{ events chan Event }

func (a *fakeAudit) Refused(e Event) { a.events <- e }

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	t       *testing.T
	r       *Receiver
	hostd   *fdchan.Channel
	raw     int
	handler *fakeHandler
	wild    *fakeWildcard
	audit   *fakeAudit
	logs    *syncBuffer
	served  chan error
}

// newHarness starts a receiver on a control channel and exchanges the
// version records.
func newHarness(t *testing.T, caps Caps) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		handler: &fakeHandler{streams: make(chan *Stream, 64)},
		wild:    &fakeWildcard{},
		audit:   &fakeAudit{events: make(chan Event, 1024)},
		logs:    &syncBuffer{},
		served:  make(chan error, 1),
	}
	r, err := NewReceiver(Config{
		Version: testVersion, Caps: caps, Handler: h.handler, Wildcard: h.wild, Audit: h.audit,
		Log:       slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		LogWindow: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.r = r
	a, b, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	h.raw = dup(t, a)
	h.hostd = newChannel(t, a)
	proxyd := newChannel(t, b)
	go func() { h.served <- r.Serve(context.Background(), proxyd) }()
	t.Cleanup(func() {
		r.Close()
		_ = h.hostd.Close()
	})
	checkVersion(t, h.hostd)
	if err := h.hostd.Send(hostversion.Record(testVersion), nil); err != nil {
		t.Fatal(err)
	}
	return h
}

func newChannel(t *testing.T, f *os.File) *fdchan.Channel {
	t.Helper()
	ch, err := fdchan.New(f, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// dup returns a copy of f's descriptor for raw sends past fdchan.Send.
func dup(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	return fd
}

// recv reads one message within wait.
func recv(t *testing.T, ch *fdchan.Channel) *fdchan.Message {
	t.Helper()
	if err := ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	m, err := ch.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	return m
}

func checkVersion(t *testing.T, ch *fdchan.Channel) {
	t.Helper()
	m := recv(t, ch)
	if _, err := hostversion.CheckRecord(nil, testVersion, m.Record); err != nil {
		t.Fatalf("the first record isn't the version record: %v", err)
	}
}

// send sends a control message, with fd when it isn't nil.
func (h *harness) send(m *egressv1.HostdToProxyd, fd *os.File) {
	h.t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	if fd == nil {
		err = h.hostd.Send(b, nil)
	} else {
		err = h.hostd.Send(b, fd)
	}
	if err != nil {
		h.t.Fatal(err)
	}
}

// sendPayload sends a control message whose descriptor is a payload
// pipe that holds payload.
func (h *harness) sendPayload(m *egressv1.HostdToProxyd, payload proto.Message) {
	h.t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := egressmsg.WritePayload(w, payload); err != nil {
		h.t.Fatal(err)
	}
	_ = w.Close()
	h.send(m, r)
}

// start sends ProxydStart with the wildcard names of each VM.
func (h *harness) start(sets ...*egressv1.WildcardNames) {
	h.t.Helper()
	h.sendPayload(&egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ProxydStart{ProxydStart: &egressv1.ProxydStart{}}},
		&egressv1.ProxydStartPayload{WildcardNames: sets})
}

// policy sends a PolicyChange and waits for its PolicyAck.
func (h *harness) policy(vm string, seq uint64, p *policyv1.EffectivePolicy) {
	h.t.Helper()
	h.sendPayload(&egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: vm, Sequence: seq},
	}}, p)
	ack := h.recvHostd().GetPolicyAck()
	if ack.GetVm() != vm || ack.GetSequence() != seq {
		h.t.Fatalf("PolicyAck = %v, want %s %d", ack, vm, seq)
	}
}

// recvHostd reads and checks one message from wb-proxyd.
func (h *harness) recvHostd() *egressv1.ProxydToHostd {
	h.t.Helper()
	m := recv(h.t, h.hostd)
	msg, ref := egressmsg.DecodeProxydToHostd(m.Record, m.File != nil)
	if ref != nil {
		h.t.Fatalf("wb-proxyd sent a message wb-hostd refuses: %v", ref)
	}
	return msg
}

func handoffSocket(vm string, gen uint64) *egressv1.HostdToProxyd {
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_HandoffSocket{
		HandoffSocket: &egressv1.HandoffSocket{Vm: vm, Generation: gen},
	}}
}

// netd is wb-netd's end of a hand-off socket.
type netd struct {
	t   *testing.T
	ch  *fdchan.Channel
	raw int
	vm  string
	gen uint64
	// next is the next barrier stream id.
	next uint64
}

// addVM gives wb-proxyd a hand-off socket for vm and gen, exchanges the
// version records on it, and waits for the HandoffSocketAck.
func (h *harness) addVM(vm string, gen uint64) *netd {
	h.t.Helper()
	return h.addVMWith(handoffSocket(vm, gen))
}

// addFreshVM is addVM with a fresh HandoffSocket, as at a start that
// cleared the VM's state.
func (h *harness) addFreshVM(vm string, gen uint64) *netd {
	h.t.Helper()
	m := handoffSocket(vm, gen)
	m.GetHandoffSocket().Fresh = true
	return h.addVMWith(m)
}

// addVMWith is addVM with the HandoffSocket m.
func (h *harness) addVMWith(m *egressv1.HostdToProxyd) *netd {
	h.t.Helper()
	vm, gen := m.GetHandoffSocket().GetVm(), m.GetHandoffSocket().GetGeneration()
	nd := h.handoffWith(m)
	if err := nd.ch.Send(hostversion.Record(testVersion), nil); err != nil {
		h.t.Fatal(err)
	}
	ack := h.recvHostd().GetHandoffSocketAck()
	if ack.GetVm() != vm || ack.GetGeneration() != gen {
		h.t.Fatalf("HandoffSocketAck = %v, want %s %d", ack, vm, gen)
	}
	return nd
}

// handoff sends a HandoffSocket and reads wb-proxyd's version record on
// the new socket, which comes before the ack.
func (h *harness) handoff(vm string, gen uint64) *netd {
	h.t.Helper()
	return h.handoffWith(handoffSocket(vm, gen))
}

// handoffWith is handoff with the HandoffSocket m.
func (h *harness) handoffWith(m *egressv1.HostdToProxyd) *netd {
	h.t.Helper()
	vm, gen := m.GetHandoffSocket().GetVm(), m.GetHandoffSocket().GetGeneration()
	a, b, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		h.t.Fatal(err)
	}
	h.send(m, a)
	_ = a.Close()
	nd := &netd{t: h.t, raw: dup(h.t, b), vm: vm, gen: gen, next: 1 << 40}
	nd.ch = newChannel(h.t, b)
	h.t.Cleanup(func() { _ = nd.ch.Close() })
	checkVersion(h.t, nd.ch)
	return nd
}

func (n *netd) send(m *egressv1.NetdMessage, fd *os.File) {
	n.t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		n.t.Fatal(err)
	}
	if fd == nil {
		err = n.ch.Send(b, nil)
	} else {
		err = n.ch.Send(b, fd)
	}
	if err != nil {
		n.t.Fatal(err)
	}
}

func (n *netd) report(name string, addr uint32) {
	n.t.Helper()
	n.send(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
		NameReport: &egressv1.NameReport{Name: name, Address: addr},
	}}, nil)
}

func (n *netd) release(addr uint32) {
	n.t.Helper()
	n.send(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{Release: &egressv1.Release{Address: addr}}}, nil)
}

func streamMsg(gen uint64, addr, port uint32, id uint64) *egressv1.NetdMessage {
	return &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Stream{
		Stream: &egressv1.Stream{Generation: gen, Address: addr, Port: port, StreamId: id},
	}}
}

// stream hands a stream over with a fresh AF_UNIX SOCK_STREAM
// socketpair, and returns wb-netd's end of it.
func (n *netd) stream(gen uint64, addr, port uint32, id uint64) *os.File {
	n.t.Helper()
	ours, theirs := socketpair(n.t, syscall.SOCK_STREAM)
	n.send(streamMsg(gen, addr, port, id), theirs)
	_ = theirs.Close()
	return ours
}

// socketpair returns both ends of an AF_UNIX socketpair of the type.
func socketpair(t *testing.T, sockType int) (a, b *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, sockType, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Non-blocking, so os.NewFile polls them and reads take deadlines.
	for _, fd := range fds {
		if err := syscall.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
	}
	a, b = os.NewFile(uintptr(fds[0]), "a"), os.NewFile(uintptr(fds[1]), "b")
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

// expectReset reads the next message on the hand-off socket and checks
// it is a StreamReset for id.
func (n *netd) expectReset(id uint64) {
	n.t.Helper()
	m := recv(n.t, n.ch)
	got, ref := egressmsg.DecodeProxydMessage(m.Record, m.File != nil)
	if ref != nil || got != id {
		n.t.Fatalf("got a StreamReset for %d (%v), want %d", got, ref, id)
	}
}

// barrier sends a stream to an address no copy holds and waits for its
// reset. wb-proxyd decides a socket's messages in order, so every
// message sent before it has been decided once the reset arrives.
func (n *netd) barrier() {
	n.t.Helper()
	n.next++
	_ = n.stream(n.gen, wire(4000), 443, n.next)
	n.expectReset(n.next)
}

// expectEnded checks that wb-proxyd closed the hand-off socket.
func (n *netd) expectEnded() {
	n.t.Helper()
	if err := n.ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		n.t.Fatal(err)
	}
	for {
		m, err := n.ch.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			n.t.Fatalf("recv on a closed hand-off socket: %v, want EOF", err)
		}
		_ = m.Close()
	}
}

// expectEvent waits for a refusal of the rule and check for vm.
func (h *harness) expectEvent(vm, rule string, check egressmsg.Check) {
	h.t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case e := <-h.audit.events:
			if e.VM == vm && e.Rule == rule && e.Check == check {
				return
			}
		case <-timeout:
			h.t.Fatalf("no refusal %s %s for %q; log:\n%s", rule, check, vm, h.logs.String())
		}
	}
}

// expectStream waits for the handler's next stream.
func (h *harness) expectStream() *Stream {
	h.t.Helper()
	select {
	case s := <-h.handler.streams:
		h.t.Cleanup(func() { s.Close() })
		return s
	case <-time.After(wait):
		h.t.Fatalf("the handler got no stream; log:\n%s", h.logs.String())
		return nil
	}
}

// noStream checks the handler got no stream.
func (h *harness) noStream() {
	h.t.Helper()
	select {
	case s := <-h.handler.streams:
		h.t.Fatalf("the handler got a stream to %s:%d", s.Name, s.Port)
	default:
	}
}

// expectLog checks the log holds a record with each of the parts.
func (h *harness) expectLog(parts ...string) {
	h.t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		for line := range strings.SplitSeq(h.logs.String(), "\n") {
			if containsAll(line, parts) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("no log record with %q; log:\n%s", parts, h.logs.String())
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// expectServeEnded checks that Serve returned an error.
func (h *harness) expectServeEnded() {
	h.t.Helper()
	select {
	case err := <-h.served:
		if err == nil {
			h.t.Fatal("Serve returned nil after a refusal")
		}
	case <-time.After(wait):
		h.t.Fatal("Serve didn't end the control channel")
	}
}

// expectEOF checks that the other end of a stream's socket closed.
func expectEOF(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read: %d, %v, want EOF", n, err)
	}
}

// expectOpen checks that the other end of a stream's socket is still
// open: a read waits until its deadline.
func expectOpen(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read: %v, want the deadline", err)
	}
}
