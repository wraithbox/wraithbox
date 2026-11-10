package handoff

import (
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"
)

// openStream returns an open stream on a socket of its own.
func openStream(t *testing.T) *Stream {
	t.Helper()
	r, err := NewReceiver(Config{
		Handler: nopHandler{}, Wildcard: nopWildcard{},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	vm := &vmState{slot: "work", copy: newMapping()}
	sock := newSocket(r, vm, 1, &failConn{})
	vm.sock = sock
	c, peer := net.Pipe()
	t.Cleanup(func() { _ = c.Close(); _ = peer.Close() })
	s := &Stream{VM: "work", Generation: 1, ID: 1, Conn: c, sock: sock}
	sock.streams[s] = struct{}{}
	return s
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// TestSendWhileOpenEnded checks that once a stream has ended,
// SendWhileOpen runs no send and returns ErrEnded (I598).
func TestSendWhileOpenEnded(t *testing.T) {
	s := openStream(t)
	if !s.Close() {
		t.Fatal("Close didn't end the stream")
	}
	ran := false
	err := s.SendWhileOpen(func() error { ran = true; return nil })
	if !errors.Is(err, ErrEnded) || ran {
		t.Errorf("SendWhileOpen after the end = %v, send ran %v; want ErrEnded and no send", err, ran)
	}
}

// TestSendWhileOpenEndWaits ends a stream while a send runs. The end
// waits for the send: Done isn't closed within 100 ms while the send
// still runs, it closes once the send returns, and a send after the
// end doesn't run (I598).
func TestSendWhileOpenEndWaits(t *testing.T) {
	s := openStream(t)
	endedBy := make(chan bool, 1)
	err := s.SendWhileOpen(func() error {
		go func() { endedBy <- s.Close() }()
		select {
		case <-s.Done():
			t.Error("Done closed while a send ran")
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatalf("SendWhileOpen on an open stream: %v", err)
	}
	select {
	case ended := <-endedBy:
		if !ended {
			t.Error("Close didn't end the stream")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close didn't return after the send")
	}
	if !isClosed(s.Done()) {
		t.Error("Done isn't closed after the end")
	}
	if err := s.SendWhileOpen(func() error { return nil }); !errors.Is(err, ErrEnded) {
		t.Errorf("SendWhileOpen after the end = %v, want ErrEnded", err)
	}
}

// TestSendWhileOpenError returns the send's own error.
func TestSendWhileOpenError(t *testing.T) {
	s := openStream(t)
	want := errors.New("write failed")
	if err := s.SendWhileOpen(func() error { return want }); !errors.Is(err, want) {
		t.Errorf("SendWhileOpen = %v, want %v", err, want)
	}
}
