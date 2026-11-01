// Package stack is wb-netd's userspace network stack for one VM: gVisor's
// stack on the link endpoint of internal/netd/link, with the TCP rules of
// S07-02-packet-path, "Connections", and the per-VM caps of "Per-VM caps"
// (SEC05-default-deny, SEC13-bounded-resources).
//
// The stack answers ARP and ping for the gateway's address only, which
// the link filter makes sure of. It decides each TCP connection at its
// first SYN. TCP to an address or port no hook serves is reset at once,
// on the read goroutine, before the guest can send the SYN again. TCP a
// hook may serve goes through gVisor's forwarder, under the half-open
// cap, and then to the hook, under the established cap.
package stack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	gvstack "gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/link"
)

const (
	nicID   tcpip.NICID = 1
	portDNS uint16      = 53
)

// How long gVisor keeps a connection after it stops making progress or
// is closed. Each bounds how long a connection holds its place under the
// established cap, and its buffers, after its hook has closed it
// (S07-02-packet-path, "Per-VM caps", SEC13-bounded-resources).
const (
	// userTimeout ends a connection whose sent data has gone unacknowledged
	// this long, zero-window probes included. Without it a guest that
	// keeps its window at zero holds a closed connection open forever.
	userTimeout = 30 * time.Second
	// lingerTimeout ends FIN-WAIT-2, gVisor's default being 60 s.
	lingerTimeout = 5 * time.Second
	// timeWaitTimeout ends TIME-WAIT, gVisor's default being 60 s.
	timeWaitTimeout = 5 * time.Second
	// abortEvery is how often stop aborts endpoints while it waits for
	// the accept goroutines.
	abortEvery = 10 * time.Millisecond
)

// Stack is the network stack of one VM's link.
type Stack struct {
	cfg   netd.Config
	hooks netd.Hooks
	rec   *netd.Recorder
	ep    *link.Endpoint
	gv    *gvstack.Stack
	fwd   *tcp.Forwarder
	dns   *gonet.UDPConn // the gateway's port 53, when DNSOverUDP is set

	mu        sync.Mutex
	active    int // connections a hook accepted that gVisor hasn't closed yet
	conns     map[*conn]struct{}
	stopped   bool
	accepting sync.WaitGroup // accept goroutines that started before stop
}

