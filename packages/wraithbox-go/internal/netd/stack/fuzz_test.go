package stack

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// FuzzLinkEndpoint is the link endpoint's fuzz target
// (C01-network-and-dns, "Fuzzing"). It feeds frames through the link
// endpoint into the stack with the TCP rules and no hooks, and checks
// what the stack sends back: frames only to the guest's MAC or
// broadcast, ARP replies only for the gateway, and no SYN-ACK, since no
// hook accepts a connection.
func FuzzLinkEndpoint(f *testing.F) {
	ip4 := uint16(header.IPv4ProtocolNumber)
	arpType := uint16(header.ARPProtocolNumber)
	tcpNum, udpNum, icmpNum := uint8(header.TCPProtocolNumber), uint8(header.UDPProtocolNumber), uint8(header.ICMPv4ProtocolNumber)
	arpReq := nt.Ethernet(nt.Broadcast, nt.GuestMAC, arpType, nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, nt.Gateway))
	syn := func(dst netip.Addr, port uint16) []byte {
		return nt.FromGuest(ip4, nt.IPv4(nt.Leased, dst, tcpNum, nt.TCPSegment(nt.Leased, dst, 40000, port, header.TCPFlagSyn)))
	}
	seeds := [][3][]byte{
		{arpReq, syn(nt.Gateway, 80), syn(public, 443)},
		{syn(nt.Leased, 443), syn(netip.MustParseAddr("127.0.0.1"), 80), syn(netip.MustParseAddr("0.0.0.1"), 80)},
		{syn(synthetic, 443), syn(nt.Gateway, 53), nt.FromGuest(ip4, nt.IPv4(nt.Leased, nt.Gateway, tcpNum, nt.TCPSegment(nt.Leased, nt.Gateway, 40000, 80, header.TCPFlagAck)))},
		{nt.FromGuest(ip4, nt.IPv4(nt.Leased, nt.Gateway, icmpNum, nt.ICMP(header.ICMPv4Echo, 0))), nt.FromGuest(ip4, nt.IPv4(nt.Leased, nt.Gateway, udpNum, nt.UDP(5353, 53, []byte("q")))), nil},
		{nt.Ethernet(nt.Broadcast, nt.GuestMAC, ip4, nt.IPv4(netip.IPv4Unspecified(), netip.MustParseAddr("255.255.255.255"), udpNum, nt.UDP(68, 67, make([]byte, 300)))), arpReq, make([]byte, 65550)},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, a, b, c []byte) {
		for _, frame := range runFrames(t, [][]byte{a, b, c}) {
			if err := checkSent(frame); err != "" {
				t.Fatalf("%s: %x", err, frame)
			}
		}
	})
}

// runFrames runs a stack with no hooks over the frames, until it has
// read them all and stopped, and returns the frames it sent.
func runFrames(t *testing.T, frames [][]byte) [][]byte {
	t.Helper()
	var mu sync.Mutex
	var sent [][]byte
	in := make(chan []byte, len(frames))
	for _, f := range frames {
		in <- f
	}
	close(in)
	m := &memTransport{in: in, out: func(f []byte) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, f)
	}, done: make(chan struct{})}
	s, err := New(nt.Config(), m, netd.Hooks{Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Run(context.Background())
	mu.Lock()
	defer mu.Unlock()
	return sent
}

// checkSent returns what is wrong with a frame the stack sent, or "".
func checkSent(frame []byte) string {
	if len(frame) < header.EthernetMinimumSize {
		return "a short frame"
	}
	eth := header.Ethernet(frame)
	if eth.SourceAddress() != tcpip.LinkAddress(nt.GatewayMAC) {
		return "a frame from another MAC"
	}
	if dst := eth.DestinationAddress(); dst != tcpip.LinkAddress(nt.GuestMAC) && dst != header.EthernetBroadcastAddress {
		return "a frame to another MAC"
	}
	p := frame[header.EthernetMinimumSize:]
	switch eth.Type() {
	case header.ARPProtocolNumber:
		a := header.ARP(p)
		if a.IsValid() && a.Op() == header.ARPReply && netip.AddrFrom4([4]byte(a.ProtocolAddressSender())) != nt.Gateway {
			return "an ARP reply for an address but the gateway"
		}
	case header.IPv4ProtocolNumber:
		ip := header.IPv4(p)
		if !ip.IsValid(len(p)) || ip.TransportProtocol() != header.TCPProtocolNumber {
			return ""
		}
		h := header.TCP(ip.Payload())
		if len(h) >= header.TCPMinimumSize && h.Flags().Contains(header.TCPFlagSyn|header.TCPFlagAck) {
			return "a SYN-ACK"
		}
	}
	return ""
}

// TestRunFrames checks the fuzz target's harness: a SYN to the gateway's
// port 80 gets a reset, and an ARP request for the gateway a reply.
func TestRunFrames(t *testing.T) {
	ip4 := uint16(header.IPv4ProtocolNumber)
	syn := nt.FromGuest(ip4, nt.IPv4(nt.Leased, nt.Gateway, uint8(header.TCPProtocolNumber),
		nt.TCPSegment(nt.Leased, nt.Gateway, 40000, 80, header.TCPFlagSyn)))
	arpReq := nt.Ethernet(nt.Broadcast, nt.GuestMAC, uint16(header.ARPProtocolNumber), nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, nt.Gateway))
	sent := runFrames(t, [][]byte{syn, arpReq})
	var rst, arpReply bool
	for _, f := range sent {
		if msg := checkSent(f); msg != "" {
			t.Fatalf("%s: %x", msg, f)
		}
		p := f[header.EthernetMinimumSize:]
		switch header.Ethernet(f).Type() {
		case header.ARPProtocolNumber:
			arpReply = header.ARP(p).Op() == header.ARPReply
		case header.IPv4ProtocolNumber:
			rst = rst || header.TCP(header.IPv4(p).Payload()).Flags().Contains(header.TCPFlagRst)
		}
	}
	if !rst || !arpReply {
		t.Fatalf("reset %v, ARP reply %v, in %d frames", rst, arpReply, len(sent))
	}
}
