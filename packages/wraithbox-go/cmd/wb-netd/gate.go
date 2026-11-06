package main

import (
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/link"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// gate is the packet transport from the moment wb-netd holds it. Until
// open it reads every frame and drops it unparsed with the rule
// link-not-started, so a guest that sends before the stack starts can't
// fill the packet socket and stall its NIC (S07-02-packet-path, "Packet
// transport"). After open the stack reads it: first the frame the drain
// had in flight, then the transport directly.
type gate struct {
	t   link.Transport
	rec *netd.Recorder

	opened  chan struct{}
	openMu  sync.Once
	drained chan struct{}

	// The drain's last read, set before drained is closed.
	frame []byte
	err   error

	first sync.Once
}

// newGate starts draining t, with frames of up to mtu bytes.
func newGate(t link.Transport, mtu int, rec *netd.Recorder) *gate {
	g := &gate{t: t, rec: rec, opened: make(chan struct{}), drained: make(chan struct{})}
	go g.drain(make([]byte, packetlink.ReadBufferSize(mtu)))
	return g
}

// drain reads and drops frames until open, or until a read fails. The
// read that returns after open is kept for the stack.
func (g *gate) drain(buf []byte) {
	defer close(g.drained)
	for {
		n, err := g.t.ReadFrame(buf)
		select {
		case <-g.opened:
			g.frame, g.err = buf[:n], err
			return
		default:
		}
		if err != nil {
			g.err = err
			return
		}
		g.rec.Record(netd.Drop, netd.RuleLinkNotStarted)
	}
}

// open ends the drain. The stack's reads start after it.
func (g *gate) open() {
	g.openMu.Do(func() { close(g.opened) })
}

// ReadFrame returns the drain's last read first, then reads t. It waits
// for the drain to end, so it is called only after open.
func (g *gate) ReadFrame(buf []byte) (int, error) {
	pending := false
	var n int
	var err error
	g.first.Do(func() {
		<-g.drained
		pending = true
		n, err = copy(buf, g.frame), g.err
	})
	if pending {
		return n, err
	}
	return g.t.ReadFrame(buf)
}

// WriteFrame writes to t.
func (g *gate) WriteFrame(frame []byte) error { return g.t.WriteFrame(frame) }

// Close closes t, which ends a drain in progress.
func (g *gate) Close() error { return g.t.Close() }
