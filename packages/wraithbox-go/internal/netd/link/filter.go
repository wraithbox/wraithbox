package link

import (
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// Frame layout: the fields the filter reads, at their offsets.
const (
	ethHeader    = packetlink.EthernetHeader
	etherTypeARP = 0x0806
	etherTypeIP4 = 0x0800

	arpLen       = 28 // Ethernet/IPv4 ARP
	arpRequest   = 1
	arpReply     = 2
	ipv4Header   = 20 // no options
	ipv4Version  = 4
	ipFlagMF     = 0x2000
	ipFlagEvil   = 0x8000 // the reserved bit
	ipOffsetMask = 0x1fff
	udpHeader    = 8
	icmpHeader   = 8

	protoICMP = 1
	protoTCP  = 6
	protoUDP  = 17

	icmpEchoRequest = 8
	portDNS         = 53
	portDHCPServer  = 67
	portDHCPClient  = 68
)

var broadcastMAC = [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

// Filter is the link filter of S07-02-packet-path, "Link filter": it
// checks every frame from the guest before the stack sees it, and drops
// it unless it passes all six steps, in their order (SEC05-default-deny).
// It reads only the bytes of the frame, and keeps no state, so Check is
// safe for concurrent use.
type Filter struct {
	guestMAC, gatewayMAC [6]byte
	gateway, leased      [4]byte
	subnetBroadcast      [4]byte
	maxFrame             int
}

// NewFilter returns the filter of a link. cfg is valid (Config.Validate).
func NewFilter(cfg *netd.Config) *Filter {
	return &Filter{
		guestMAC:        [6]byte(cfg.GuestMAC),
		gatewayMAC:      [6]byte(cfg.GatewayMAC),
		gateway:         cfg.Gateway.As4(),
		leased:          cfg.Leased.As4(),
		subnetBroadcast: netd.BroadcastOf(cfg.Subnet).As4(),
		maxFrame:        packetlink.MaxFrame(cfg.MTU),
	}
}

// Check reports whether a frame passes the filter, and when it doesn't,
// the rule of the first step it fails. A frame that passes goes to the
// stack, which decides TCP under "Connections".
func (f *Filter) Check(frame []byte) (netd.Rule, bool) {
	// Step 1: 14 to MTU + 14 bytes.
	if len(frame) < ethHeader || len(frame) > f.maxFrame {
		return netd.RuleFrameSize, false
	}
	// Step 2: from the guest's MAC, to the gateway's or broadcast.
	dst, src := [6]byte(frame[0:6]), [6]byte(frame[6:12])
	if src != f.guestMAC || (dst != f.gatewayMAC && dst != broadcastMAC) {
		return netd.RuleFrameMAC, false
	}
	// Step 3: ARP or IPv4. IPv6 and every other EtherType are dropped.
	payload := frame[ethHeader:]
	switch binary.BigEndian.Uint16(frame[12:14]) {
	case etherTypeARP:
		return f.checkARP(payload)
	case etherTypeIP4:
		return f.checkIPv4(payload)
	default:
		return netd.RuleFrameEtherType, false
	}
}

// checkARP is step 4: Ethernet/IPv4 ARP, a request or reply, from the
// guest's MAC and leased address, for the gateway. A probe for the
// leased address (sender 0.0.0.0) and a gratuitous ARP (target the
// leased address) both fail it, so the stack never answers either and
// nothing the guest sends claims another address.
func (f *Filter) checkARP(p []byte) (netd.Rule, bool) {
	if len(p) < arpLen {
		return netd.RuleARPForm, false
	}
	hwType, protoType := binary.BigEndian.Uint16(p[0:2]), binary.BigEndian.Uint16(p[2:4])
	hwLen, protoLen, op := p[4], p[5], binary.BigEndian.Uint16(p[6:8])
	sha, spa, tpa := [6]byte(p[8:14]), [4]byte(p[14:18]), [4]byte(p[24:28])
	switch {
	case hwType != 1 || protoType != etherTypeIP4 || hwLen != 6 || protoLen != 4:
	case op != arpRequest && op != arpReply:
	case sha != f.guestMAC || spa != f.leased || tpa != f.gateway:
	default:
		return "", true
	}
	return netd.RuleARPForm, false
}

// ipv4 is the part of an IPv4 packet that step 6 reads.
type ipv4 struct {
	src, dst [4]byte
	proto    byte
	payload  []byte // up to the header's total length
}

// checkIPv4 is step 5 and step 6 (SEC05-default-deny).
func (f *Filter) checkIPv4(p []byte) (netd.Rule, bool) {
	ip, ok := parseIPv4(p)
	if !ok {
		return netd.RuleIPv4Form, false
	}
	dhcp := isDHCP(ip)
	if ip.src != f.leased && (ip.src != [4]byte{} || !dhcp) {
		return netd.RuleIPv4Source, false
	}
	if f.badDestination(ip.dst) {
		return netd.RuleIPv4Destination, false
	}
	return f.checkProtocol(ip, dhcp)
}

// badDestination is the destination check of step 5: the guest's own
// leased address, 0.0.0.0/8 and 127.0.0.0/8. The stack would answer TCP
// to the leased address with a reset to the gateway's own MAC, and drops
// the other two itself, uncounted.
func (f *Filter) badDestination(dst [4]byte) bool {
	return dst == f.leased || dst[0] == 0 || dst[0] == 127
}

// parseIPv4 is the form check of step 5: version 4, a header of 20
// bytes with a valid checksum, a total length within the frame, and not
// a fragment. The reserved flag bit set fails it too.
func parseIPv4(p []byte) (ipv4, bool) {
	if len(p) < ipv4Header || p[0] != ipv4Version<<4|ipv4Header/4 {
		return ipv4{}, false
	}
	total := int(binary.BigEndian.Uint16(p[2:4]))
	frag := binary.BigEndian.Uint16(p[6:8])
	if total < ipv4Header || total > len(p) || frag&(ipFlagMF|ipFlagEvil|ipOffsetMask) != 0 || checksum(p[:ipv4Header]) != 0xffff {
		return ipv4{}, false
	}
	return ipv4{
		src:     [4]byte(p[12:16]),
		dst:     [4]byte(p[16:20]),
		proto:   p[9],
		payload: p[ipv4Header:total],
	}, true
}

// checksum is the ones' complement sum of b, 0xffff over a header with
// a valid checksum.
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	return uint16(sum)
}

// isDHCP reports UDP from port 68 to port 67.
func isDHCP(ip ipv4) bool {
	return ip.proto == protoUDP && len(ip.payload) >= udpHeader &&
		binary.BigEndian.Uint16(ip.payload[0:2]) == portDHCPClient &&
		binary.BigEndian.Uint16(ip.payload[2:4]) == portDHCPServer
}

// checkProtocol is step 6: TCP passes to the stack, UDP only to the
// gateway's port 53 or as DHCP to the gateway or broadcast, and ICMP
// only as an echo request to the gateway. Multicast and broadcast other
// than DHCP, and every other protocol, are dropped.
func (f *Filter) checkProtocol(ip ipv4, dhcp bool) (netd.Rule, bool) {
	broadcast := ip.dst == [4]byte{255, 255, 255, 255} || ip.dst == f.subnetBroadcast
	if ip.dst[0]&0xf0 == 0xe0 || (broadcast && !dhcp) {
		return netd.RuleIPBroadcast, false
	}
	switch ip.proto {
	case protoTCP:
		return "", true
	case protoUDP:
		return f.checkUDP(ip, dhcp, broadcast)
	case protoICMP:
		return f.checkICMP(ip)
	default:
		return netd.RuleIPProtocol, false
	}
}

// checkUDP passes DHCP to the gateway or broadcast, and DNS to the
// gateway's port 53.
func (f *Filter) checkUDP(ip ipv4, dhcp, broadcast bool) (netd.Rule, bool) {
	if dhcp && (broadcast || ip.dst == f.gateway) {
		return "", true
	}
	if ip.dst == f.gateway && len(ip.payload) >= udpHeader && binary.BigEndian.Uint16(ip.payload[2:4]) == portDNS {
		return "", true
	}
	return netd.RuleUDPNotDNS, false
}

// checkICMP passes an echo request to the gateway.
func (f *Filter) checkICMP(ip ipv4) (netd.Rule, bool) {
	if ip.dst == f.gateway && len(ip.payload) >= icmpHeader && ip.payload[0] == icmpEchoRequest && ip.payload[1] == 0 {
		return "", true
	}
	return netd.RuleICMPNotEcho, false
}

// Describe returns what a drop of a frame is logged with: its length,
// MACs and EtherType, and for IPv4 its addresses, protocol and ports.
// Only numbers, addresses and MACs, never the guest's bytes as text
// (S07-02-packet-path, "Events and rate limits").
func Describe(frame []byte) []slog.Attr {
	attrs := []slog.Attr{slog.Int("bytes", len(frame))}
	if len(frame) < ethHeader {
		return attrs
	}
	attrs = append(attrs,
		slog.String("src_mac", net.HardwareAddr(frame[6:12]).String()),
		slog.String("dst_mac", net.HardwareAddr(frame[0:6]).String()),
		slog.Int("ether_type", int(binary.BigEndian.Uint16(frame[12:14]))),
	)
	p := frame[ethHeader:]
	if binary.BigEndian.Uint16(frame[12:14]) != etherTypeIP4 || len(p) < ipv4Header {
		return attrs
	}
	attrs = append(attrs,
		slog.String("src", netip.AddrFrom4([4]byte(p[12:16])).String()),
		slog.String("dst", netip.AddrFrom4([4]byte(p[16:20])).String()),
		slog.Int("proto", int(p[9])),
	)
	ihl := int(p[0]&0x0f) * 4
	if (p[9] == protoTCP || p[9] == protoUDP) && ihl >= ipv4Header && len(p) >= ihl+4 {
		attrs = append(attrs,
			slog.Int("src_port", int(binary.BigEndian.Uint16(p[ihl:]))),
			slog.Int("dst_port", int(binary.BigEndian.Uint16(p[ihl+2:]))),
		)
	}
	return attrs
}