// New returns the stack of a link over t. It checks cfg, and closes t
// when cfg isn't valid. The stack owns t, and closes it when Run
// returns.
func New(cfg netd.Config, t link.Transport, hooks netd.Hooks) (*Stack, error) {
	if err := cfg.Validate(); err != nil {
		_ = t.Close()
		return nil, err
	}
	rec := netd.NewRecorder(hooks.Log, hooks.Events)
	s := &Stack{
		cfg:   cfg,
		hooks: hooks,
		rec:   rec,
		ep:    link.New(t, &cfg, rec),
		conns: map[*conn]struct{}{},
	}
	s.gv = gvstack.New(gvstack.Options{
		NetworkProtocols:   []gvstack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []gvstack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.setup(); err != nil {
		s.gv.Close()
		s.gv.Wait()
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// setup adds the NIC, its address, route and neighbor, sets the TCP
// buffer caps and timeouts, and installs the TCP and UDP rules.
func (s *Stack) setup() error {
	if err := s.setupNIC(); err != nil {
		return err
	}
	buf := s.cfg.Caps.TCPBuffer
	linger, timeWait := tcpip.TCPLingerTimeoutOption(lingerTimeout), tcpip.TCPTimeWaitTimeoutOption(timeWaitTimeout)
	// SEC13-bounded-resources: every TCP connection's send and receive
	// buffer is at most Caps.TCPBuffer bytes, and a closed connection
	// ends within the timeouts.
	for _, opt := range []tcpip.SettableTransportProtocolOption{
		&tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: buf, Max: buf},
		&tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: buf, Max: buf},
		&linger,
		&timeWait,
	} {
		if err := s.gv.SetTransportProtocolOption(tcp.ProtocolNumber, opt); err != nil {
			return fmt.Errorf("netd: set the TCP option %T: %s", opt, err)
		}
	}
	// SEC13-bounded-resources: at most Caps.HalfOpenTCP handshakes at once.
	s.fwd = tcp.NewForwarder(s.gv, buf, s.cfg.Caps.HalfOpenTCP, s.accept)
	s.gv.SetTransportProtocolHandler(tcp.ProtocolNumber, s.handleTCP)
	s.gv.SetTransportProtocolHandler(udp.ProtocolNumber, s.handleUDP)
	if s.hooks.DNSOverUDP == nil {
		return nil
	}
	dns, err := gonet.DialUDP(s.gv, &tcpip.FullAddress{NIC: nicID, Addr: addr(s.cfg.Gateway), Port: portDNS}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("netd: bind the gateway's UDP port 53: %w", err)
	}
	s.dns = dns
	return nil
}

// setupNIC adds the one NIC, with the gateway's address. It is
// promiscuous, and may send from any address, so that the stack sees TCP
// to every address and can reset it, and can answer from a synthetic
// address. The link filter keeps everything but TCP away from addresses
// other than the gateway's. The guest's MAC is a static neighbor, so the
// stack never sends ARP.
func (s *Stack) setupNIC() error {
	gw := tcpip.AddressWithPrefix{Address: addr(s.cfg.Gateway), PrefixLen: s.cfg.Subnet.Bits()}
	steps := []struct {
		what string
		do   func() tcpip.Error
	}{
		{"create the NIC", func() tcpip.Error { return s.gv.CreateNIC(nicID, ethernet.New(s.ep)) }},
		{"add the gateway's address", func() tcpip.Error {
			return s.gv.AddProtocolAddress(nicID, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: gw}, gvstack.AddressProperties{})
		}},
		{"set promiscuous mode", func() tcpip.Error { return s.gv.SetPromiscuousMode(nicID, true) }},
		{"allow spoofing", func() tcpip.Error { return s.gv.SetSpoofing(nicID, true) }},
		{"add the guest as a neighbor", func() tcpip.Error {
			return s.gv.AddStaticNeighbor(nicID, ipv4.ProtocolNumber, addr(s.cfg.Leased), tcpip.LinkAddress(s.cfg.GuestMAC))
		}},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			return fmt.Errorf("netd: %s: %s", step.what, err)
		}
	}
	// Only the subnet is routed: the stack sends to the guest, never past it.
	s.gv.SetRouteTable([]tcpip.Route{{Destination: gw.Subnet(), NIC: nicID}})
	return nil
}

// Run moves frames until the transport fails or ctx is done. Then it
// closes every connection, the DNS socket and the stack, and returns the
// transport's error or ctx's. Run is called once.
func (s *Stack) Run(ctx context.Context) error {
	if s.dns != nil {
		go s.hooks.DNSOverUDP.ServeDNSOverUDP(s.dns)
	}
	err := s.ep.Run(ctx)
	s.stop()
	return err
}

// stop closes what Run leaves open, and waits for the accept goroutines.
// Closing the link endpoint stops its writer once the frames still queued
// are sent. The stack's Wait waits for the writer through the endpoint's
// Wait, which closes the transport.
func (s *Stack) stop() {
	s.mu.Lock()
	s.stopped = true
	open := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		open = append(open, c)
	}
	s.mu.Unlock()
	for _, c := range open {
		_ = c.Close()
	}
	if s.dns != nil {
		_ = s.dns.Close()
	}
	s.gv.Close()
	s.ep.Close()
	s.waitAccepts()
	s.gv.Wait()
}

// waitAccepts waits for the accept goroutines. One may be in a handshake,
// which waits for an ACK the stopped link never delivers, and one may
// have started its handshake after gv.Close. So while it waits it aborts
// every endpoint the stack holds, which ends their handshakes.
func (s *Stack) waitAccepts() {
	done := make(chan struct{})
	go func() {
		s.accepting.Wait()
		close(done)
	}()
	tick := time.NewTicker(abortEvery)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			for _, e := range s.gv.RegisteredEndpoints() {
				e.Abort()
			}
		}
	}
}

