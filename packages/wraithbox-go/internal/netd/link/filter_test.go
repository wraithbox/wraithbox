package link

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

var (
	zero      = netip.IPv4Unspecified()
	limited   = netip.MustParseAddr("255.255.255.255")
	directed  = netip.MustParseAddr("192.168.127.255")
	public    = netip.MustParseAddr("1.1.1.1")
	otherHost = netip.MustParseAddr("192.168.127.3")
	multicast = netip.MustParseAddr("224.0.0.251")
)

const (
	ethARP  = uint16(header.ARPProtocolNumber)
	ethIPv4 = uint16(header.IPv4ProtocolNumber)
	ethIPv6 = uint16(header.IPv6ProtocolNumber)
	tcpNum  = uint8(header.TCPProtocolNumber)
	udpNum  = uint8(header.UDPProtocolNumber)
	icmpNum = uint8(header.ICMPv4ProtocolNumber)
)

func ip4(src, dst netip.Addr, proto uint8, payload []byte) []byte {
	return nt.FromGuest(ethIPv4, nt.IPv4(src, dst, proto, payload))
}

type filterCase struct {
	name  string
	frame []byte
	rule  netd.Rule // "" passes
}

// filterCases are the cases of S07-02-packet-path, "Link filter", each
// frame with the rule that drops it, or "" for a frame that passes.
func filterCases() []filterCase {
	mtu := nt.Config().MTU
	syn := nt.TCPSyn(40000, 443)
	dhcp := nt.UDP(68, 67, make([]byte, 300))
	badSum := nt.IPv4(nt.Leased, nt.Gateway, tcpNum, syn)
	badSum[10] ^= 0xff
	shortLen := nt.IPv4(nt.Leased, nt.Gateway, tcpNum, syn)
	shortLen[3] = 19
	return []filterCase{
		// Passes.
		{"TCP to a public address", ip4(nt.Leased, public, tcpNum, syn), ""},
		{"TCP to the gateway", ip4(nt.Leased, nt.Gateway, tcpNum, syn), ""},
		{"DNS to the gateway", ip4(nt.Leased, nt.Gateway, udpNum, nt.UDP(5353, 53, []byte("q"))), ""},
		{"DHCP discover from 0.0.0.0", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethIPv4, nt.IPv4(zero, limited, udpNum, dhcp)), ""},
		{"DHCP to the subnet broadcast", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethIPv4, nt.IPv4(nt.Leased, directed, udpNum, dhcp)), ""},
		{"DHCP renew to the gateway", ip4(nt.Leased, nt.Gateway, udpNum, dhcp), ""},
		{"ping to the gateway", ip4(nt.Leased, nt.Gateway, icmpNum, nt.ICMP(header.ICMPv4Echo, 0)), ""},
		{"ARP request for the gateway", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, nt.Gateway)), ""},
		{"ARP reply to the gateway", nt.FromGuest(ethARP, nt.ARP(header.ARPReply, nt.GuestMAC, nt.Leased, nt.Gateway)), ""},
		{"largest frame", ip4(nt.Leased, public, tcpNum, append(syn, make([]byte, mtu-20-len(syn))...)), ""},

		// Step 1.
		{"13 bytes", nt.FromGuest(ethIPv4, nil)[:13], netd.RuleFrameSize},
		{"MTU + 15 bytes", append(ip4(nt.Leased, public, tcpNum, append(syn, make([]byte, mtu-20-len(syn))...)), 0), netd.RuleFrameSize},
		// Step 2.
		{"another source MAC", nt.Ethernet(nt.GatewayMAC, nt.OtherMAC, ethIPv4, nt.IPv4(nt.Leased, public, tcpNum, syn)), netd.RuleFrameMAC},
		{"another destination MAC", nt.Ethernet(nt.OtherMAC, nt.GuestMAC, ethIPv4, nt.IPv4(nt.Leased, public, tcpNum, syn)), netd.RuleFrameMAC},
		// Step 3.
		{"IPv6", nt.FromGuest(ethIPv6, make([]byte, 40)), netd.RuleFrameEtherType},
		{"an Ethernet header only", nt.FromGuest(0x88cc, nil), netd.RuleFrameEtherType},
		// Step 4.
		{"ARP reply from another MAC", nt.FromGuest(ethARP, nt.ARP(header.ARPReply, nt.OtherMAC, nt.Leased, nt.Gateway)), netd.RuleARPForm},
		{"gratuitous ARP", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, nt.Leased)), netd.RuleARPForm},
		{"ARP probe for the leased address", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, zero, nt.Leased)), netd.RuleARPForm},
		{"ARP from another address", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, otherHost, nt.Gateway)), netd.RuleARPForm},
		{"ARP for another address", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, otherHost)), netd.RuleARPForm},
		{"ARP of another op", nt.FromGuest(ethARP, nt.ARP(3, nt.GuestMAC, nt.Leased, nt.Gateway)), netd.RuleARPForm},
		{"short ARP", nt.FromGuest(ethARP, nt.ARP(header.ARPRequest, nt.GuestMAC, nt.Leased, nt.Gateway)[:27]), netd.RuleARPForm},
		// Step 5.
		{"IPv4 with options", nt.FromGuest(ethIPv4, nt.IPv4With(nt.Leased, public, tcpNum, []byte{1, 1, 1, 0}, 0, syn)), netd.RuleIPv4Form},
		{"first fragment", nt.FromGuest(ethIPv4, nt.IPv4With(nt.Leased, public, tcpNum, nil, 0x2000, syn)), netd.RuleIPv4Form},
		{"later fragment", nt.FromGuest(ethIPv4, nt.IPv4With(nt.Leased, public, tcpNum, nil, 0x0010, syn)), netd.RuleIPv4Form},
		{"reserved flag", nt.FromGuest(ethIPv4, nt.IPv4With(nt.Leased, public, tcpNum, nil, 0x8000, syn)), netd.RuleIPv4Form},
		{"bad checksum", nt.FromGuest(ethIPv4, badSum), netd.RuleIPv4Form},
		{"total length under 20", nt.FromGuest(ethIPv4, shortLen), netd.RuleIPv4Form},
		{"short IPv4", nt.FromGuest(ethIPv4, nt.IPv4(nt.Leased, public, tcpNum, nil)[:19]), netd.RuleIPv4Form},
		{"spoofed source", ip4(otherHost, public, tcpNum, syn), netd.RuleIPv4Source},
		{"0.0.0.0 to TCP", ip4(zero, public, tcpNum, syn), netd.RuleIPv4Source},
		{"0.0.0.0 to DNS", ip4(zero, nt.Gateway, udpNum, nt.UDP(5353, 53, nil)), netd.RuleIPv4Source},
		// Step 6.
		{"multicast", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethIPv4, nt.IPv4(nt.Leased, multicast, udpNum, nt.UDP(5353, 5353, nil))), netd.RuleIPBroadcast},
		{"broadcast other than DHCP", nt.Ethernet(nt.Broadcast, nt.GuestMAC, ethIPv4, nt.IPv4(nt.Leased, limited, udpNum, nt.UDP(137, 137, nil))), netd.RuleIPBroadcast},
		{"TCP to the subnet broadcast", ip4(nt.Leased, directed, tcpNum, syn), netd.RuleIPBroadcast},
		{"UDP to another DNS server", ip4(nt.Leased, public, udpNum, nt.UDP(5353, 53, nil)), netd.RuleUDPNotDNS},
		{"UDP to another gateway port", ip4(nt.Leased, nt.Gateway, udpNum, nt.UDP(5353, 123, nil)), netd.RuleUDPNotDNS},
		{"DHCP to another host", ip4(nt.Leased, otherHost, udpNum, dhcp), netd.RuleUDPNotDNS},
		{"short UDP", ip4(nt.Leased, nt.Gateway, udpNum, nt.UDP(5353, 53, nil)[:7]), netd.RuleUDPNotDNS},
		{"ping to another address", ip4(nt.Leased, public, icmpNum, nt.ICMP(header.ICMPv4Echo, 0)), netd.RuleICMPNotEcho},
		{"ICMP other than echo", ip4(nt.Leased, nt.Gateway, icmpNum, nt.ICMP(header.ICMPv4DstUnreachable, 3)), netd.RuleICMPNotEcho},
		{"echo with a code", ip4(nt.Leased, nt.Gateway, icmpNum, nt.ICMP(header.ICMPv4Echo, 1)), netd.RuleICMPNotEcho},
		{"SCTP", ip4(nt.Leased, public, 132, make([]byte, 12)), netd.RuleIPProtocol},
	}
}

