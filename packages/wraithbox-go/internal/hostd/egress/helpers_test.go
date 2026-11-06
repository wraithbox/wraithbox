package egress

import (
	"bytes"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// own is the build version both ends of a test channel carry.
const own = "1.2.3-dev+abc"

// wait bounds every wait in the tests.
const wait = 3 * time.Second

// fakeConn hands Recv the pushed messages, then io.EOF once its input
// is closed, or os.ErrClosed once Close was called. It records each
// record sent and whether a descriptor came with it.
type fakeConn struct {
	in     chan received
	sent   chan sent
	closed chan struct{}
	once   sync.Once

	mu      sync.Mutex
	sendErr error
}

type received struct {
	m   *fdchan.Message
	err error
}

type sent struct {
	record []byte
	hasFD  bool
}

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan received, 64), sent: make(chan sent, 64), closed: make(chan struct{})}
}

func (c *fakeConn) push(record []byte) { c.in <- received{m: &fdchan.Message{Record: record}} }

func (c *fakeConn) pushMsg(t *testing.T, m proto.Message) {
	t.Helper()
	c.push(marshal(t, m))
}

func (c *fakeConn) pushErr(err error) { c.in <- received{err: err} }

func (c *fakeConn) Recv() (*fdchan.Message, error) {
	select {
	case r, ok := <-c.in:
		if !ok {
			return nil, io.EOF
		}
		return r.m, r.err
	case <-c.closed:
		return nil, os.ErrClosed
	}
}

func (c *fakeConn) Send(record []byte, fd syscall.Conn) error {
	c.mu.Lock()
	err := c.sendErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.sent <- sent{record: bytes.Clone(record), hasFD: fd != nil}
	return nil
}

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// next returns the next record sent, within wait.
func (c *fakeConn) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-c.sent:
		return s
	case <-time.After(wait):
		t.Fatal("nothing was sent")
		return sent{}
	}
}

// nextControl returns the next control message sent to wb-proxyd.
func (c *fakeConn) nextControl(t *testing.T) (*egressv1.HostdToProxyd, bool) {
	t.Helper()
	s := c.next(t)
	m, ref := egressmsg.DecodeHostdToProxyd(s.record, s.hasFD)
	if ref != nil {
		t.Fatalf("wb-hostd sent a message wb-proxyd refuses: %v", ref)
	}
	return m, s.hasFD
}

// expectVersion checks that the next record sent is the version record.
func (c *fakeConn) expectVersion(t *testing.T) {
	t.Helper()
	s := c.next(t)
	if _, err := hostversion.CheckRecord(nil, own, s.record); err != nil || s.hasFD {
		t.Fatalf("the first record isn't the version record: %v", err)
	}
}

// ender records each End.
type ender struct {
	mu    sync.Mutex
	rules []string
	ended chan struct{}
	once  sync.Once
}

func newEnder() *ender { return &ender{ended: make(chan struct{})} }

func (e *ender) End(rule string) {
	e.mu.Lock()
	e.rules = append(e.rules, rule)
	e.mu.Unlock()
	e.once.Do(func() { close(e.ended) })
}

func (e *ender) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.rules...)
}

// expect waits for the first End and checks every End so far was rule.
func (e *ender) expect(t *testing.T, rule string) {
	t.Helper()
	select {
	case <-e.ended:
	case <-time.After(wait):
		t.Fatalf("not ended, want %s", rule)
	}
	if got := e.got(); len(got) != 1 || got[0] != rule {
		t.Fatalf("ended with %q, want once with %s", got, rule)
	}
}

// syncBuffer is a log sink safe for concurrent use.
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

func newLog() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// expectLog checks, within wait, that one log record holds every part.
func expectLog(t *testing.T, buf *syncBuffer, parts ...string) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if containsAll(line, parts) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log record with %q; log:\n%s", parts, buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func marshal(t testing.TB, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wire is the wire form of the synthetic address 198.18.0.0 + i.
func wire(i uint32) uint32 { return 198<<24 | 18<<16 | i }

func synth(i uint32) netip.Addr { return egressmsg.AddrFromWire(wire(i)) }

func entryRecord(t testing.TB, name string, addr uint32) []byte {
	t.Helper()
	return marshal(t, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingEntry{
		MappingEntry: &egressv1.MappingEntry{Name: name, Address: addr},
	}})
}

func releaseRecord(t testing.TB, addr uint32) []byte {
	t.Helper()
	return marshal(t, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingRelease{
		MappingRelease: &egressv1.MappingRelease{Address: addr},
	}})
}

func debitRecord(t testing.TB, name string) []byte {
	t.Helper()
	return marshal(t, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_WildcardDebit{
		WildcardDebit: &egressv1.WildcardDebit{Name: name},
	}})
}

func proxydNameRecord(t testing.TB, vm, name string) []byte {
	t.Helper()
	return marshal(t, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_ProxydWildcardName{
		ProxydWildcardName: &egressv1.ProxydWildcardName{Vm: vm, Name: name},
	}})
}
