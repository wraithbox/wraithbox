package vmhandoff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// syncBuffer is a log sink the receiver's goroutine and the test share.
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

// fixture is a receiver over a real datagram socketpair, and the raw
// sending end, so a test can send what wb-vmd never should.
type fixture struct {
	rx   *Receiver
	send int
	log  *syncBuffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	log := &syncBuffer{}
	rx, err := NewReceiver(os.NewFile(uintptr(fds[1]), "handoff"), slog.New(slog.NewJSONHandler(log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rx.Close()
		_ = syscall.Close(fds[0])
	})
	return &fixture{rx: rx, send: fds[0], log: log}
}

// sendRaw sends one datagram with fds attached, and closes the test's
// copies of fds, so that only the receiver's copies remain.
func (f *fixture) sendRaw(t *testing.T, record []byte, fds ...int) {
	t.Helper()
	var oob []byte
	if len(fds) > 0 {
		oob = syscall.UnixRights(fds...)
	}
	if err := syscall.Sendmsg(f.send, record, oob, nil, 0); err != nil {
		t.Fatalf("sendmsg: %v", err)
	}
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
}

// streamPair returns a connected AF_UNIX stream socketpair: the guest
// connection's descriptor and the side that stands in for the guest.
func streamPair(t *testing.T) (conn, guest int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fds[0], fds[1]
}

// openFDs returns the descriptors open in this process, by fcntl.
func openFDs() map[int]bool {
	open := map[int]bool{}
	for fd := range 4096 {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			open[fd] = true
		}
	}
	return open
}

// ruleOf is the rule of a refusal of this package or of fdchan.
func ruleOf(err error) string {
	if r, ok := errors.AsType[*Refusal](err); ok {
		return string(r.Rule)
	}
	if r, ok := errors.AsType[*fdchan.Refusal](err); ok {
		return string(r.Rule)
	}
	return ""
}

// pipeEnd returns the read end of a new pipe.
func pipeEnd(t *testing.T) []int {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(p[1])
	return p[:1]
}

// dgramEnd returns one end of a new datagram socketpair.
func dgramEnd(t *testing.T) []int {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fds[1])
	return fds[:1]
}

// streamEnd returns one end of a new stream socketpair, and closes the
// other when the test ends.
func streamEnd(t *testing.T) int {
	c, g := streamPair(t)
	t.Cleanup(func() { _ = syscall.Close(g) })
	return c
}

func oneStream(t *testing.T) []int { return []int{streamEnd(t)} }

func twoStreams(t *testing.T) []int { return []int{streamEnd(t), streamEnd(t)} }

func noFDs(*testing.T) []int { return nil }

// changed returns the record of good with change applied.
func changed(change func(*vmdv1.HandoffMessage)) func(*testing.T) []byte {
	return func(t *testing.T) []byte {
		m := good()
		change(m)
		return marshal(t, m)
	}
}

func bytesOf(b []byte) func(*testing.T) []byte { return func(*testing.T) []byte { return b } }

var goodRecord = changed(func(*vmdv1.HandoffMessage) {})

// refusalCase is one record the receiver refuses.
type refusalCase struct {
	name   string
	record func(*testing.T) []byte
	fds    func(*testing.T) []int
	rule   string
}

var refusalCases = []refusalCase{
	{"no descriptor", goodRecord, noFDs, string(RuleFDCount)},
	{"two descriptors", goodRecord, twoStreams, string(fdchan.RuleFDCount)},
	{"a pipe", goodRecord, pipeEnd, string(RuleSocketType)},
	{"a datagram socket", goodRecord, dgramEnd, string(RuleSocketType)},
	{"over 256 bytes", bytesOf(make([]byte, MaxRecord+1)), oneStream, string(fdchan.RuleRecordSize)},
	// Go's Sendmsg sends one byte when the record is empty and has a
	// descriptor, so the empty record goes without one.
	{"empty", bytesOf(nil), noFDs, string(fdchan.RuleRecordSize)},
	{"not protobuf", bytesOf([]byte{0xff, 0xff}), oneStream, string(RuleMalformed)},
	{"generation 0", changed(func(m *vmdv1.HandoffMessage) { m.Generation = 0 }), oneStream, string(RuleGeneration)},
	{"older generation", changed(func(m *vmdv1.HandoffMessage) { m.Generation = 2 }), oneStream, string(RuleGeneration)},
	{"request ID 0", changed(func(m *vmdv1.HandoffMessage) { m.RequestId = 0 }), oneStream, string(RuleUnexpected)},
	{"request not open", changed(func(m *vmdv1.HandoffMessage) { m.RequestId = 8 }), oneStream, string(RuleUnexpected)},
	{"other port", changed(func(m *vmdv1.HandoffMessage) { m.DestinationPort = 1024 }), oneStream, string(RuleUnexpected)},
	{"no direction", changed(func(m *vmdv1.HandoffMessage) { m.Direction = 0 }), oneStream, string(RuleDirection)},
}