func TestFilter(t *testing.T) {
	cfg := nt.Config()
	f := NewFilter(&cfg)
	for _, tc := range filterCases() {
		t.Run(tc.name, func(t *testing.T) {
			rule, ok := f.Check(tc.frame)
			if ok != (tc.rule == "") || rule != tc.rule {
				t.Fatalf("Check = %q, %v; want %q", rule, ok, tc.rule)
			}
			if ref := reference(&cfg, tc.frame); (ref == nil) != ok {
				t.Fatalf("the reference check says %v", ref)
			}
		})
	}
}

// TestFilterMTU checks the size step at the smallest MTU too.
func TestFilterMTU(t *testing.T) {
	cfg := nt.Config()
	cfg.MTU = packetlink.MinMTU
	f := NewFilter(&cfg)
	syn := nt.TCPSyn(40000, 443)
	fits := ip4(nt.Leased, public, tcpNum, append(syn, make([]byte, cfg.MTU-20-len(syn))...))
	if _, ok := f.Check(fits); !ok {
		t.Fatalf("a frame of %d bytes was dropped", len(fits))
	}
	over := ip4(nt.Leased, public, tcpNum, append(syn, make([]byte, cfg.MTU-20-len(syn)+1)...))
	if rule, ok := f.Check(over); ok || rule != netd.RuleFrameSize {
		t.Fatalf("a frame of %d bytes: %q, %v", len(over), rule, ok)
	}
}

