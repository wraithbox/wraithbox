package fdchan

import (
	"errors"
	"io"
	"syscall"
	"testing"
	"time"
)

// TestTryRecv reads every queued message, a descriptor among them, then
// returns ErrNoMessage at once, even with a read deadline that passed.
// After the peer closed, it returns io.EOF.
func TestTryRecv(t *testing.T) {
	a, b := pair(t)
	if _, err := b.TryRecv(); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("empty: %v, want ErrNoMessage", err)
	}
	r, _ := pipe(t)
	for _, rec := range []string{"one", "two"} {
		if err := a.Send([]byte(rec), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Send([]byte("three"), r); err != nil {
		t.Fatal(err)
	}
	if err := b.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one", "two", "three"} {
		m, err := b.TryRecv()
		if err != nil || string(m.Record) != want {
			t.Fatalf("got %v, %v, want %q", m, err, want)
		}
		if want == "three" {
			if m.File == nil || !cloexec(t, m.File) {
				t.Fatalf("descriptor %v missing or not close-on-exec", m.File)
			}
			_ = m.Close()
		}
	}
	start := time.Now()
	if _, err := b.TryRecv(); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("drained: %v, want ErrNoMessage", err)
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("TryRecv took %s on an empty channel", took)
	}
	_ = a.Close()
	if _, err := b.TryRecv(); !errors.Is(err, io.EOF) {
		t.Fatalf("closed peer: %v, want io.EOF", err)
	}
}

// TestTryRecvRefuses refuses a message with two descriptors, as Recv
// does, and the channel goes on.
func TestTryRecvRefuses(t *testing.T) {
	raw, ch := rawPair(t)
	r, w := pipe(t)
	if err := syscall.Sendmsg(raw, []byte("two"), syscall.UnixRights(int(r.Fd()), int(w.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ch.TryRecv()
	wantRefusal(t, err, RuleFDCount)
	if err := syscall.Sendmsg(raw, []byte("next"), nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if m, err := ch.TryRecv(); err != nil || string(m.Record) != "next" {
		t.Fatalf("got %v, %v", m, err)
	}
}

// TestStreamPair makes a connected AF_UNIX SOCK_STREAM pair whose ends
// are close-on-exec and carry bytes both ways.
func TestStreamPair(t *testing.T) {
	a, b, err := StreamPair()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	for _, f := range []syscall.Conn{a, b} {
		family, sockType, err := SocketType(f)
		if err != nil || family != syscall.AF_UNIX || sockType != syscall.SOCK_STREAM {
			t.Fatalf("%d, %d, %v, want AF_UNIX SOCK_STREAM", family, sockType, err)
		}
	}
	if !cloexec(t, a) || !cloexec(t, b) {
		t.Fatal("an end isn't close-on-exec")
	}
	if _, err := a.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(b, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v", buf, err)
	}
}
