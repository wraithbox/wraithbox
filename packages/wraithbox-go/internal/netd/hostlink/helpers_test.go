package hostlink_test

import (
	"net"
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
)

const (
	vm  = "work"
	gen = 3
)

// link is the addressing of every NetdStart of these tests.
var link = egressmsg.Link{
	GuestMAC: net.HardwareAddr{2, 0, 0, 0, 0, 2}, GatewayMAC: net.HardwareAddr{2, 0, 0, 0, 0, 1},
	Subnet:  netip.MustParsePrefix("192.168.127.0/24"),
	Gateway: netip.MustParseAddr("192.168.127.1"), Leased: netip.MustParseAddr("192.168.127.2"),
}

// netdStart is a NetdStart for vm, gen and link.
func netdStart() *egressv1.HostdToNetd {
	return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{
		NetdStart: egressmsg.NewNetdStart(vm, gen, link),
	}}
}

func handoffSocket(vmName string, generation uint64) *egressv1.HostdToNetd {
	return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_HandoffSocket{
		HandoffSocket: &egressv1.HandoffSocket{Vm: vmName, Generation: generation},
	}}
}

func policyChange(seq uint64) *egressv1.HostdToNetd {
	return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: vm, Sequence: seq},
	}}
}

func validPolicy() *policyv1.EffectivePolicy {
	return &policyv1.EffectivePolicy{Generation: 1, WildcardBudget: 100}
}