// FuzzLinkFilter is the link filter's fuzz target (C01-network-and-dns,
// "Fuzzing"). The filter and a second check written with gVisor's header
// package must agree on every frame, and a frame longer than MTU + 14
// bytes, which is what a truncated read fills the buffer with, never
// passes.
func FuzzLinkFilter(f *testing.F) {
	cfg := nt.Config()
	filter := NewFilter(&cfg)
	for _, tc := range filterCases() {
		f.Add(tc.frame)
	}
	f.Add(bytes.Repeat([]byte{0xff}, packetlink.ReadBufferSize(cfg.MTU)))
	f.Fuzz(func(t *testing.T, frame []byte) {
		rule, ok := filter.Check(frame)
		if ok && rule != "" || !ok && rule == "" {
			t.Fatalf("Check = %q, %v", rule, ok)
		}
		if ok && len(frame) > packetlink.MaxFrame(cfg.MTU) {
			t.Fatalf("a frame of %d bytes passed", len(frame))
		}
		if err := reference(&cfg, frame); (err == nil) != ok {
			t.Fatalf("the filter and the reference check disagree: Check = %q, reference: %v", rule, err)
		}
	})
}

// reference is the link filter written again with gVisor's header
// package: nil when a frame passes S07-02-packet-path, "Link filter".
func reference(cfg *netd.Config, frame []byte) error {
	if len(frame) < header.EthernetMinimumSize || len(frame) > packetlink.MaxFrame(cfg.MTU) {
		return errors.New("size")
	}
	eth := header.Ethernet(frame)
	dst := eth.DestinationAddress()
	if eth.SourceAddress() != tcpip.LinkAddress(cfg.GuestMAC) ||
		dst != tcpip.LinkAddress(cfg.GatewayMAC) && dst != header.EthernetBroadcastAddress {
		return errors.New("MAC")
	}
	payload := frame[header.EthernetMinimumSize:]
	switch eth.Type() {
	case header.ARPProtocolNumber:
		return referenceARP(cfg, payload)
	case header.IPv4ProtocolNumber:
		return referenceIPv4(cfg, payload)
	default:
		return fmt.Errorf("EtherType %#x", eth.Type())
	}
}

