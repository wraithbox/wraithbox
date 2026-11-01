package stack

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

var (
	public    = netip.MustParseAddr("1.1.1.1")
	synthetic = netip.MustParseAddr("198.18.0.5")
	otherHost = netip.MustParseAddr("192.168.127.3")
)

const dialTimeout = 5 * time.Second

func isRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "refused")
}

// TestTCPRefused checks the TCP rules of S07-02-packet-path,
// "Connections" (SEC05-default-deny): TCP that no hook takes is reset at
// its first SYN, before the guest sends it again, with the rule logged.
func TestTCPRefused(t *testing.T) {
	tests := []struct {
		name string
		dest netip.AddrPort
		rule netd.Rule
	}{
		{"a gateway port", netip.AddrPortFrom(nt.Gateway, 80), netd.RuleGatewayPortRefused},
		{"the gateway's DNS port without a server", netip.AddrPortFrom(nt.Gateway, 53), netd.RuleGatewayPortRefused},
		{"a public address", netip.AddrPortFrom(public, 443), netd.RuleTCPDestinationRefused},
		{"another host on the subnet", netip.AddrPortFrom(otherHost, 22), netd.RuleTCPDestinationRefused},
		{"a synthetic address without a policy", netip.AddrPortFrom(synthetic, 443), netd.RuleSyntheticUnmapped},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuest(t)
			h := g.attach(t, nt.Config(), netd.Hooks{})
			if _, err := g.dial(tc.dest); !isRefused(err) {
				t.Fatalf("dial = %v, want refused", err)
			}
			if n := g.synCount(tc.dest); n != 1 {
				t.Errorf("the guest sent %d SYNs, want 1", n)
			}
			if n := h.s.Recorder().Count(tc.rule); n != 1 {
				t.Errorf("%s count %d, want 1", tc.rule, n)
			}
		})
	}
}

// policy is a stream policy that returns decide's answer.
type policy func(netd.Flow) netd.StreamDecision

func (p policy) DecideStream(f netd.Flow) netd.StreamDecision { return p(f) }

// sink passes every connection it takes to a channel.
type sink chan net.Conn

func (s sink) ServeStream(c net.Conn, _ netd.Flow)     { s <- c }
func (s sink) ServeDNSOverTCP(c net.Conn, _ netd.Flow) { s <- c }

func (s sink) take(t *testing.T) net.Conn {
	t.Helper()
	select {
	case c := <-s:
		return c
	case <-time.After(dialTimeout):
		t.Fatal("no connection")
		return nil
	}
}

