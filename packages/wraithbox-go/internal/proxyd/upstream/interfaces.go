package upstream

import (
	"fmt"
	"net"
	"net/netip"
)

// hostNetworks reads every address assigned to an interface of the
// host, with its on-link prefix, in one net.InterfaceAddrs call. The
// dialer calls it at each connect and keeps nothing, so a network the
// host joins later is refused from its first connect on
// (S07-07-hosts-and-modes, "Upstream address", the host's own
// networks). An address it can't turn into a prefix fails the read.
func hostNetworks() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("read the host's interface addresses: %w", err)
	}
	out := make([]netip.Prefix, 0, len(addrs))
	for _, a := range addrs {
		p, ok := interfacePrefix(a)
		if !ok {
			return nil, fmt.Errorf("interface address %q isn't an IP address with a prefix length", a.String())
		}
		out = append(out, p)
	}
	return out, nil
}

// interfacePrefix turns one interface address into the prefix it is
// on, keeping the assigned address in it. An address that isn't an IP
// address, or whose mask isn't a prefix length, gives false, since the
// prefix it is on can't be known.
func interfacePrefix(a net.Addr) (netip.Prefix, bool) {
	switch v := a.(type) {
	case *net.IPNet:
		ip, ok := netip.AddrFromSlice(v.IP)
		if !ok {
			return netip.Prefix{}, false
		}
		ip = ip.Unmap()
		ones, bits := v.Mask.Size()
		if ip.Is4() && bits == 128 && ones >= 96 {
			// An IPv4 mask in the 16-byte form.
			ones, bits = ones-96, 32
		}
		if bits != ip.BitLen() {
			return netip.Prefix{}, false
		}
		return netip.PrefixFrom(ip, ones), true
	case *net.IPAddr:
		ip, ok := netip.AddrFromSlice(v.IP)
		if !ok {
			return netip.Prefix{}, false
		}
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()), true
	}
	return netip.Prefix{}, false
}

// onHostNetwork returns the host network that holds the address or an
// IPv4 address it embeds.
func onHostNetwork(nets []netip.Prefix, a netip.Addr) (netip.Prefix, bool) {
	for _, f := range forms(a) {
		for _, p := range nets {
			if p.Masked().Contains(f) {
				return p, true
			}
		}
	}
	return netip.Prefix{}, false
}
