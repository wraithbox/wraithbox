package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// LeaseTime is the lease the server offers (S07-02-packet-path,
// "Addressing"). The lease never changes, so its length only sets how
// often the guest renews. X03-network-path used one hour.
const LeaseTime = time.Hour

// minReply is the least size of a reply: BOOTP's minimum message of 300
// bytes (RFC 1542, section 3.4), padded after END.
const minReply = 300

// Lease is the one lease of a link: the guest's MAC and address, and
// what the server offers with it.
type Lease struct {
	MAC     net.HardwareAddr
	Addr    netip.Addr
	Gateway netip.Addr
	Subnet  netip.Prefix
	MTU     uint16
}

// LeaseOf is the lease of a link's configuration, which Config.Validate
// has checked.
func LeaseOf(cfg *netd.Config) (Lease, error) {
	if cfg.MTU < 0 || cfg.MTU > 0xffff {
		return Lease{}, fmt.Errorf("dhcp: MTU %d doesn't fit option 26", cfg.MTU)
	}
	return Lease{
		MAC:     cfg.GuestMAC,
		Addr:    cfg.Leased,
		Gateway: cfg.Gateway,
		Subnet:  cfg.Subnet,
		MTU:     uint16(cfg.MTU),
	}, nil
}

// Outcome is what the server does with a request: the reply it sends,
// or none, and the rule it is logged with. Drop marks a request the
// server drops, which is logged as a drop and passed to Events.
type Outcome struct {
	Reply MessageType
	Rule  netd.Rule
	Drop  bool
}

// Decide is the answer table of S07-02-packet-path, "Addressing"
// (SEC05-default-deny). A request from another MAC is dropped with
// dhcp-mac. DISCOVER gets OFFER. A REQUEST gets ACK when it is for the
// leased address, and NAK otherwise. INFORM gets ACK. RELEASE gets no
// answer and the lease stays. DECLINE is dropped as a finding,
// dhcp-decline, and every other type with dhcp-type.
func Decide(req Request, l Lease) Outcome {
	if !slices.Equal(req.CHAddr, l.MAC) {
		return Outcome{Rule: netd.RuleDHCPMAC, Drop: true}
	}
	switch req.Type {
	case MsgDiscover:
		return Outcome{Reply: MsgOffer, Rule: netd.RuleDHCPOffer}
	case MsgRequest:
		if forLease(req, l) {
			return Outcome{Reply: MsgAck, Rule: netd.RuleDHCPAck}
		}
		return Outcome{Reply: MsgNak, Rule: netd.RuleDHCPNak}
	case MsgInform:
		return Outcome{Reply: MsgAck, Rule: netd.RuleDHCPInform}
	case MsgRelease:
		return Outcome{Rule: netd.RuleDHCPRelease}
	case MsgDecline:
		return Outcome{Rule: netd.RuleDHCPDecline, Drop: true}
	case MsgOffer, MsgAck, MsgNak:
		// Messages a server sends, never a client.
		return Outcome{Rule: netd.RuleDHCPType, Drop: true}
	default:
		return Outcome{Rule: netd.RuleDHCPType, Drop: true}
	}
}

// forLease reports whether a REQUEST is for the leased address from this
// server: option 50 names the address, or ciaddr does when option 50 is
// absent, as in a renewal. A server identifier, when present, is the
// gateway's.
func forLease(req Request, l Lease) bool {
	addr := req.Requested
	if !addr.IsValid() {
		addr = req.CIAddr
	}
	return addr == l.Addr && (!req.ServerID.IsValid() || req.ServerID == l.Gateway)
}

// Reply is the reply to a request, and false when Decide gives none.
// The reply holds the message type, the server identifier and, but in a
// NAK, the subnet mask, router, DNS server, lease time and interface
// MTU: nothing else, whatever the parameter request list asks for
// (FR08-no-proxy-config). Only an OFFER and the ACK of a REQUEST carry
// the leased address in yiaddr.
func Reply(req Request, l Lease) ([]byte, bool) {
	out := Decide(req, l)
	if out.Reply == 0 {
		return nil, false
	}
	b := make([]byte, offOpts, minReply)
	b[offOp], b[offHType], b[offHLen] = opReply, htypeEth, hlenEth
	binary.BigEndian.PutUint32(b[offXID:], req.XID)
	binary.BigEndian.PutUint16(b[offFlags:], req.Flags)
	if out.Reply != MsgNak && req.CIAddr == l.Addr {
		put4(b[offCIAddr:], l.Addr)
	}
	if out.Reply == MsgOffer || (out.Reply == MsgAck && req.Type == MsgRequest) {
		put4(b[offYIAddr:], l.Addr)
	}
	copy(b[offCHAddr:], l.MAC)
	copy(b[offCookie:], magicCookie[:])

	b = append(b, optMessageType, 1, byte(out.Reply))
	b = appendAddr(b, optServerID, l.Gateway)
	if out.Reply != MsgNak {
		mask := net.CIDRMask(l.Subnet.Bits(), 32)
		b = append(b, optSubnetMask, 4)
		b = append(b, mask...)
		b = appendAddr(b, optRouter, l.Gateway)
		b = appendAddr(b, optDNS, l.Gateway)
		b = append(b, optLeaseTime, 4)
		b = binary.BigEndian.AppendUint32(b, uint32(LeaseTime/time.Second))
		b = append(b, optMTU, 2)
		b = binary.BigEndian.AppendUint16(b, l.MTU)
	}
	b = append(b, optEnd)
	for len(b) < minReply {
		b = append(b, optPad)
	}
	return b, true
}

func put4(b []byte, a netip.Addr) {
	v := a.As4()
	copy(b, v[:])
}

func appendAddr(b []byte, code byte, a netip.Addr) []byte {
	v := a.As4()
	return append(append(b, code, 4), v[:]...)
}
