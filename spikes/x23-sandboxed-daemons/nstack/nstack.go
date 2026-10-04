// Package nstack builds a gVisor netstack on a datagram socket that
// carries one Ethernet frame per datagram, as the Virtualization
// framework's file-handle network attachment does.
package nstack

import (
	"context"
	"net/netip"
	"os"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// MTU of the link.
const MTU = 1500

// New starts a stack with one NIC on f, address ip/24 and MAC mac.
func New(f *os.File, ip string, mac string) (*stack.Stack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	hw, err := parseMAC(mac)
	if err != nil {
		return nil, err
	}
	ch := channel.New(1024, MTU, hw)
	if terr := s.CreateNIC(1, ethernet.New(ch)); terr != nil {
		return nil, errOf(terr)
	}
	a := netip.MustParseAddr(ip)
	pa := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4(a.As4()).WithPrefix(),
	}
	pa.AddressWithPrefix.PrefixLen = 24
	if terr := s.AddProtocolAddress(1, pa, stack.AddressProperties{}); terr != nil {
		return nil, errOf(terr)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	// Outbound: frames from the stack to the socket.
	go func() {
		for {
			pkt := ch.ReadContext(context.Background())
			if pkt == nil {
				return
			}
			var frame []byte
			for _, v := range pkt.AsSlices() {
				frame = append(frame, v...)
			}
			pkt.DecRef()
			if _, err := f.Write(frame); err != nil {
				return
			}
		}
	}()
	// Inbound: frames from the socket to the stack.
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := f.Read(buf)
			if err != nil {
				return
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), buf[:n]...))})
			ch.InjectInbound(0, pkt)
			pkt.DecRef()
		}
	}()
	return s, nil
}

// Addr builds a full address.
func Addr(ip string, port uint16) tcpip.FullAddress {
	a := netip.MustParseAddr(ip)
	return tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(a.As4()), Port: port}
}

type tcpipErr struct{ e tcpip.Error }

func (t tcpipErr) Error() string { return t.e.String() }

func errOf(e tcpip.Error) error { return tcpipErr{e} }

func parseMAC(s string) (tcpip.LinkAddress, error) {
	return tcpip.ParseMACAddress(s)
}