// TestStreamPolicy checks that a synthetic address goes to the stream
// policy, and that the policy's refusal resets the SYN with its rule.
func TestStreamPolicy(t *testing.T) {
	conns := make(sink, 4)
	var mu sync.Mutex
	var flows []netd.Flow
	p := policy(func(f netd.Flow) netd.StreamDecision {
		mu.Lock()
		flows = append(flows, f)
		mu.Unlock()
		switch f.Dest.Port() {
		case 443:
			return netd.StreamDecision{Sink: conns}
		case 22:
			return netd.StreamDecision{Rule: "port-refused"}
		case 25:
			return netd.StreamDecision{Sink: conns, Rule: "port-refused"}
		default:
			return netd.StreamDecision{}
		}
	})
	g := newGuest(t)
	h := g.attach(t, nt.Config(), netd.Hooks{Stream: p})

	c, err := g.dial(netip.AddrPortFrom(synthetic, 443))
	if err != nil {
		t.Fatal(err)
	}
	server := conns.take(t)
	roundTrip(t, c, server)
	_ = c.Close()
	_ = server.Close()

	for port, rule := range map[uint16]netd.Rule{22: "port-refused", 25: "port-refused", 80: netd.RuleStreamRefused} {
		before := h.s.Recorder().Count(rule)
		if _, err := g.dial(netip.AddrPortFrom(synthetic, port)); !isRefused(err) {
			t.Fatalf("port %d: dial = %v, want refused", port, err)
		}
		if n := h.s.Recorder().Count(rule); n != before+1 {
			t.Fatalf("port %d: %s count %d, want %d", port, rule, n, before+1)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if flows[0].Guest.Addr() != nt.Leased || flows[0].Dest != netip.AddrPortFrom(synthetic, 443) {
		t.Fatalf("flow %+v", flows[0])
	}
}

// roundTrip checks that bytes pass both ways.
func roundTrip(t *testing.T, a, b net.Conn) {
	t.Helper()
	for _, pair := range [][2]net.Conn{{a, b}, {b, a}} {
		if _, err := pair[0].Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(pair[1], buf); err != nil || string(buf) != "ping" {
			t.Fatalf("read %q, %v", buf, err)
		}
	}
}

// TestDNSOverTCP checks that the gateway's port 53 goes to the
// DNS-over-TCP server when one is set.
func TestDNSOverTCP(t *testing.T) {
	conns := make(sink, 1)
	g := newGuest(t)
	g.attach(t, nt.Config(), netd.Hooks{DNSOverTCP: conns})
	c, err := g.dial(netip.AddrPortFrom(nt.Gateway, 53))
	if err != nil {
		t.Fatal(err)
	}
	server := conns.take(t)
	roundTrip(t, c, server)
	_ = server.Close()
	_ = c.Close()
	if _, err := g.dial(netip.AddrPortFrom(nt.Gateway, 54)); !isRefused(err) {
		t.Fatalf("port 54: dial = %v, want refused", err)
	}
}

// TestEstablishedCap checks Caps.EstablishedTCP (SEC13-bounded-resources):
// the SYN over the cap is reset with tcp-cap, and a closed connection
// frees its place.
func TestEstablishedCap(t *testing.T) {
	conns := make(sink, 4)
	cfg := nt.Config()
	cfg.Caps.EstablishedTCP = 1
	g := newGuest(t)
	h := g.attach(t, cfg, netd.Hooks{Stream: policy(func(netd.Flow) netd.StreamDecision {
		return netd.StreamDecision{Sink: conns}
	})})
	dest := netip.AddrPortFrom(synthetic, 443)

	first, err := g.dial(dest)
	if err != nil {
		t.Fatal(err)
	}
	server := conns.take(t)
	if _, err := g.dial(dest); !isRefused(err) {
		t.Fatalf("second dial = %v, want refused", err)
	}
	if n := h.s.Recorder().Count(netd.RuleTCPCap); n != 1 {
		t.Fatalf("tcp-cap count %d, want 1", n)
	}
	_ = server.Close()
	_ = first.Close()
	third, err := g.dial(dest)
	if err != nil {
		t.Fatalf("after a close: %v", err)
	}
	_ = third.Close()
	_ = conns.take(t).Close()
}

// TestHalfOpenCap checks Caps.HalfOpenTCP (SEC13-bounded-resources): a
// SYN while the cap's handshakes are pending is dropped with
// tcp-half-open-cap.
func TestHalfOpenCap(t *testing.T) {
	cfg := nt.Config()
	cfg.Caps.HalfOpenTCP = 1
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p := policy(func(netd.Flow) netd.StreamDecision {
		once.Do(func() {
			close(entered)
			<-release
		})
		return netd.StreamDecision{Rule: "port-refused"}
	})
	g := newGuest(t)
	h := g.attach(t, cfg, netd.Hooks{Stream: p})
	dest := netip.AddrPortFrom(synthetic, 443)

	results := make(chan error, 2)
	go func() { _, err := g.dial(dest); results <- err }()
	<-entered
	go func() { _, err := g.dial(dest); results <- err }()
	h.waitRule(netd.RuleTCPHalfOpenCap)
	close(release)
	for range 2 {
		if err := <-results; !isRefused(err) {
			t.Fatalf("dial = %v, want refused", err)
		}
	}
}

// TestPing checks that the stack answers an echo request to the gateway,
// and that one to another address gets no answer.
func TestPing(t *testing.T) {
	h := start(t, nt.Config(), netd.Hooks{}, nil)
	h.send(nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, nt.Gateway, uint8(header.ICMPv4ProtocolNumber), nt.ICMP(header.ICMPv4Echo, 0))))
	reply := h.receive()
	ip := header.IPv4(reply[header.EthernetMinimumSize:])
	if header.Ethernet(reply).DestinationAddress() != tcpipMAC(nt.GuestMAC) ||
		ip.TransportProtocol() != header.ICMPv4ProtocolNumber || header.ICMPv4(ip.Payload()).Type() != header.ICMPv4EchoReply {
		t.Fatalf("reply %x", reply)
	}
	h.send(nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, public, uint8(header.ICMPv4ProtocolNumber), nt.ICMP(header.ICMPv4Echo, 0))))
	h.waitRule(netd.RuleICMPNotEcho)
	h.quiet()
}

