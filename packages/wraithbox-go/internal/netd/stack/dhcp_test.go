package stack

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

var limitedBroadcast = netip.MustParseAddr("255.255.255.255")

// dhcpFrame is a DHCP message from the guest's port 68 to port 67.
func dhcpFrame(src, dst netip.Addr, msg []byte) []byte {
	ethDst := nt.Broadcast
	if dst == nt.Gateway {
		ethDst = nt.GatewayMAC
	}
	return nt.Ethernet(ethDst, nt.GuestMAC, uint16(header.IPv4ProtocolNumber),
		nt.IPv4(src, dst, uint8(header.UDPProtocolNumber), nt.UDP(68, 67, msg)))
}

func dhcpRequest(typ byte, ciaddr netip.Addr, opts ...[]byte) []byte {
	return nt.DHCP(0x1234, ciaddr, nt.GuestMAC, append([][]byte{nt.DHCPOpt(53, typ)}, opts...)...)
}

// dhcpReply checks that a frame is a DHCP reply from the gateway's port
// 67 to port 68 at the destination MAC and address, and returns its
// message and options.
func dhcpReply(t *testing.T, frame []byte, mac tcpip.LinkAddress, dst netip.Addr) ([]byte, map[byte][]byte) {
	t.Helper()
	eth := header.Ethernet(frame)
	if eth.DestinationAddress() != mac || eth.Type() != header.IPv4ProtocolNumber {
		t.Fatalf("a frame to %s of type %#x", eth.DestinationAddress(), eth.Type())
	}
	ip := header.IPv4(frame[header.EthernetMinimumSize:])
	if got := netip.AddrFrom4(ip.DestinationAddress().As4()); got != dst || netip.AddrFrom4(ip.SourceAddress().As4()) != nt.Gateway {
		t.Fatalf("a packet from %s to %s", ip.SourceAddress(), got)
	}
	u := header.UDP(ip.Payload())
	if u.SourcePort() != 67 || u.DestinationPort() != 68 {
		t.Fatalf("UDP from %d to %d", u.SourcePort(), u.DestinationPort())
	}
	msg := u.Payload()
	_, opts, ok := nt.DHCPOptions(msg)
	if !ok {
		t.Fatalf("the reply's options don't parse: %x", msg)
	}
	return msg, opts
}

// TestDHCP sends the guest's DISCOVER, REQUEST and INFORM from 0.0.0.0
// through the link filter, and checks that OFFER and ACK come back by
// broadcast with the leased address and option 26 the link MTU, that a
// second DISCOVER gets the same address, and that a renewal from the
// leased address is answered by unicast (S07-02-packet-path,
// "Addressing" and "MTU").
func TestDHCP(t *testing.T) {
	h := start(t, nt.Config(), netd.Hooks{}, nil)
	zero := netip.IPv4Unspecified()
	bcast := header.EthernetBroadcastAddress
	leased := nt.Leased.As4()
	mtu := binary.BigEndian.AppendUint16(nil, uint16(netd.DefaultMTU))
	for i, step := range []struct {
		name   string
		frame  []byte
		reply  byte
		yiaddr netip.Addr
	}{
		{"DISCOVER", dhcpFrame(zero, limitedBroadcast, dhcpRequest(1, zero)), 2, nt.Leased},
		{"REQUEST", dhcpFrame(zero, limitedBroadcast, dhcpRequest(3, zero, nt.DHCPOpt(50, leased[:]...))), 5, nt.Leased},
		{"second DISCOVER", dhcpFrame(zero, limitedBroadcast, dhcpRequest(1, zero)), 2, nt.Leased},
		{"INFORM", dhcpFrame(zero, limitedBroadcast, dhcpRequest(8, zero)), 5, zero},
		{"to the subnet broadcast", dhcpFrame(zero, netd.BroadcastOf(nt.Subnet), dhcpRequest(1, zero)), 2, nt.Leased},
	} {
		h.send(step.frame)
		msg, opts := dhcpReply(t, h.receive(), bcast, limitedBroadcast)
		if opts[53][0] != step.reply {
			t.Fatalf("%d %s: reply type %d, want %d", i, step.name, opts[53][0], step.reply)
		}
		if y := nt.DHCPAddr(msg, nt.DHCPOffYIAddr); y != step.yiaddr {
			t.Fatalf("%s: yiaddr %s, want %s", step.name, y, step.yiaddr)
		}
		if string(opts[26]) != string(mtu) {
			t.Fatalf("%s: option 26 is %x, want %x", step.name, opts[26], mtu)
		}
	}
	t.Run("renewal by unicast", func(t *testing.T) {
		h.send(dhcpFrame(nt.Leased, nt.Gateway, dhcpRequest(3, nt.Leased)))
		msg, opts := dhcpReply(t, h.receive(), tcpip.LinkAddress(nt.GuestMAC), nt.Leased)
		if opts[53][0] != 5 || nt.DHCPAddr(msg, nt.DHCPOffYIAddr) != nt.Leased {
			t.Fatalf("type %d, yiaddr %s", opts[53][0], nt.DHCPAddr(msg, nt.DHCPOffYIAddr))
		}
	})
	t.Run("drops", func(_ *testing.T) {
		h.send(dhcpFrame(zero, limitedBroadcast, nt.DHCP(1, zero, nt.OtherMAC, nt.DHCPOpt(53, 1))))
		h.waitRule(netd.RuleDHCPMAC)
		h.send(dhcpFrame(zero, limitedBroadcast, []byte("not DHCP")))
		h.waitRule(netd.RuleDHCPForm)
		h.send(dhcpFrame(zero, limitedBroadcast, dhcpRequest(4, zero)))
		h.waitRule(netd.RuleDHCPDecline)
		h.quiet()
	})
	if n := h.s.Recorder().Count(netd.RuleUDPUnbound); n != 0 {
		t.Fatalf("%d DHCP messages dropped as udp-unbound", n)
	}
}
