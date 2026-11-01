package stack

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	gvstack "gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// memTransport is wb-netd's side of an in-memory link. It reads the
// frames sent on in, and io.EOF once in is closed, and passes every frame
// it writes to out.
type memTransport struct {
	in   chan []byte
	out  func([]byte)
	once sync.Once
	done chan struct{}
}

func (m *memTransport) ReadFrame(buf []byte) (int, error) {
	select {
	case f, ok := <-m.in:
		if !ok {
			return 0, io.EOF
		}
		return copy(buf, f), nil
	case <-m.done:
		return 0, io.EOF
	}
}

func (m *memTransport) WriteFrame(frame []byte) error {
	m.out(append([]byte(nil), frame...))
	return nil
}

func (m *memTransport) Close() error {
	m.once.Do(func() { close(m.done) })
	return nil
}

// events passes the rule of every drop and reset to a channel.
type events chan netd.Rule

func (e events) Event(_ netd.Decision, rule netd.Rule, _ uint64, _ []slog.Attr) {
	select {
	case e <- rule:
	default:
	}
}

// harness runs a stack on an in-memory link.
type harness struct {
	t      *testing.T
	s      *Stack
	in     chan []byte
	out    chan []byte
	events events
	cancel context.CancelFunc
	done   chan error
}

// start runs a stack of cfg with hooks. Frames it sends go to out, or to
// h.out when out is nil.
func start(t *testing.T, cfg netd.Config, hooks netd.Hooks, out func([]byte)) *harness {
	t.Helper()
	h := &harness{t: t, in: make(chan []byte), out: make(chan []byte, 4096), events: make(events, 4096), done: make(chan error, 1)}
	if out == nil {
		out = func(f []byte) { h.out <- f }
	}
	hooks.Events = h.events
	hooks.Log = slog.New(slog.DiscardHandler)
	s, err := New(cfg, &memTransport{in: h.in, out: out, done: make(chan struct{})}, hooks)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- s.Run(ctx) }()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
	h.done <- nil
}

// send passes a frame from the guest to the stack.
func (h *harness) send(frame []byte) {
	h.t.Helper()
	select {
	case h.in <- frame:
	case <-time.After(5 * time.Second):
		h.t.Fatal("the stack doesn't read")
	}
}

// waitRule waits for a drop or reset with a rule.
func (h *harness) waitRule(rule netd.Rule) {
	h.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case r := <-h.events:
			if r == rule {
				return
			}
		case <-timeout:
			h.t.Fatalf("no %s", rule)
		}
	}
}

// receive waits for the next frame the stack sends.
func (h *harness) receive() []byte {
	h.t.Helper()
	select {
	case f := <-h.out:
		return f
	case <-time.After(5 * time.Second):
		h.t.Fatal("the stack sent nothing")
		return nil
	}
}

// quiet checks that the stack has sent nothing.
func (h *harness) quiet() {
	h.t.Helper()
	select {
	case f := <-h.out:
		h.t.Fatalf("the stack sent %x", f)
	default:
	}
}

// guest is a second gVisor stack in the guest's place: the leased
// address, the guest's MAC, and the gateway as its route.
type guest struct {
	s  *gvstack.Stack
	ch *channel.Endpoint

	mu   sync.Mutex
	sent map[netip.AddrPort]int
}

func newGuest(t *testing.T) *guest {
	t.Helper()
	g := &guest{
		s: gvstack.New(gvstack.Options{
			NetworkProtocols:   []gvstack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
			TransportProtocols: []gvstack.TransportProtocolFactory{tcp.NewProtocol},
		}),
		ch:   channel.New(1024, uint32(packetlink.MaxFrame(netd.DefaultMTU)), tcpip.LinkAddress(nt.GuestMAC)),
		sent: map[netip.AddrPort]int{},
	}
	if err := g.s.CreateNIC(1, ethernet.New(g.ch)); err != nil {
		t.Fatal(err)
	}
	pa := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4(nt.Leased.As4()), PrefixLen: nt.Subnet.Bits()},
	}
	if err := g.s.AddProtocolAddress(1, pa, gvstack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	g.s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, Gateway: tcpip.AddrFrom4(nt.Gateway.As4()), NIC: 1}})
	t.Cleanup(func() {
		g.s.Close()
		g.ch.Close()
		g.s.Wait()
	})
	return g
}

// smallWindow caps the guest's TCP receive buffer at 4 KiB, so a guest
// that doesn't read soon advertises a window of zero.
func (g *guest) smallWindow(t *testing.T) {
	t.Helper()
	opt := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: 4096, Max: 4096}
	if err := g.s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
		t.Fatal(err)
	}
}

// reset aborts every connection the guest has open, which sends a RST
// on each one that is connected.
func (g *guest) reset() {
	for _, e := range g.s.RegisteredEndpoints() {
		e.Abort()
	}
}

// attach starts a stack of cfg with hooks, linked to the guest.
func (g *guest) attach(t *testing.T, cfg netd.Config, hooks netd.Hooks) *harness {
	t.Helper()
	h := start(t, cfg, hooks, g.inject)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.pump(ctx, h.in)
	return h
}

// inject passes a frame from wb-netd to the guest.
func (g *guest) inject(frame []byte) {
	pkt := gvstack.NewPacketBuffer(gvstack.PacketBufferOptions{Payload: buffer.MakeWithData(frame)})
	g.ch.InjectInbound(0, pkt)
	pkt.DecRef()
}

// pump passes the guest's frames to wb-netd, and counts the SYNs it sends
// to each destination.
func (g *guest) pump(ctx context.Context, in chan<- []byte) {
	for {
		pkt := g.ch.ReadContext(ctx)
		if pkt == nil {
			return
		}
		frame := pkt.ToView().AsSlice()
		pkt.DecRef()
		g.countSYN(frame)
		select {
		case in <- frame:
		case <-ctx.Done():
			return
		}
	}
}

func (g *guest) countSYN(frame []byte) {
	if len(frame) < header.EthernetMinimumSize+header.IPv4MinimumSize+header.TCPMinimumSize ||
		header.Ethernet(frame).Type() != header.IPv4ProtocolNumber {
		return
	}
	ip := header.IPv4(frame[header.EthernetMinimumSize:])
	if ip.TransportProtocol() != header.TCPProtocolNumber {
		return
	}
	h := header.TCP(ip.Payload())
	if h.Flags() != header.TCPFlagSyn {
		return
	}
	dest := netip.AddrPortFrom(netip.AddrFrom4(ip.DestinationAddress().As4()), h.DestinationPort())
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sent[dest]++
}

func (g *guest) synCount(dest netip.AddrPort) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sent[dest]
}

func tcpipMAC(m net.HardwareAddr) tcpip.LinkAddress { return tcpip.LinkAddress(m) }

// dial opens a TCP connection from the guest.
func (g *guest) dial(dest netip.AddrPort) (*gonet.TCPConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	return gonet.DialContextTCP(ctx, g.s, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(dest.Addr().As4()), Port: dest.Port()}, ipv4.ProtocolNumber)
}