// Recorder returns the recorder every drop and reset is counted with.
func (s *Stack) Recorder() *netd.Recorder { return s.rec }

// handleTCP is the TCP rules of S07-02-packet-path, "Connections", for a
// segment no connection takes (SEC05-default-deny). TCP to an address
// and port no hook serves gets false: the stack resets it at once, on
// this goroutine. The rest goes to the forwarder, which takes a SYN under
// the half-open cap and drops it over the cap.
func (s *Stack) handleTCP(id gvstack.TransportEndpointID, pkt *gvstack.PacketBuffer) bool {
	f := flowOf(id)
	if rule := s.route(f.Dest); rule != "" {
		s.rec.Record(decisionFor(pkt), rule, f.LogAttrs()...)
		return false
	}
	drops := s.gv.Stats().TCP.ForwardMaxInFlightDrop
	before := drops.Value()
	handled := s.fwd.HandlePacket(id, pkt)
	if drops.Value() != before {
		// SEC13-bounded-resources: a SYN over Caps.HalfOpenTCP.
		s.rec.Record(netd.Drop, netd.RuleTCPHalfOpenCap, f.LogAttrs()...)
	}
	return handled
}

// route returns the rule that refuses TCP to dest, or "" when a hook may
// take it: the DNS-over-TCP server on the gateway's port 53, and the
// stream policy on a synthetic address.
func (s *Stack) route(dest netip.AddrPort) netd.Rule {
	a := dest.Addr()
	switch {
	case a == s.cfg.Gateway && dest.Port() == portDNS && s.hooks.DNSOverTCP != nil:
		return ""
	case a == s.cfg.Gateway:
		return netd.RuleGatewayPortRefused
	case s.cfg.Synthetic.Contains(a) && s.hooks.Stream != nil:
		return ""
	case s.cfg.Synthetic.Contains(a):
		return netd.RuleSyntheticUnmapped
	default:
		return netd.RuleTCPDestinationRefused
	}
}

// decisionFor is Reset for a segment the stack answers with a reset,
// and Drop for a reset from the guest, which gets no answer.
func decisionFor(pkt *gvstack.PacketBuffer) netd.Decision {
	h := header.TCP(pkt.TransportHeader().Slice())
	if len(h) >= header.TCPMinimumSize && h.Flags().Contains(header.TCPFlagRst) {
		return netd.Drop
	}
	return netd.Reset
}

// accept decides one SYN the forwarder took, on its own goroutine. The
// established cap is checked first, then the hook decides. A refused SYN
// is reset. An accepted one completes its handshake and goes to the hook.
// After stop has begun, every SYN is reset.
func (s *Stack) accept(req *tcp.ForwarderRequest) {
	if !s.enter() {
		req.Complete(true)
		return
	}
	defer s.accepting.Done()
	f := flowOf(req.ID())
	if !s.reserve() {
		// SEC13-bounded-resources: a SYN over Caps.EstablishedTCP.
		s.refuse(req, netd.RuleTCPCap, f)
		return
	}
	serve, rule := s.decide(f)
	if serve == nil {
		s.release()
		s.refuse(req, rule, f)
		return
	}
	var wq waiter.Queue
	ep, err := req.CreateEndpoint(&wq)
	if err != nil {
		s.release()
		req.Complete(true)
		s.rec.Record(netd.Reset, netd.RuleTCPHandshakeFailed, append(f.LogAttrs(), slog.String("error", err.String()))...)
		return
	}
	req.Complete(false)
	s.releaseWhenClosed(ep, &wq)
	uto := tcpip.TCPUserTimeoutOption(userTimeout)
	if err := ep.SetSockOpt(&uto); err != nil {
		// Without the timeout the connection's place isn't bounded.
		ep.Abort()
		s.rec.Record(netd.Reset, netd.RuleTCPHandshakeFailed, append(f.LogAttrs(), slog.String("error", err.String()))...)
		return
	}
	c := s.track(gonet.NewTCPConn(&wq, ep))
	if c == nil {
		return
	}
	go serve(c, f)
}