func referenceARP(cfg *netd.Config, p []byte) error {
	a := header.ARP(p)
	if !a.IsValid() || a.Op() != header.ARPRequest && a.Op() != header.ARPReply {
		return errors.New("ARP form")
	}
	if !bytes.Equal(a.HardwareAddressSender(), cfg.GuestMAC) ||
		netip.AddrFrom4([4]byte(a.ProtocolAddressSender())) != cfg.Leased ||
		netip.AddrFrom4([4]byte(a.ProtocolAddressTarget())) != cfg.Gateway {
		return errors.New("ARP addresses")
	}
	return nil
}

// referenceForm is step 5's form check.
func referenceForm(p []byte) error {
	ip := header.IPv4(p)
	if !ip.IsValid(len(p)) || ip.HeaderLength() != header.IPv4MinimumSize || !ip.IsChecksumValid() {
		return errors.New("IPv4 form")
	}
	if ip.Flags()&^header.IPv4FlagDontFragment != 0 || ip.FragmentOffset() != 0 {
		return errors.New("fragment")
	}
	return nil
}

// referenceDHCP reports UDP from port 68 to port 67.
func referenceDHCP(proto tcpip.TransportProtocolNumber, body []byte) bool {
	return proto == header.UDPProtocolNumber && len(body) >= header.UDPMinimumSize &&
		header.UDP(body).SourcePort() == 68 && header.UDP(body).DestinationPort() == 67
}

func referenceIPv4(cfg *netd.Config, p []byte) error {
	if err := referenceForm(p); err != nil {
		return err
	}
	ip := header.IPv4(p)
	src := netip.AddrFrom4(ip.SourceAddress().As4())
	dst := netip.AddrFrom4(ip.DestinationAddress().As4())
	body := p[header.IPv4MinimumSize:ip.TotalLength()]
	proto := ip.TransportProtocol()
	dhcp := referenceDHCP(proto, body)
	if src != cfg.Leased && (!src.IsUnspecified() || !dhcp) {
		return errors.New("source")
	}
	broadcast := dst == limited || dst == netd.BroadcastOf(cfg.Subnet)
	if dst.IsMulticast() || broadcast && !dhcp {
		return errors.New("broadcast")
	}
	if !referenceProtocol(cfg, proto, dst, body, dhcp && (broadcast || dst == cfg.Gateway)) {
		return errors.New("protocol")
	}
	return nil
}

// referenceProtocol is step 6 for a packet that isn't multicast, or
// broadcast other than DHCP.
func referenceProtocol(cfg *netd.Config, proto tcpip.TransportProtocolNumber, dst netip.Addr, body []byte, dhcpOK bool) bool {
	switch proto {
	case header.TCPProtocolNumber:
		return true
	case header.UDPProtocolNumber:
		return dhcpOK || dst == cfg.Gateway && len(body) >= header.UDPMinimumSize && header.UDP(body).DestinationPort() == 53
	case header.ICMPv4ProtocolNumber:
		return dst == cfg.Gateway && len(body) >= header.ICMPv4MinimumSize &&
			header.ICMPv4(body).Type() == header.ICMPv4Echo && header.ICMPv4(body).Code() == 0
	default:
		return false
	}
}
