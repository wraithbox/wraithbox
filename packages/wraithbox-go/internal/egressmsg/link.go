package egressmsg

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// CheckAddressing: a NetdStart whose addressing doesn't describe one
// link (control.proto, NetdStart; S07-02-packet-path, "Addressing").
const CheckAddressing Check = "addressing"

// The bounds of a link's prefix length: a subnet of at most 2^24
// addresses, and of at least 4, so it holds a network address, a
// broadcast address and two host addresses.
const (
	minPrefixLen = 8
	maxPrefixLen = 30
)

// Link is the addressing of one VM's link, as NetdStart carries it.
type Link struct {
	GuestMAC   net.HardwareAddr
	GatewayMAC net.HardwareAddr
	Subnet     netip.Prefix
	Gateway    netip.Addr
	Leased     netip.Addr
}

// NewNetdStart returns the NetdStart of a VM, its generation and its
// link. A link with an address that isn't IPv4 gives a message that
// DecodeHostdToNetd refuses.
func NewNetdStart(vm string, generation uint64, l Link) *egressv1.NetdStart {
	subnet, _ := AddrToWire(l.Subnet.Addr())
	gateway, _ := AddrToWire(l.Gateway)
	leased, _ := AddrToWire(l.Leased)
	// Bits is -1 for an invalid prefix, which leaves 0 and is refused.
	var bits uint32
	if b := l.Subnet.Bits(); b > 0 && b <= 32 {
		bits = uint32(b)
	}
	return &egressv1.NetdStart{
		Vm: vm, Generation: generation,
		GuestMac: append([]byte(nil), l.GuestMAC...), GatewayMac: append([]byte(nil), l.GatewayMAC...),
		Subnet: subnet, PrefixLen: bits, Gateway: gateway, Leased: leased,
	}
}

// LinkOf returns the link a NetdStart describes, or refuses it with
// CheckAddressing: MACs that aren't two distinct unicast Ethernet
// addresses, a prefix length outside 8 to 30, a subnet with host bits
// set, or a gateway or leased address that isn't a host address inside
// the subnet, or that are the same.
func LinkOf(m *egressv1.NetdStart) (Link, *Refusal) {
	l := Link{
		GuestMAC: net.HardwareAddr(m.GetGuestMac()), GatewayMAC: net.HardwareAddr(m.GetGatewayMac()),
		Gateway: AddrFromWire(m.GetGateway()), Leased: AddrFromWire(m.GetLeased()),
	}
	if r := checkMACs(l.GuestMAC, l.GatewayMAC); r != nil {
		return Link{}, r
	}
	bits := m.GetPrefixLen()
	if bits < minPrefixLen || bits > maxPrefixLen {
		return Link{}, refuse(CheckAddressing, "netd_start.prefix_len",
			fmt.Sprintf("prefix length %d, want %d to %d", bits, minPrefixLen, maxPrefixLen))
	}
	l.Subnet = netip.PrefixFrom(AddrFromWire(m.GetSubnet()), int(bits))
	if l.Subnet != l.Subnet.Masked() {
		return Link{}, refuse(CheckAddressing, "netd_start.subnet", "the subnet has host bits set")
	}
	if r := checkHost("netd_start.gateway", l.Gateway, l.Subnet); r != nil {
		return Link{}, r
	}
	if r := checkHost("netd_start.leased", l.Leased, l.Subnet); r != nil {
		return Link{}, r
	}
	if l.Gateway == l.Leased {
		return Link{}, refuse(CheckAddressing, "netd_start.leased", "the leased address is the gateway's")
	}
	return l, nil
}

func checkMACs(guest, gateway net.HardwareAddr) *Refusal {
	for field, mac := range map[string]net.HardwareAddr{"netd_start.guest_mac": guest, "netd_start.gateway_mac": gateway} {
		if len(mac) != 6 || mac[0]&1 != 0 {
			return refuse(CheckAddressing, field, fmt.Sprintf("%d bytes, want a unicast 6-byte MAC", len(mac)))
		}
	}
	if string(guest) == string(gateway) {
		return refuse(CheckAddressing, "netd_start.gateway_mac", "the gateway's MAC is the guest's")
	}
	return nil
}

// checkHost refuses an address that isn't in the subnet, or is its
// network or broadcast address.
func checkHost(field string, a netip.Addr, subnet netip.Prefix) *Refusal {
	network := subnet.Addr().As4()
	host := uint32(1)<<(32-subnet.Bits()) - 1
	var last [4]byte
	binary.BigEndian.PutUint32(last[:], binary.BigEndian.Uint32(network[:])|host)
	if !subnet.Contains(a) || a == subnet.Addr() || a == netip.AddrFrom4(last) {
		return refuse(CheckAddressing, field, fmt.Sprintf("%s isn't a host address in %s", a, subnet))
	}
	return nil
}