// TestReceiveRefusals is C11-host-daemons, "Hand-off receiver": over a
// real socketpair, each refusal of the receiver and of its transport has
// its rule, is logged with it, and leaves no descriptor of the record
// open, checked with fcntl after the call.
func TestReceiveRefusals(t *testing.T) {
	for _, c := range refusalCases {
		t.Run(c.name, func(t *testing.T) { checkRefusal(t, c) })
	}
}

func checkRefusal(t *testing.T, c refusalCase) {
	f := newFixture(t)
	if _, err := f.rx.Expect(7, 3, 1023); err != nil {
		t.Fatal(err)
	}
	f.sendRaw(t, c.record(t), c.fds(t)...)
	before := openFDs()
	err := f.rx.Receive()
	after := openFDs()
	if got := ruleOf(err); got != c.rule {
		t.Fatalf("Receive = %v, want rule %s", err, c.rule)
	}
	if !maps.Equal(before, after) {
		t.Errorf("descriptors open after the refusal: %v, before: %v", keys(after), keys(before))
	}
	if _, ok := errors.AsType[*Refusal](err); ok && !strings.Contains(f.log.String(), `"rule":"`+c.rule+`"`) {
		t.Errorf("log %s has no rule %s", f.log.String(), c.rule)
	}
}

func keys(m map[int]bool) []int { return slices.Sorted(maps.Keys(m)) }

// TestReceiveAccepted: a good record gives its request a working
// connection, and the receiver keeps no copy of the descriptor.
func TestReceiveAccepted(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.Expect(7, 3, 1023)
	if err != nil {
		t.Fatal(err)
	}
	c, guest := streamPair(t)
	defer func() { _ = syscall.Close(guest) }()
	f.sendRaw(t, marshal(t, good()), c)
	before := openFDs()
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := p.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := syscall.Read(guest, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("guest read %q, %v", buf, err)
	}
	_ = conn.Close()
	if after := openFDs(); !maps.Equal(before, after) {
		t.Errorf("descriptors open after the connection closed: %v, before: %v", keys(after), keys(before))
	}
	if !strings.Contains(f.log.String(), `"decision":"accept"`) {
		t.Errorf("log %s has no accept", f.log.String())
	}
}

// TestRefusalReachesRequest: a record for an open request that fails a
// check gives the request the refusal, and a second record for it is
// refused as unexpected.
func TestRefusalReachesRequest(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.Expect(7, 3, 1023)
	if err != nil {
		t.Fatal(err)
	}
	m := good()
	m.DestinationPort = 22
	c, guest := streamPair(t)
	defer func() { _ = syscall.Close(guest) }()
	f.sendRaw(t, marshal(t, m), c)
	if err := f.rx.Receive(); ruleOf(err) != string(RuleUnexpected) {
		t.Fatalf("Receive = %v", err)
	}
	if _, err := p.Wait(t.Context()); ruleOf(err) != string(RuleUnexpected) {
		t.Fatalf("Wait = %v, want %s", err, RuleUnexpected)
	}
	c2, guest2 := streamPair(t)
	defer func() { _ = syscall.Close(guest2) }()
	f.sendRaw(t, marshal(t, good()), c2)
	if err := f.rx.Receive(); ruleOf(err) != string(RuleUnexpected) {
		t.Fatalf("second record: Receive = %v, want %s", err, RuleUnexpected)
	}
}

// TestCancelAndClose: a cancelled request refuses its late record, a
// newer generation fails the older requests, Close fails the open ones,
// and Run returns nil once the channel is closed.
func TestCancelAndClose(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.Expect(7, 3, 1023)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
	c, guest := streamPair(t)
	defer func() { _ = syscall.Close(guest) }()
	f.sendRaw(t, marshal(t, good()), c)
	if err := f.rx.Receive(); ruleOf(err) != string(RuleUnexpected) {
		t.Fatalf("late record: Receive = %v, want %s", err, RuleUnexpected)
	}

	old, err := f.rx.Expect(8, 3, 1023)
	if err != nil {
		t.Fatal(err)
	}
	open, err := f.rx.Expect(1, 4, 1023)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Wait(t.Context()); ruleOf(err) != string(RuleGeneration) {
		t.Fatalf("older generation: Wait = %v, want %s", err, RuleGeneration)
	}

	done := make(chan error, 1)
	go func() { done <- f.rx.Run() }()
	if err := f.rx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := open.Wait(t.Context()); ruleOf(err) != string(RuleClosed) {
		t.Fatalf("after Close: Wait = %v, want %s", err, RuleClosed)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return after Close")
	}
	if _, err := f.rx.Expect(9, 4, 1023); ruleOf(err) != string(RuleClosed) {
		t.Fatalf("Expect after Close = %v, want %s", err, RuleClosed)
	}
}

// TestRunEndsAtPeerClose: Run skips a refused record and returns nil
// when wb-vmd closes its end.
func TestRunEndsAtPeerClose(t *testing.T) {
	f := newFixture(t)
	f.sendRaw(t, []byte{0xff})
	_ = syscall.Close(f.send)
	if err := f.rx.Run(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Run = %v, want nil", err)
	}
}
