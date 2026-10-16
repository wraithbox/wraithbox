package fdchan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDarwinControlLayout checks the layout decide reads against the
// syscall package.
func TestDarwinControlLayout(t *testing.T) {
	lay := darwinControl
	checks := []struct {
		name      string
		got, want int
	}{
		{"CMSG_LEN(0)", lay.hdrLen, syscall.CmsgLen(0)},
		{"sizeof(struct cmsghdr)", lay.hdrLen, syscall.SizeofCmsghdr},
		{"CMSG_SPACE(1)", alignUp(lay.hdrLen, lay.align) + alignUp(1, lay.align), syscall.CmsgSpace(1)},
		{"SOL_SOCKET", int(lay.solSocket), syscall.SOL_SOCKET},
		{"SCM_RIGHTS", int(lay.scmRights), syscall.SCM_RIGHTS},
		{"MSG_TRUNC", lay.msgTrunc, syscall.MSG_TRUNC},
		{"MSG_CTRUNC", lay.msgCtrunc, syscall.MSG_CTRUNC},
		{"rights(7, 8)", len(rights(7, 8)), len(syscall.UnixRights(7, 8))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if !bytes.Equal(rights(7, 8), syscall.UnixRights(7, 8)) {
		t.Errorf("rights(7, 8) = %v, want %v", rights(7, 8), syscall.UnixRights(7, 8))
	}
}

// testMax is the maximum record size of the test channels, the size of
// the records of S04-08-records-and-bounds.
const testMax = 4096

// pair returns two channels of one socketpair.
func pair(t *testing.T) (a, b *Channel) {
	t.Helper()
	fa, fb, err := Socketpair(testMax)
	if err != nil {
		t.Fatal(err)
	}
	a, err = New(fa, testMax)
	if err != nil {
		t.Fatal(err)
	}
	b, err = New(fb, testMax)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

// rawPair returns a channel and the raw descriptor of its peer, for
// sends that bypass Send.
func rawPair(t *testing.T) (raw int, ch *Channel) {
	t.Helper()
	fa, fb, err := Socketpair(testMax)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fa.Close() })
	ch, err = New(fb, testMax)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return int(fa.Fd()), ch
}

func pipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// openFDs counts the open descriptors of the process.
func openFDs(t *testing.T) int {
	t.Helper()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	n := 0
	for fd := range int(min(lim.Cur, 1<<16)) {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) == nil {
			n++
		}
	}
	return n
}

func cloexec(t *testing.T, f *os.File) bool {
	t.Helper()
	rc, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags uintptr
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		flags, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if errno != 0 {
		t.Fatal(errno)
	}
	return flags&syscall.FD_CLOEXEC != 0
}

func wantRefusal(t *testing.T, err error, rule Rule) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal with %s", err, rule)
	}
	if r.Rule != rule {
		t.Fatalf("rule = %s, want %s (%v)", r.Rule, rule, err)
	}
}

// TestRoundTrip sends a record without and with a descriptor. The
// received descriptor works and is close-on-exec.
func TestRoundTrip(t *testing.T) {
	a, b := pair(t)
	if err := a.Send([]byte("no descriptor"), nil); err != nil {
		t.Fatal(err)
	}
	m, err := b.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Record) != "no descriptor" || m.File != nil {
		t.Fatalf("got %q with file %v", m.Record, m.File)
	}

	r, w := pipe(t)
	if err := a.Send([]byte("with descriptor"), w); err != nil {
		t.Fatal(err)
	}
	m, err = b.Recv()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if string(m.Record) != "with descriptor" || m.File == nil {
		t.Fatalf("got %q with file %v", m.Record, m.File)
	}
	if !cloexec(t, m.File) {
		t.Error("received descriptor is not close-on-exec")
	}
	if _, err := m.File.Write([]byte("through it")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 10)
	if _, err := io.ReadFull(r, got); err != nil || string(got) != "through it" {
		t.Fatalf("read %q, %v", got, err)
	}
}

// TestMaximumRecordWithDescriptor round-trips a 4 KiB record with one
// descriptor on a pair made for 4 KiB, above darwin's default datagram
// limit of 2048 bytes.
func TestMaximumRecordWithDescriptor(t *testing.T) {
	a, b := pair(t)
	_, w := pipe(t)
	rec := bytes.Repeat([]byte{0xa5}, 4096)
	if err := a.Send(rec, w); err != nil {
		t.Fatal(err)
	}
	m, err := b.Recv()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if !bytes.Equal(m.Record, rec) || m.File == nil {
		t.Fatalf("got %d bytes with file %v", len(m.Record), m.File)
	}
}

