package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// fakeTransport reads the frames sent on reads, and io.EOF once reads is
// closed. A read longer than the buffer is cut, as a datagram socket
// cuts it. WriteFrame fails with ErrBusy busy times, then takes frames.
type fakeTransport struct {
	reads chan []byte

	mu      sync.Mutex
	busy    int
	fail    error
	written [][]byte
	closed  bool
	done    chan struct{}
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{reads: make(chan []byte), done: make(chan struct{})}
}

func (t *fakeTransport) ReadFrame(buf []byte) (int, error) {
	select {
	case f, ok := <-t.reads:
		if !ok {
			return 0, io.EOF
		}
		return copy(buf, f), nil
	case <-t.done:
		return 0, io.EOF
	}
}

func (t *fakeTransport) WriteFrame(frame []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail != nil {
		return t.fail
	}
	if t.busy > 0 {
		t.busy--
		return fmt.Errorf("fake: %w", packetlink.ErrBusy)
	}
	t.written = append(t.written, append([]byte(nil), frame...))
	return nil
}

func (t *fakeTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.done)
	}
	return nil
}

func (t *fakeTransport) writes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.written)
}

// dispatcher counts the frames the endpoint delivers.
type dispatcher struct {
	mu        sync.Mutex
	delivered int
}

func (d *dispatcher) DeliverNetworkPacket(tcpip.NetworkProtocolNumber, *stack.PacketBuffer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.delivered++
}

func (*dispatcher) DeliverLinkPacket(tcpip.NetworkProtocolNumber, *stack.PacketBuffer) {}

func newTestEndpoint(t *testing.T, cfg netd.Config) (*Endpoint, *fakeTransport, *netd.Recorder) {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ft := newFakeTransport()
	rec := netd.NewRecorderWith(slog.New(slog.DiscardHandler), nil)
	return New(ft, &cfg, rec), ft, rec
}

func packets(sizes ...int) stack.PacketBufferList {
	var l stack.PacketBufferList
	for _, n := range sizes {
		l.PushBack(stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(make([]byte, n))}))
	}
	return l
}

// TestEndpointDelivers checks that a frame that passes the filter goes to
// the stack, and that a frame that fails it, and a truncated read, are
// dropped and counted (S07-02-packet-path, "Stack", "Link filter").
func TestEndpointDelivers(t *testing.T) {
	cfg := nt.Config()
	e, ft, rec := newTestEndpoint(t, cfg)
	d := &dispatcher{}
	e.Attach(d)
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()

	syn := nt.TCPSyn(40000, 443)
	ft.reads <- ip4(nt.Leased, public, tcpNum, syn)
	ft.reads <- ip4(otherHost, public, tcpNum, syn)
	// A datagram longer than the largest frame fills the buffer.
	long := append(ip4(nt.Leased, public, tcpNum, syn), make([]byte, cfg.MTU)...)
	ft.reads <- long
	close(ft.reads)
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatalf("Run = %v, want io.EOF", err)
	}
	e.Close()
	e.Wait()

	if d.delivered != 1 {
		t.Errorf("delivered %d frames, want 1", d.delivered)
	}
	if n := rec.Count(netd.RuleIPv4Source); n != 1 {
		t.Errorf("ipv4-source count %d, want 1", n)
	}
	if n := rec.Count(netd.RuleFrameTruncated); n != 1 {
		t.Errorf("frame-truncated count %d, want 1", n)
	}
}

// TestEndpointQueueFull checks the outbound queue's caps
// (SEC13-bounded-resources): a frame over the frame cap or the byte cap
// is dropped with link-queue-full, and the frames queued before it are
// sent at Close.
func TestEndpointQueueFull(t *testing.T) {
	big := packetlink.MaxFrame(packetlink.MaxMTU)
	tests := []struct {
		name          string
		frames, bytes int
		sizes         []int
		sent          int
	}{
		{"frame cap", 2, 16 << 20, []int{60, 60, 60}, 2},
		{"byte cap", 4096, big + 100, []int{big, 60, 101}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := nt.Config()
			cfg.Caps.QueueFrames, cfg.Caps.QueueBytes = tc.frames, tc.bytes
			e, ft, rec := newTestEndpoint(t, cfg)
			l := packets(tc.sizes...)
			if n, err := e.WritePackets(l); n != len(tc.sizes) || err != nil {
				t.Fatalf("WritePackets = %d, %v", n, err)
			}
			if got := rec.Count(netd.RuleLinkQueueFull); got != uint64(len(tc.sizes)-tc.sent) {
				t.Fatalf("link-queue-full count %d, want %d", got, len(tc.sizes)-tc.sent)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = e.Run(ctx)
			e.Close()
			e.Wait()
			if got := ft.writes(); got != tc.sent {
				t.Fatalf("sent %d frames, want %d", got, tc.sent)
			}
			if n, _ := e.WritePackets(packets(60)); n != 1 || ft.writes() != tc.sent {
				t.Fatalf("a frame after Close was sent")
			}
		})
	}
}

// TestEndpointWriteRetries checks that a write the peer's full buffer
// refuses is retried, at most writeRetries times, and then dropped with
// link-write-failed, and that another error drops the frame at once.
func TestEndpointWriteRetries(t *testing.T) {
	tests := []struct {
		name     string
		busy     int
		fail     error
		sent     int
		dropped  uint64
		attempts int
	}{
		{"busy, then sent", 3, nil, 1, 0, 4},
		{"busy past the retries", writeRetries + 1, nil, 0, 1, writeRetries + 1},
		{"another error", 0, errors.New("broken"), 0, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ft, rec := newTestEndpoint(t, nt.Config())
			ft.busy, ft.fail = tc.busy, tc.fail
			start := time.Now()
			e.write(make([]byte, 60))
			if ft.writes() != tc.sent || rec.Count(netd.RuleLinkWriteFailed) != tc.dropped {
				t.Fatalf("sent %d, dropped %d; want %d, %d", ft.writes(), rec.Count(netd.RuleLinkWriteFailed), tc.sent, tc.dropped)
			}
			if tc.busy > writeRetries && ft.busy != tc.busy-tc.attempts {
				t.Fatalf("%d attempts, want %d", tc.busy-ft.busy, tc.attempts)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("the retries took %v", d)
			}
		})
	}
}

// TestEndpointRunStops checks that Run returns once its context is done,
// and closes the transport.
func TestEndpointRunStops(t *testing.T) {
	e, ft, _ := newTestEndpoint(t, nt.Config())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	ft.mu.Lock()
	closed := ft.closed
	ft.mu.Unlock()
	if !closed {
		t.Fatal("the transport is open")
	}
	e.Close()
	e.Wait()
}

// TestDescribe checks the fields a drop is logged with.
func TestDescribe(t *testing.T) {
	frame := ip4(nt.Leased, public, tcpNum, nt.TCPSyn(40000, 443))
	got := map[string]string{}
	for _, a := range Describe(frame) {
		got[a.Key] = a.Value.String()
	}
	want := map[string]string{
		"bytes": fmt.Sprint(len(frame)), "src_mac": nt.GuestMAC.String(), "dst_mac": nt.GatewayMAC.String(),
		"ether_type": "2048", "src": nt.Leased.String(), "dst": netip.MustParseAddr("1.1.1.1").String(),
		"proto": "6", "src_port": "40000", "dst_port": "443",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Describe = %v, want %v", got, want)
	}
	if n := len(Describe(frame[:5])); n != 1 {
		t.Fatalf("Describe of 5 bytes has %d fields", n)
	}
}
