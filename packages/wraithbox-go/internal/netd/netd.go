// Package netd holds what wb-netd's packages share: the addresses of a
// VM's link (Config), the per-VM caps (Caps), the rules every drop and
// reset is counted and logged with (Rule, Recorder), and the hooks the
// DNS server, the port check and the stream hand-off plug into (Hooks).
//
// The packet path is in internal/netd/link (the link endpoint and its
// filter) and internal/netd/stack (gVisor's stack, the TCP rules and the
// caps), after S07-02-packet-path. wb-netd reads raw guest frames, so
// every input in these packages is adversarial (S04-01-design-principles).
package netd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/packetlink"
)

// Config is the addressing of one VM's link (S07-02-packet-path,
// "Addressing"). wb-netd gets it at start.
type Config struct {
	// GuestMAC is the MAC of the guest's NIC. The link filter drops every
	// frame from another source MAC.
	GuestMAC net.HardwareAddr
	// GatewayMAC is the MAC of wb-netd's side of the link.
	GatewayMAC net.HardwareAddr
	// Gateway is wb-netd's address: the guest's router and DNS server.
	Gateway netip.Addr
	// Leased is the one address the guest gets. The link filter drops
	// every packet from another source.
	Leased netip.Addr
	// Subnet holds the gateway and the leased address. Its last address
	// is the subnet-directed broadcast that DHCP may use.
	Subnet netip.Prefix
	// Synthetic is the pool the DNS server answers from, 198.18.0.0/15
	// (S07-03-dns). TCP to it goes to Hooks.Stream.
	Synthetic netip.Prefix
	// MTU is the link MTU, 65535 (S07-02-packet-path, "MTU").
	MTU int
	// Caps are the per-VM caps. DefaultCaps gives the defaults.
	Caps Caps
}

// DefaultMTU is the link MTU (S07-02-packet-path, "MTU").
const DefaultMTU = packetlink.MaxMTU

// DefaultSynthetic is the synthetic pool (S07-03-dns).
var DefaultSynthetic = netip.MustParsePrefix("198.18.0.0/15")

// Validate checks that a configuration describes one link: two distinct
// unicast MACs, IPv4 addresses inside an IPv4 subnet that are neither
// its network nor its broadcast address, a synthetic pool outside the
// subnet, an MTU the attachment takes, and caps of at least one.
func (c *Config) Validate() error {
	return errors.Join(c.validateMACs(), c.validateAddrs(), packetlink.CheckMTU(c.MTU), c.Caps.Validate())
}

func (c *Config) validateMACs() error {
	for name, mac := range map[string]net.HardwareAddr{"guest": c.GuestMAC, "gateway": c.GatewayMAC} {
		if len(mac) != 6 || mac[0]&1 != 0 {
			return fmt.Errorf("netd: the %s MAC %q isn't a unicast Ethernet address", name, mac)
		}
	}
	if string(c.GuestMAC) == string(c.GatewayMAC) {
		return fmt.Errorf("netd: the guest and the gateway share the MAC %s", c.GuestMAC)
	}
	return nil
}

func (c *Config) validateAddrs() error {
	s := c.Subnet
	if !s.IsValid() || !s.Addr().Is4() || s.Bits() > 30 || s != s.Masked() {
		return fmt.Errorf("netd: subnet %s isn't an IPv4 network of at least 4 addresses", s)
	}
	for name, a := range map[string]netip.Addr{"gateway": c.Gateway, "leased address": c.Leased} {
		if !a.Is4() || !s.Contains(a) || a == s.Addr() || a == BroadcastOf(s) {
			return fmt.Errorf("netd: the %s %s isn't a host address in %s", name, a, s)
		}
	}
	if c.Gateway == c.Leased {
		return fmt.Errorf("netd: the gateway and the leased address are both %s", c.Gateway)
	}
	p := c.Synthetic
	if !p.IsValid() || !p.Addr().Is4() || p.Overlaps(s) {
		return fmt.Errorf("netd: synthetic pool %s isn't an IPv4 network outside %s", p, s)
	}
	return nil
}

// BroadcastOf is the subnet-directed broadcast address of an IPv4
// prefix: its last address.
func BroadcastOf(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	binary.BigEndian.PutUint32(a[:], binary.BigEndian.Uint32(a[:])|host)
	return netip.AddrFrom4(a)
}

// Caps are the per-VM caps of S07-02-packet-path, "Per-VM caps"
// (SEC13-bounded-resources). S07-02 makes each one configuration with a
// default. Until wb-netd's start message carries them, the defaults are
// the values.
//
// gVisor has no stack-wide memory limit. The stack's buffer memory is
// bounded by construction: EstablishedTCP connections of at most
// TCPBuffer bytes each way.
type Caps struct {
	// HalfOpenTCP bounds TCP connections between their first SYN and the
	// end of the handshake. A SYN over it is dropped with
	// tcp-half-open-cap.
	HalfOpenTCP int
	// EstablishedTCP bounds the TCP connections a hook accepted that
	// aren't closed yet, the half-open ones that a hook accepted
	// included. A SYN over it is reset with tcp-cap.
	EstablishedTCP int
	// TCPBuffer bounds each accepted connection's send buffer and its
	// receive buffer, in bytes.
	TCPBuffer int
	// QueueFrames and QueueBytes bound the outbound frame queue. A frame
	// over either is dropped with link-queue-full.
	QueueFrames int
	QueueBytes  int
}

// DefaultCaps returns the default caps (S07-02-packet-path, "Per-VM
// caps"). With them the stack's buffer memory is at most 1,024
// connections of 256 KiB each way, 512 MiB.
func DefaultCaps() Caps {
	return Caps{
		HalfOpenTCP:    1024,
		EstablishedTCP: 1024,
		TCPBuffer:      256 << 10,
		QueueFrames:    4096,
		QueueBytes:     16 << 20,
	}
}

// minTCPBuffer is the least TCPBuffer: gVisor's own minimum buffer size.
const minTCPBuffer = 4096

// Validate checks that every cap is at least one, that the TCP buffer is
// at least gVisor's minimum, and that the queue holds a frame of the
// largest size.
func (c Caps) Validate() error {
	if c.HalfOpenTCP < 1 || c.EstablishedTCP < 1 || c.QueueFrames < 1 {
		return fmt.Errorf("netd: caps %+v: each TCP and frame cap must be at least 1", c)
	}
	if c.TCPBuffer < minTCPBuffer {
		return fmt.Errorf("netd: TCP buffer cap %d is below %d", c.TCPBuffer, minTCPBuffer)
	}
	if c.QueueBytes < packetlink.MaxFrame(packetlink.MaxMTU) {
		return fmt.Errorf("netd: frame queue cap %d bytes doesn't hold the largest frame", c.QueueBytes)
	}
	return nil
}
