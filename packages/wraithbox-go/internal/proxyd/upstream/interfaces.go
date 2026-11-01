package upstream

import (
	"fmt"
	"net"
	"net/netip"
)

// hostNetworks reads the address and on-link prefix of every interface
// of the host, up or down, from net.Interfaces. The dialer calls it at
// each connect and keeps nothing, so a network the host joins later is
// refused from its first connect on (S07-07-hosts-and-modes, "Upstream
// address", the host's own networks).
func hostNetworks() ([]netip.Prefix, error) {
	links, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("read the host's interfaces: %w", err)
	}
	var out []netip.Prefix
	for _, link := range links {
		addrs, err := link.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read the addresses of interface %s: %w", link.Name, err)
		}
		for _, a := range addrs {
			p, ok := interfacePrefix(a)
			if !ok {
				return nil, fmt.Errorf("interface %s has address %q that isn't an IP address", link.Name, a.String())
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// interfacePrefix turns one interface address into the prefix it is
// on, keeping the assigned address in it. An address with a mask that
// isn't a prefix length counts as the address alone.
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
			ones = ip.BitLen()
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
