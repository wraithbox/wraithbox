// Package link is wb-netd's link endpoint: it moves Ethernet frames
// between the packet transport and gVisor's stack (S07-02-packet-path,
// "Stack"), and checks every frame from the guest with the link filter
// before the stack sees it ("Link filter").
//
// gVisor's file-descriptor link endpoint is Linux-only, and its channel
// endpoint queues inbound frames without a bound in bytes. This endpoint
// has no inbound queue: a frame is read, checked and delivered to the
// stack on the read goroutine. Outbound frames go through one queue
// bounded in frames and in bytes ("Per-VM caps", "MTU").
//
// The endpoint handles raw Ethernet frames. gVisor's link/ethernet
// wraps it, adding and stripping the Ethernet header for the stack.
package link

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// Transport carries one Ethernet frame per call. packetlink.Transport
// implements it. A WriteFrame that fails because the peer's buffer is
// full returns an error that wraps packetlink.ErrBusy.
type Transport interface {
	ReadFrame(buf []byte) (int, error)
	WriteFrame(frame []byte) error
	Close() error
}

// Retries of a write the peer's full buffer refused (S07-02-packet-path,
// "Per-VM caps"). X03-network-path saw ENOBUFS under load and retried
// without a bound. Here each frame gets at most writeRetries more
// attempts, about 20 ms in all, and is then dropped with
// link-write-failed.
const (
	writeRetries    = 12
	firstRetryDelay = 20 * time.Microsecond
	maxRetryDelay   = 4 * time.Millisecond
)

// Endpoint is the link endpoint of one VM's NIC. It implements gVisor's
// stack.LinkEndpoint for raw Ethernet frames.
type Endpoint struct {
	t      Transport
	filter *Filter
	rec    *netd.Recorder
	mtu    int
	mac    tcpip.LinkAddress

	maxFrames, maxBytes int

	mu         sync.RWMutex
	dispatcher stack.NetworkDispatcher

	qmu    sync.Mutex
	queue  [][]byte
	queued int // bytes in queue
	closed bool
	wake   chan struct{}

	writer    sync.WaitGroup
	startOnce sync.Once
	closeT    sync.Once
}

var _ stack.LinkEndpoint = (*Endpoint)(nil)

// New returns the endpoint of a link over t. cfg is valid
// (Config.Validate). The endpoint owns t, and closes it when it stops.
func New(t Transport, cfg *netd.Config, rec *netd.Recorder) *Endpoint {
	return &Endpoint{
		t:         t,
		filter:    NewFilter(cfg),
		rec:       rec,
		mtu:       cfg.MTU,
		mac:       tcpip.LinkAddress(cfg.GatewayMAC),
		maxFrames: cfg.Caps.QueueFrames,
		maxBytes:  cfg.Caps.QueueBytes,
		wake:      make(chan struct{}, 1),
	}
}

// Run reads frames from the transport and delivers each one that passes
// the filter to the stack, until a read fails or ctx is done. It starts
// the writer, which sends queued frames until Close. When ctx is done it
// closes the transport, which ends the read. It returns the read's
// error, or ctx's.
func (e *Endpoint) Run(ctx context.Context) error {
	e.startOnce.Do(func() { e.writer.Go(e.writeLoop) })
	stop := context.AfterFunc(ctx, e.closeTransport)
	defer stop()
	buf := make([]byte, packetlink.ReadBufferSize(e.mtu))
	for {
		n, err := e.t.ReadFrame(buf)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		e.deliver(buf[:n], n == len(buf))
	}
}

// deliver checks one frame and passes it to the stack. A read that
// filled the buffer is dropped with frame-truncated, never passed on in
// part (S07-02-packet-path, "Stack"). The stack copies the frame.
func (e *Endpoint) deliver(frame []byte, truncated bool) {
	if truncated {
		e.rec.Record(netd.Drop, netd.RuleFrameTruncated, Describe(frame)...)
		return
	}
	if rule, ok := e.filter.Check(frame); !ok {
		e.rec.Record(netd.Drop, rule, Describe(frame)...)
		return
	}
	e.mu.RLock()
	d := e.dispatcher
	e.mu.RUnlock()
	if d == nil {
		return
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(frame)})
	d.DeliverNetworkPacket(0, pkt)
	pkt.DecRef()
}

// WritePackets queues the stack's frames for the writer. A frame over
// the queue's frame or byte cap is dropped with link-queue-full
// (SEC13-bounded-resources). Frames after Close are dropped. Each frame
// counts as written: the stack's own retransmissions cover a drop.
func (e *Endpoint) WritePackets(packets stack.PacketBufferList) (int, tcpip.Error) {
	for _, pkt := range packets.AsSlice() {
		e.enqueue(pkt)
	}
	return packets.Len(), nil
}