// TestARP checks that the stack answers ARP for the gateway only, and
// nothing that names the leased address as its target.
func TestARP(t *testing.T) {
	h := start(t, nt.Config(), netd.Hooks{}, nil)
	arpFrame := func(spa, tpa netip.Addr) []byte {
		return nt.Ethernet(nt.Broadcast, nt.GuestMAC, uint16(header.ARPProtocolNumber), nt.ARP(header.ARPRequest, nt.GuestMAC, spa, tpa))
	}
	h.send(arpFrame(nt.Leased, nt.Gateway))
	reply := header.ARP(h.receive()[header.EthernetMinimumSize:])
	if reply.Op() != header.ARPReply || netip.AddrFrom4([4]byte(reply.ProtocolAddressSender())) != nt.Gateway ||
		string(reply.HardwareAddressSender()) != string(nt.GatewayMAC) {
		t.Fatalf("reply %x", []byte(reply))
	}
	for _, f := range [][]byte{
		arpFrame(netip.IPv4Unspecified(), nt.Leased), // a probe
		arpFrame(nt.Leased, nt.Leased),               // gratuitous
		arpFrame(nt.Leased, otherHost),
		arpFrame(nt.Leased, public),
	} {
		h.send(f)
		h.waitRule(netd.RuleARPForm)
	}
	h.quiet()
}

// TestUDP checks that UDP to a port nothing is bound to is dropped
// without an ICMP port unreachable, and that the DNS-over-UDP server gets
// the gateway's port 53.
func TestUDP(t *testing.T) {
	dns := nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, nt.Gateway, uint8(header.UDPProtocolNumber), nt.UDP(5353, 53, []byte("q"))))
	t.Run("unbound", func(t *testing.T) {
		h := start(t, nt.Config(), netd.Hooks{}, nil)
		h.send(dns)
		h.waitRule(netd.RuleUDPUnbound)
		h.quiet()
	})
	t.Run("DNS over UDP", func(t *testing.T) {
		h := start(t, nt.Config(), netd.Hooks{DNSOverUDP: echoUDP{}}, nil)
		h.send(dns)
		reply := h.receive()
		ip := header.IPv4(reply[header.EthernetMinimumSize:])
		u := header.UDP(ip.Payload())
		if netip.AddrFrom4(ip.SourceAddress().As4()) != nt.Gateway || u.SourcePort() != 53 || u.DestinationPort() != 5353 || string(u.Payload()) != "q" {
			t.Fatalf("reply %x", reply)
		}
	})
}

// echoUDP answers every datagram with its own bytes.
type echoUDP struct{}

func (echoUDP) ServeDNSOverUDP(c net.PacketConn) {
	buf := make([]byte, 512)
	for {
		n, addr, err := c.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = c.WriteTo(buf[:n], addr)
	}
}

// TestRunCloses checks that Run closes the accepted connections when it
// stops.
func TestRunCloses(t *testing.T) {
	conns := make(sink, 1)
	g := newGuest(t)
	h := g.attach(t, nt.Config(), netd.Hooks{DNSOverTCP: conns})
	c, err := g.dial(netip.AddrPortFrom(nt.Gateway, 53))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	server := conns.take(t)
	h.stop()
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Fatal("the connection is open after Run")
	}
}

// TestNewRefuses checks that New refuses a configuration that isn't valid,
// and closes the transport.
func TestNewRefuses(t *testing.T) {
	cfg := nt.Config()
	cfg.Leased = cfg.Gateway
	m := &memTransport{in: make(chan []byte), out: func([]byte) {}, done: make(chan struct{})}
	if _, err := New(cfg, m, netd.Hooks{}); err == nil {
		t.Fatal("New took a bad configuration")
	}
	select {
	case <-m.done:
	default:
		t.Fatal("the transport is open")
	}
	if _, err := m.ReadFrame(nil); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame = %v", err)
	}
}
