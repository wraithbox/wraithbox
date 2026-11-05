package stack

import (
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// TestStreamConnEnds checks the two ends a stream hook gives a guest
// connection (S07-05-stream-hand-off, "Streams"). CloseWrite sends the
// guest a FIN, and the guest can still send. Reset sends it an RST.
func TestStreamConnEnds(t *testing.T) {
	conns := make(sink, 4)
	g := newGuest(t)
	g.attach(t, nt.Config(), netd.Hooks{Stream: policy(func(netd.Flow) netd.StreamDecision {
		return netd.StreamDecision{Sink: conns}
	})})
	dest := netip.AddrPortFrom(synthetic, 443)

	c, err := g.dial(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server, ok := conns.take(t).(netd.StreamConn)
	if !ok {
		t.Fatal("the accepted connection isn't a netd.StreamConn")
	}
	if err := server.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(dialTimeout))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("guest read after CloseWrite = %v, want io.EOF", err)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v after the half-close", buf, err)
	}
	_ = server.Close()

	c2, err := g.dial(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	server2, ok := conns.take(t).(netd.StreamConn)
	if !ok {
		t.Fatal("the accepted connection isn't a netd.StreamConn")
	}
	server2.Reset()
	_ = server2.Close()
	_ = c2.SetReadDeadline(time.Now().Add(dialTimeout))
	_, err = c2.Read(make([]byte, 1))
	if err == nil || !strings.Contains(err.Error(), "reset") {
		t.Fatalf("guest read after Reset = %v, want a reset", err)
	}
}