func (e *Endpoint) enqueue(pkt *stack.PacketBuffer) {
	size := pkt.Size()
	frame := make([]byte, 0, size)
	for _, s := range pkt.AsSlices() {
		frame = append(frame, s...)
	}
	e.qmu.Lock()
	if e.closed {
		e.qmu.Unlock()
		return
	}
	if len(e.queue) >= e.maxFrames || e.queued+len(frame) > e.maxBytes {
		frames, queued := len(e.queue), e.queued
		e.qmu.Unlock()
		e.rec.Record(netd.Drop, netd.RuleLinkQueueFull,
			slog.Int("bytes", len(frame)), slog.Int("queued_frames", frames), slog.Int("queued_bytes", queued))
		return
	}
	e.queue = append(e.queue, frame)
	e.queued += len(frame)
	e.qmu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// writeLoop sends queued frames until Close, and then the ones still
// queued.
func (e *Endpoint) writeLoop() {
	for {
		frame, ok := e.next()
		if !ok {
			return
		}
		e.write(frame)
	}
}

// next takes the oldest queued frame, waiting for one. It returns false
// once the endpoint is closed and the queue is empty.
func (e *Endpoint) next() ([]byte, bool) {
	for {
		e.qmu.Lock()
		if len(e.queue) > 0 {
			frame := e.queue[0]
			e.queue[0] = nil
			e.queue = e.queue[1:]
			e.queued -= len(frame)
			e.qmu.Unlock()
			return frame, true
		}
		closed := e.closed
		e.qmu.Unlock()
		if closed {
			return nil, false
		}
		<-e.wake
	}
}

// write sends one frame. A send the peer's full buffer refused is tried
// again after a short, growing delay, writeRetries times at most. A
// frame that still isn't sent is dropped with link-write-failed.
func (e *Endpoint) write(frame []byte) {
	delay := firstRetryDelay
	for attempt := 0; ; attempt++ {
		err := e.t.WriteFrame(frame)
		if err == nil {
			return
		}
		if !errors.Is(err, packetlink.ErrBusy) || attempt == writeRetries {
			e.rec.Record(netd.Drop, netd.RuleLinkWriteFailed,
				slog.Int("bytes", len(frame)), slog.Int("attempts", attempt+1), slog.String("error", err.Error()))
			return
		}
		time.Sleep(delay)
		delay = min(2*delay, maxRetryDelay)
	}
}

func (e *Endpoint) closeTransport() {
	e.closeT.Do(func() { _ = e.t.Close() })
}

// Close stops the endpoint: the writer sends the frames still queued and
// stops, and later frames are dropped. gVisor calls it when the NIC is
// removed. Wait waits for the writer and closes the transport.
func (e *Endpoint) Close() {
	e.qmu.Lock()
	e.closed = true
	e.qmu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Wait waits for the writer to stop after Close, and closes the
// transport.
func (e *Endpoint) Wait() {
	e.writer.Wait()
	e.closeTransport()
}

// MTU is the largest frame: the link MTU plus the Ethernet header, which
// link/ethernet takes off again. Config.Validate kept the MTU at most
// 65535, so the bound below never cuts.
func (e *Endpoint) MTU() uint32 {
	frame := packetlink.MaxFrame(e.mtu)
	if frame < 0 || frame > math.MaxUint32 {
		return 0
	}
	return uint32(frame)
}

// SetMTU does nothing: the MTU is the link's, fixed at start.
func (*Endpoint) SetMTU(uint32) {}

// MaxHeaderLength is 0: link/ethernet adds the Ethernet header.
func (*Endpoint) MaxHeaderLength() uint16 { return 0 }

// LinkAddress is the gateway's MAC.
func (e *Endpoint) LinkAddress() tcpip.LinkAddress { return e.mac }

// SetLinkAddress does nothing: the gateway's MAC is fixed at start.
func (*Endpoint) SetLinkAddress(tcpip.LinkAddress) {}

// Capabilities are none. link/ethernet adds address resolution.
func (*Endpoint) Capabilities() stack.LinkEndpointCapabilities { return 0 }

// Attach sets the dispatcher frames are delivered to.
func (e *Endpoint) Attach(d stack.NetworkDispatcher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dispatcher = d
}

// IsAttached reports whether a dispatcher is set.
func (e *Endpoint) IsAttached() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dispatcher != nil
}

// ARPHardwareType is none: link/ethernet sets Ethernet.
func (*Endpoint) ARPHardwareType() header.ARPHardwareType { return header.ARPHardwareNone }

// AddHeader does nothing: link/ethernet adds the header.
func (*Endpoint) AddHeader(*stack.PacketBuffer) {}

// ParseHeader does nothing: link/ethernet parses the header.
func (*Endpoint) ParseHeader(*stack.PacketBuffer) bool { return true }

// SetOnCloseAction does nothing: the stack owner stops the endpoint.
func (*Endpoint) SetOnCloseAction(func()) {}
