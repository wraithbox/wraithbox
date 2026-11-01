// Package netdtest builds the link configuration and the frames the tests
// of wb-netd's packet path use. It builds frames with gVisor's header
// package, apart from the link filter's own parser.
package netdtest

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/netip"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// The addresses of the test link.
var (
	GuestMAC   = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}
	GatewayMAC = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}
	OtherMAC   = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x03}
	Broadcast  = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	Gateway = netip.MustParseAddr("192.168.127.1")
	Leased  = netip.MustParseAddr("192.168.127.2")
	Subnet  = netip.MustParsePrefix("192.168.127.0/24")
)

// Config is the test link's configuration, with the default MTU and
// caps.
func Config() netd.Config {
	return netd.Config{
		GuestMAC:   GuestMAC,
		GatewayMAC: GatewayMAC,
		Gateway:    Gateway,
		Leased:     Leased,
		Subnet:     Subnet,
		Synthetic:  netd.DefaultSynthetic,
		MTU:        netd.DefaultMTU,
		Caps:       netd.DefaultCaps(),
	}
}

// Ethernet is a frame from src to dst.
func Ethernet(dst, src net.HardwareAddr, etherType uint16, payload []byte) []byte {
	b := make([]byte, header.EthernetMinimumSize+len(payload))
	header.Ethernet(b).Encode(&header.EthernetFields{
		SrcAddr: tcpip.LinkAddress(src),
		DstAddr: tcpip.LinkAddress(dst),
		Type:    tcpip.NetworkProtocolNumber(etherType),
	})
	copy(b[header.EthernetMinimumSize:], payload)
	return b
}

// FromGuest is a frame from the guest's MAC to the gateway's.
func FromGuest(etherType uint16, payload []byte) []byte {
	return Ethernet(GatewayMAC, GuestMAC, etherType, payload)
}

// ARP is an Ethernet/IPv4 ARP packet.
func ARP(op header.ARPOp, sha net.HardwareAddr, spa, tpa netip.Addr) []byte {
	b := make([]byte, header.ARPSize)
	a := header.ARP(b)
	a.SetIPv4OverEthernet()
	a.SetOp(op)
	copy(a.HardwareAddressSender(), sha)
	s, t := spa.As4(), tpa.As4()
	copy(a.ProtocolAddressSender(), s[:])
	copy(a.ProtocolAddressTarget(), t[:])
	return b
}

// IPv4 is an IPv4 packet without options and with a valid checksum.
func IPv4(src, dst netip.Addr, proto uint8, payload []byte) []byte {
	return IPv4With(src, dst, proto, nil, 0, payload)
}

// IPv4With is an IPv4 packet with options, which are a multiple of 4
// bytes, and the flags and fragment offset field, with a valid checksum.
func IPv4With(src, dst netip.Addr, proto uint8, options []byte, flagsOffset uint16, payload []byte) []byte {
	headerLen := header.IPv4MinimumSize + len(options)
	b := make([]byte, headerLen+len(payload))
	ip := header.IPv4(b)
	ip.Encode(&header.IPv4Fields{
		TotalLength: u16(len(b)),
		TTL:         64,
		Protocol:    proto,
		SrcAddr:     tcpip.AddrFrom4(src.As4()),
		DstAddr:     tcpip.AddrFrom4(dst.As4()),
	})
	ip.SetHeaderLength(u8(headerLen))
	copy(b[header.IPv4MinimumSize:], options)
	binary.BigEndian.PutUint16(b[6:8], flagsOffset)
	copy(b[headerLen:], payload)
	ip.SetChecksum(0)
	ip.SetChecksum(^ip.CalculateChecksum())
	return b
}

// UDP is a UDP datagram without a checksum.
func UDP(src, dst uint16, data []byte) []byte {
	b := make([]byte, header.UDPMinimumSize+len(data))
	header.UDP(b).Encode(&header.UDPFields{SrcPort: src, DstPort: dst, Length: u16(len(b))})
	copy(b[header.UDPMinimumSize:], data)
	return b
}

// TCPSyn is a TCP SYN without options or a checksum.
func TCPSyn(src, dst uint16) []byte {
	b := make([]byte, header.TCPMinimumSize)
	header.TCP(b).Encode(&header.TCPFields{
		SrcPort:    src,
		DstPort:    dst,
		DataOffset: header.TCPMinimumSize,
		Flags:      header.TCPFlagSyn,
		WindowSize: 65535,
	})
	return b
}

// TCPSegment is a TCP segment without options or data, with a valid
// checksum for its addresses.
func TCPSegment(src, dst netip.Addr, srcPort, dstPort uint16, flags header.TCPFlags) []byte {
	b := make([]byte, header.TCPMinimumSize)
	h := header.TCP(b)
	h.Encode(&header.TCPFields{
		SrcPort:    srcPort,
		DstPort:    dstPort,
		SeqNum:     1000,
		DataOffset: header.TCPMinimumSize,
		Flags:      flags,
		WindowSize: 65535,
	})
	sum := header.PseudoHeaderChecksum(header.TCPProtocolNumber,
		tcpip.AddrFrom4(src.As4()), tcpip.AddrFrom4(dst.As4()), u16(len(b)))
	h.SetChecksum(^h.CalculateChecksum(sum))
	return b
}

// ICMP is an ICMPv4 message of a type and code, with 8 bytes of body
// and a valid checksum.
func ICMP(typ header.ICMPv4Type, code header.ICMPv4Code) []byte {
	b := make([]byte, header.ICMPv4MinimumSize+8)
	h := header.ICMPv4(b)
	h.SetType(typ)
	h.SetCode(code)
	h.SetChecksum(^checksum(b))
	return b
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	return uint16(sum)
}

// u16 is a length as a header field. A test that builds a packet over
// 65535 bytes is wrong, so it panics.
func u16(n int) uint16 {
	if n < 0 || n > math.MaxUint16 {
		panic(fmt.Sprintf("netdtest: length %d doesn't fit a header field", n))
	}
	return uint16(n)
}

// u8 is a header length as a header field.
func u8(n int) uint8 {
	if n < 0 || n > math.MaxUint8 {
		panic(fmt.Sprintf("netdtest: header length %d doesn't fit a header field", n))
	}
	return uint8(n)
}
