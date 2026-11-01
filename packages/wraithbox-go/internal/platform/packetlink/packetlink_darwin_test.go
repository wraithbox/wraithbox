package packetlink

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// socketpair returns both ends of an AF_UNIX socketpair of a type. The
// test closes them at its end.
func socketpair(t *testing.T, typ int) (a, b *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, typ, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b = os.NewFile(uintptr(fds[0]), "host"), os.NewFile(uintptr(fds[1]), "guest")
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

// guestEnd raises the socket buffers of the end that stands in for the
// VM's network device, as the attachment does, so that it can send the
// largest frame.
func guestEnd(t *testing.T, f *os.File) {
	t.Helper()
	snd, rcv := bufferSizes(MaxMTU)
	for opt, v := range map[int]int{syscall.SO_SNDBUF: snd, syscall.SO_RCVBUF: rcv} {
		if err := syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_SOCKET, opt, v); err != nil {
			t.Fatal(err)
		}
	}
}

func newTransport(t *testing.T) (*Transport, *os.File) {
	t.Helper()
	host, guest := socketpair(t, syscall.SOCK_DGRAM)
	guestEnd(t, guest)
	tr, err := New(host, MaxMTU)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr, guest
}

// TestTransportBuffers checks the socket buffers after setup
// (S07-02-packet-path, "Stack").
func TestTransportBuffers(t *testing.T) {
	tr, _ := newTransport(t)
	snd, rcv, err := tr.Buffers()
	if err != nil {
		t.Fatal(err)
	}
	wantSnd, wantRcv := bufferSizes(MaxMTU)
	if snd != wantSnd || rcv != wantRcv {
		t.Errorf("buffers %d and %d, want %d and %d", snd, rcv, wantSnd, wantRcv)
	}
	if snd < MaxFrame(MaxMTU) || rcv < 2*snd {
		t.Errorf("buffers %d and %d don't hold the largest frame twice over", snd, rcv)
	}
}

// TestTransportRoundTrip sends a frame of the largest size each way.
func TestTransportRoundTrip(t *testing.T) {
	tr, guest := newTransport(t)
	frame := bytes.Repeat([]byte{0xa5}, MaxFrame(MaxMTU))
	if MaxFrame(MaxMTU) != 65549 {
		t.Fatalf("largest frame %d, want 65549", MaxFrame(MaxMTU))
	}
	if _, err := guest.Write(frame); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, ReadBufferSize(MaxMTU))
	n, err := tr.ReadFrame(buf)
	if err != nil || n != len(frame) || !bytes.Equal(buf[:n], frame) {
		t.Fatalf("ReadFrame = %d, %v, want %d bytes", n, err, len(frame))
	}
	if err := tr.WriteFrame(frame); err != nil {
		t.Fatal(err)
	}
	n, err = guest.Read(buf)
	if err != nil || n != len(frame) || !bytes.Equal(buf[:n], frame) {
		t.Fatalf("guest read = %d, %v, want %d bytes", n, err, len(frame))
	}
}

// TestTransportTruncated sends a datagram longer than the read buffer
// and sees the read fill it, which the link endpoint drops with
// frame-truncated.
func TestTransportTruncated(t *testing.T) {
	tr, guest := newTransport(t)
	if _, err := guest.Write(make([]byte, ReadBufferSize(MaxMTU)+100)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, ReadBufferSize(MaxMTU))
	n, err := tr.ReadFrame(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("ReadFrame = %d, %v, want a full buffer of %d", n, err, len(buf))
	}
}

// TestTransportBusy fills the peer's receive buffer and sees WriteFrame
// fail with ErrBusy instead of waiting.
func TestTransportBusy(t *testing.T) {
	tr, _ := newTransport(t)
	frame := make([]byte, MaxFrame(MaxMTU))
	for range 1000 {
		err := tr.WriteFrame(frame)
		if errors.Is(err, ErrBusy) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("1000 frames written to a peer that reads none, want ErrBusy")
}

// TestTransportPeerClosed closes the peer while a read waits, and sees
// io.EOF.
func TestTransportPeerClosed(t *testing.T) {
	tr, guest := newTransport(t)
	done := make(chan error, 1)
	go func() {
		_, err := tr.ReadFrame(make([]byte, ReadBufferSize(MaxMTU)))
		done <- err
	}()
	_ = guest.Close()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after the peer closed = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFrame still waits 5 s after the peer closed")
	}
}

// TestNewRefuses feeds New descriptors it must refuse, and sees each
// with its rule.
func TestNewRefuses(t *testing.T) {
	stream, _ := socketpair(t, syscall.SOCK_STREAM)
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	unconnected := os.NewFile(uintptr(fd), "unconnected")
	t.Cleanup(func() { _ = unconnected.Close() })
	notSocket, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    *os.File
		rule Rule
	}{
		{"SOCK_STREAM", stream, RuleSocketType},
		{"not a socket", notSocket, RuleSocketType},
		{"no peer", unconnected, RuleSocketUnconnected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.f, MaxMTU)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tt.rule {
				t.Fatalf("New = %v, want a refusal with %s", err, tt.rule)
			}
			if err := tt.f.Close(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("New left its file open: Close = %v", err)
			}
		})
	}
	host, _ := socketpair(t, syscall.SOCK_DGRAM)
	if _, err := New(host, MinMTU-1); err == nil {
		t.Error("New took an MTU below the attachment's range")
	}
}