func (s *Stack) refuse(req *tcp.ForwarderRequest, rule netd.Rule, f netd.Flow) {
	req.Complete(true)
	s.rec.Record(netd.Reset, rule, f.LogAttrs()...)
}

// decide asks the hook for a flow route let through. It returns the
// function that serves the connection, or nil and the rule that refuses
// it. A stream decision is an accept only with a sink and no rule.
func (s *Stack) decide(f netd.Flow) (func(net.Conn, netd.Flow), netd.Rule) {
	if f.Dest.Addr() == s.cfg.Gateway {
		return s.hooks.DNSOverTCP.ServeDNSOverTCP, ""
	}
	d := s.hooks.Stream.DecideStream(f)
	switch {
	case d.Sink != nil && d.Rule == "":
		return d.Sink.ServeStream, ""
	case d.Rule == "":
		return nil, netd.RuleStreamRefused
	default:
		return nil, d.Rule
	}
}

// handleUDP drops UDP no socket takes, without the ICMP port unreachable
// the stack would send. The filter passed it: DNS before the DNS server
// binds, or DHCP before the DHCP server does.
func (s *Stack) handleUDP(id gvstack.TransportEndpointID, _ *gvstack.PacketBuffer) bool {
	s.rec.Record(netd.Drop, netd.RuleUDPUnbound, flowOf(id).LogAttrs()...)
	return true
}

// enter counts an accept goroutine that stop waits for, or reports that
// stop has begun.
func (s *Stack) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.accepting.Add(1)
	return true
}

// releaseWhenClosed frees a connection's place under the established cap
// once gVisor's endpoint is closed. The hook's Close isn't the end: a
// graceful close keeps the endpoint, and its buffers, until the FIN
// exchange ends or a timeout does (SEC13-bounded-resources). gVisor
// signals EventHUp on the endpoint's queue when it closes. The entry is
// never unregistered, because Notify holds the queue's lock while it
// calls it, and the queue goes with the endpoint.
func (s *Stack) releaseWhenClosed(ep tcpip.Endpoint, wq *waiter.Queue) {
	var once sync.Once
	check := func(waiter.EventMask) {
		if st := ep.State(); st == uint32(tcp.StateClose) || st == uint32(tcp.StateError) {
			once.Do(s.release)
		}
	}
	entry := waiter.NewFunctionEntry(waiter.EventHUp, check)
	wq.EventRegister(&entry)
	check(0) // closed before the entry was registered
}

// reserve takes a place under the established cap, or reports that
// there is none.
func (s *Stack) reserve() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= s.cfg.Caps.EstablishedTCP {
		return false
	}
	s.active++
	return true
}

func (s *Stack) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
}

// track wraps an accepted connection, so stop can close it. After Run
// has stopped it closes the connection and returns nil.
func (s *Stack) track(tc net.Conn) *conn {
	c := &conn{Conn: tc, s: s}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		_ = c.Close()
		return nil
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	return c
}

// conn is an accepted connection. Its first Close takes it off the list
// stop closes. Its place under the established cap is freed later, when
// gVisor closes the endpoint (releaseWhenClosed).
type conn struct {
	net.Conn
	s    *Stack
	once sync.Once
}

// Close closes the connection.
func (c *conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.s.mu.Lock()
		defer c.s.mu.Unlock()
		delete(c.s.conns, c)
	})
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// flowOf is the flow of a segment from the guest: the stack's side of
// the endpoint ID is the destination.
func flowOf(id gvstack.TransportEndpointID) netd.Flow {
	return netd.Flow{
		Guest: netip.AddrPortFrom(netip.AddrFrom4(id.RemoteAddress.As4()), id.RemotePort),
		Dest:  netip.AddrPortFrom(netip.AddrFrom4(id.LocalAddress.As4()), id.LocalPort),
	}
}

func addr(a netip.Addr) tcpip.Address { return tcpip.AddrFrom4(a.As4()) }
