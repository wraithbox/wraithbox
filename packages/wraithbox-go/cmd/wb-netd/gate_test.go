package main

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// fakeLink is a transport whose reads come from frames. Close ends a
// read in progress with io.EOF.
type fakeLink struct {
	frames chan []byte
	done   chan struct{}
}

func newFakeLink() *fakeLink {
	return &fakeLink{frames: make(chan []byte), done: make(chan struct{})}
}

func (f *fakeLink) ReadFrame(buf []byte) (int, error) {
	select {
	case b := <-f.frames:
		return copy(buf, b), nil
	case <-f.done:
		return 0, io.EOF
	}
}

func (f *fakeLink) WriteFrame([]byte) error { return nil }

func (f *fakeLink) Close() error {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}

// TestGateDrains checks that frames before open are read and dropped
// with link-not-started, so the sender never waits, and that the stack
// reads the frames after open, the one in flight first
// (S04-09-netd-start, "Drain before the start").
func TestGateDrains(t *testing.T) {
	f := newFakeLink()
	rec := netd.NewRecorderWith(slog.New(slog.DiscardHandler), nil)
	g := newGate(f, netd.DefaultMTU, rec)
	t.Cleanup(func() { _ = g.Close() })
	for range 100 {
		select {
		case f.frames <- []byte("early"):
		case <-time.After(5 * time.Second):
			t.Fatal("a frame before the start waited")
		}
	}
	// The last early frame is counted before open, so it isn't the one
	// in flight.
	deadline := time.Now().Add(5 * time.Second)
	for rec.Count(netd.RuleLinkNotStarted) < 100 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	g.open()
	f.frames <- []byte("first")
	go func() { f.frames <- []byte("second") }()
	buf := make([]byte, 64)
	for _, want := range []string{"first", "second"} {
		n, err := g.ReadFrame(buf)
		if err != nil || string(buf[:n]) != want {
			t.Fatalf("ReadFrame = %q, %v; want %q", buf[:n], err, want)
		}
	}
	if n := rec.Count(netd.RuleLinkNotStarted); n != 100 {
		t.Fatalf("link-not-started counted %d frames, want 100", n)
	}
}

// TestGateClosed checks that a transport that ends during the drain
// gives the stack's first read its error.
func TestGateClosed(t *testing.T) {
	f := newFakeLink()
	g := newGate(f, netd.DefaultMTU, netd.NewRecorderWith(slog.New(slog.DiscardHandler), nil))
	_ = g.Close()
	g.open()
	if _, err := g.ReadFrame(make([]byte, 64)); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame = %v, want io.EOF", err)
	}
}
