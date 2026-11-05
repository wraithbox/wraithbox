package hostlink_test

import (
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
)

const (
	vm  = "work"
	gen = 3
)

// netdStart is a NetdStart for vm and gen.
func netdStart() *egressv1.HostdToNetd {
	return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{
		NetdStart: &egressv1.NetdStart{Vm: vm, Generation: gen},
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