// TestRecordOverMaximum sees Send refuse a record one byte over the
// maximum, and Recv refuse one sent around Send, closing its descriptor.
func TestRecordOverMaximum(t *testing.T) {
	a, _ := pair(t)
	wantRefusal(t, a.Send(make([]byte, 4097), nil), RuleRecordSize)
	wantRefusal(t, a.Send(nil, nil), RuleRecordSize)

	raw, ch := rawPair(t)
	_, w := pipe(t)
	before := openFDs(t)
	if err := syscall.Sendmsg(raw, make([]byte, 4097), syscall.UnixRights(int(w.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ch.Recv()
	wantRefusal(t, err, RuleRecordSize)
	if after := openFDs(t); after != before {
		t.Errorf("open descriptors: %d before, %d after", before, after)
	}
}

// TestTwoDescriptors sends two pipes' write ends in one message. Recv
// refuses it and closes both: once the sender closes its copies, both
// read ends see end of file.
func TestTwoDescriptors(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	raw, ch := rawPair(t)
	r1, w1 := pipe(t)
	r2, w2 := pipe(t)
	if err := syscall.Sendmsg(raw, []byte("two"), syscall.UnixRights(int(w1.Fd()), int(w2.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ch.Recv()
	wantRefusal(t, err, RuleFDCount)
	_ = w1.Close()
	_ = w2.Close()
	for _, r := range []*os.File{r1, r2} {
		if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Errorf("read end: %d, %v, want EOF: a refused descriptor is still open", n, err)
		}
	}
	if !strings.Contains(logs.String(), "rule="+string(RuleFDCount)) || !strings.Contains(logs.String(), "decision=refuse") {
		t.Errorf("log %q has no refusal with %s", logs.String(), RuleFDCount)
	}
}

// TestThreeDescriptors sees a message with three descriptors refused,
// with as many descriptors open after it as before.
func TestThreeDescriptors(t *testing.T) {
	raw, ch := rawPair(t)
	_, w := pipe(t)
	fd := int(w.Fd())
	before := openFDs(t)
	if err := syscall.Sendmsg(raw, []byte("three"), syscall.UnixRights(fd, fd, fd), nil, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ch.Recv()
	wantRefusal(t, err, RuleFDCount)
	if after := openFDs(t); after != before {
		t.Errorf("open descriptors: %d before, %d after", before, after)
	}
}

// TestMostDescriptors sends the most descriptors darwin lets one message
// carry, and sees every one closed.
func TestMostDescriptors(t *testing.T) {
	raw, ch := rawPair(t)
	_, w := pipe(t)
	fds := make([]int, kernelMaxFDs)
	for i := range fds {
		fds[i] = int(w.Fd())
	}
	before := openFDs(t)
	if err := syscall.Sendmsg(raw, []byte("many"), syscall.UnixRights(fds...), nil, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ch.Recv()
	wantRefusal(t, err, RuleFDCount)
	if after := openFDs(t); after != before {
		t.Errorf("open descriptors: %d before, %d after", before, after)
	}
	more := slices.Concat(fds, []int{int(w.Fd())})
	if err := syscall.Sendmsg(raw, []byte("too many"), syscall.UnixRights(more...), nil, 0); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("send of %d descriptors: %v, want EINVAL: the control buffer may no longer hold every descriptor", len(more), err)
	}
}

// TestNewRefusesOtherSockets refuses a stream socket and a pipe as an
// inherited end.
func TestNewRefusesOtherSockets(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fds[1])
	_, err = New(os.NewFile(uintptr(fds[0]), "stream"), 4096)
	wantRefusal(t, err, RuleSocketType)

	r, _ := pipe(t)
	_, err = New(r, 4096)
	wantRefusal(t, err, RuleSocketType)
}

// TestSendToFullPeer fills the peer's buffer. Send then fails at once
// instead of blocking, and the record it failed is never sent later.
func TestSendToFullPeer(t *testing.T) {
	a, b := pair(t)
	if err := a.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sent, err := fill(a)
	if took := time.Since(start); err == nil || took > time.Second {
		t.Fatalf("after %d sends in %s: err = %v, want a failed send before the deadline", sent, took, err)
	}
	if !errors.Is(err, syscall.ENOBUFS) && !errors.Is(err, syscall.EAGAIN) {
		t.Errorf("err = %v, want ENOBUFS or EAGAIN", err)
	}
	t.Logf("%d records of %d bytes queued before %v", sent, testMax, err)

	if err := b.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for i := range sent {
		m, err := b.Recv()
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if got := binary.BigEndian.Uint32(m.Record); got != uint32(i) {
			t.Fatalf("record %d has sequence %d", i, got)
		}
	}
	if m, err := b.Recv(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("after the queued records: %v, %v, want the deadline: the failed record was sent later", m, err)
	}

	if err := a.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := a.Send([]byte("late"), nil); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("send after the deadline: %v, want os.ErrDeadlineExceeded", err)
	}
}

// fill sends numbered records of the maximum size until a send fails,
// and returns how many it sent and the error.
func fill(c *Channel) (int, error) {
	rec := make([]byte, testMax)
	for sent := range 1000 {
		binary.BigEndian.PutUint32(rec, uint32(sent))
		if err := c.Send(rec, nil); err != nil {
			return sent, err
		}
	}
	return 1000, nil
}

// TestClosedPeer reads the queued records, then io.EOF, and io.EOF again.
// A Recv that waits when the peer closes returns io.EOF too.
func TestClosedPeer(t *testing.T) {
	a, b := pair(t)
	if err := a.Send([]byte("last"), nil); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	if m, err := b.Recv(); err != nil || string(m.Record) != "last" {
		t.Fatalf("got %v, %v", m, err)
	}
	for range 2 {
		if _, err := b.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	}

	c, d := pair(t)
	done := make(chan error, 1)
	go func() {
		_, err := d.Recv()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = c.Close()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv still waits after the peer closed")
	}
}

// TestReadDeadline sees a deadline set while Recv waits end the wait at
// once, and a later deadline let it wait past the peer checks.
func TestReadDeadline(t *testing.T) {
	a, b := pair(t)
	done := make(chan error, 1)
	go func() {
		_, err := b.Recv()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if err := b.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want os.ErrDeadlineExceeded", err)
	}
	if took := time.Since(start); took > peerCheckInterval/2 {
		t.Errorf("Recv took %s to see the deadline", took)
	}

	if err := b.SetReadDeadline(time.Now().Add(3 * peerCheckInterval)); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(2 * peerCheckInterval)
		_ = a.Send([]byte("late"), nil)
	}()
	if m, err := b.Recv(); err != nil || string(m.Record) != "late" {
		t.Fatalf("got %v, %v", m, err)
	}
}

// TestSocketType reports the family and type of a few descriptors.
func TestSocketType(t *testing.T) {
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tl, ok := l.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener is %T", l)
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	stream := os.NewFile(uintptr(fds[0]), "stream")
	defer stream.Close()
	defer syscall.Close(fds[1])
	r, _ := pipe(t)

	for _, c := range []struct {
		name           string
		conn           syscall.Conn
		family, sockType int
	}{
		{"tcp listener", tl, syscall.AF_INET, syscall.SOCK_STREAM},
		{"unix stream", stream, syscall.AF_UNIX, syscall.SOCK_STREAM},
	} {
		family, sockType, err := SocketType(c.conn)
		if err != nil || family != c.family || sockType != c.sockType {
			t.Errorf("%s: %d, %d, %v, want %d, %d", c.name, family, sockType, err, c.family, c.sockType)
		}
	}
	if _, _, err := SocketType(r); !errors.Is(err, syscall.ENOTSOCK) {
		t.Errorf("pipe: %v, want ENOTSOCK", err)
	}
}

// TestNewChecksMaximum refuses a maximum outside 1 to MaxRecordLimit.
func TestNewChecksMaximum(t *testing.T) {
	for _, m := range []int{0, -1, MaxRecordLimit + 1} {
		if _, _, err := Socketpair(m); err == nil {
			t.Errorf("Socketpair(%d) succeeded", m)
		}
	}
	a, b, err := Socketpair(MaxRecordLimit)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	if _, err := New(a, MaxRecordLimit+1); err == nil {
		t.Error("New with a maximum over the limit succeeded")
	}
}
