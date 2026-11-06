package egressmsg

import (
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// RuleNameReportMalformed is the rule of a HandoffSocketClosed: wb-proxyd
// closed a VM's hand-off socket for a malformed message from wb-netd
// (S07-06-name-mapping, "Malformed reports").
const RuleNameReportMalformed = "name-report-malformed"

// closedChecks are the checks a HandoffSocketClosed may name: those
// that make a message from wb-netd malformed.
var closedChecks = map[string]bool{
	string(CheckMessageForm):    true,
	string(CheckNameForm):       true,
	string(CheckSyntheticRange): true,
}

// DecodeHostdToProxyd checks one record that wb-proxyd read from its
// control channel, and whether a descriptor came with it, against the
// comments in control.proto. The start, a hand-off socket and a policy
// change each take exactly one descriptor, and the CA messages none.
func DecodeHostdToProxyd(record []byte, hasFD bool) (*egressv1.HostdToProxyd, *Refusal) {
	m := &egressv1.HostdToProxyd{}
	if r := decode(record, m); r != nil {
		return nil, r
	}
	var r *Refusal
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_ProxydStart:
		r = descriptors("proxyd_start", true, hasFD)
	case *egressv1.HostdToProxyd_HandoffSocket:
		r = handoffSocket(k.HandoffSocket, hasFD)
	case *egressv1.HostdToProxyd_PolicyChange:
		r = policyChange(k.PolicyChange, hasFD)
	case *egressv1.HostdToProxyd_CaIssue:
		r = caIssue(k.CaIssue, hasFD)
	case *egressv1.HostdToProxyd_CaSet:
		r = caSet(k.CaSet, hasFD)
	case *egressv1.HostdToProxyd_CaAccept:
		r = caAccept(k.CaAccept, hasFD)
	case *egressv1.HostdToProxyd_CaRemove:
		r = caRemove(k.CaRemove, hasFD)
	default:
		r = refuse(CheckMessageForm, "kind", "no kind set")
	}
	if r != nil {
		return nil, r
	}
	return m, nil
}

// DecodeHostdToNetd checks one record that a wb-netd read from its
// control channel, and whether a descriptor came with it. Each kind
// takes exactly one descriptor. Whether the VM and generation are the
// wb-netd's own is its check.
func DecodeHostdToNetd(record []byte, hasFD bool) (*egressv1.HostdToNetd, *Refusal) {
	m := &egressv1.HostdToNetd{}
	if r := decode(record, m); r != nil {
		return nil, r
	}
	var r *Refusal
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToNetd_NetdStart:
		r = netdStart(k.NetdStart, hasFD)
	case *egressv1.HostdToNetd_HandoffSocket:
		r = handoffSocket(k.HandoffSocket, hasFD)
	case *egressv1.HostdToNetd_PolicyChange:
		r = policyChange(k.PolicyChange, hasFD)
	default:
		r = refuse(CheckMessageForm, "kind", "no kind set")
	}
	if r != nil {
		return nil, r
	}
	return m, nil
}

// DecodeProxydToHostd checks one record that wb-hostd read from
// wb-proxyd's control channel, and whether a descriptor came with it.
// No kind takes a descriptor. Whether the VM, generation and sequence
// match what wb-hostd sent is wb-hostd's check, and so is whether a CA
// reply answers a CA message wb-hostd sent (internal/hostd/carecord).
func DecodeProxydToHostd(record []byte, hasFD bool) (*egressv1.ProxydToHostd, *Refusal) {
	m := &egressv1.ProxydToHostd{}
	if r := decode(record, m); r != nil {
		return nil, r
	}
	var r *Refusal
	switch k := m.GetKind().(type) {
	case *egressv1.ProxydToHostd_HandoffSocketAck:
		r = vmGeneration("handoff_socket_ack", k.HandoffSocketAck.GetVm(), k.HandoffSocketAck.GetGeneration(), false, hasFD)
	case *egressv1.ProxydToHostd_PolicyAck:
		r = policyAck(k.PolicyAck, hasFD)
	case *egressv1.ProxydToHostd_HandoffSocketClosed:
		r = handoffSocketClosed(k.HandoffSocketClosed, hasFD)
	case *egressv1.ProxydToHostd_CaIssued:
		r = caIssued(k.CaIssued, hasFD)
	case *egressv1.ProxydToHostd_CaLoaded:
		r = caLoaded(k.CaLoaded, hasFD)
	case *egressv1.ProxydToHostd_CaRestoreFailed:
		r = caRestoreFailed(k.CaRestoreFailed, hasFD)
	case *egressv1.ProxydToHostd_CaRemoved:
		r = caRemoved(k.CaRemoved, hasFD)
	default:
		r = refuse(CheckMessageForm, "kind", "no kind set")
	}
	if r != nil {
		return nil, r
	}
	return m, nil
}

// DecodeNetdToHostd checks one record that wb-hostd read from a
// wb-netd's control channel, and whether a descriptor came with it.
func DecodeNetdToHostd(record []byte, hasFD bool) (*egressv1.NetdToHostd, *Refusal) {
	m := &egressv1.NetdToHostd{}
	if r := decode(record, m); r != nil {
		return nil, r
	}
	ack := m.GetPolicyAck()
	if ack == nil {
		return nil, refuse(CheckMessageForm, "kind", "no kind set")
	}
	if r := policyAck(ack, hasFD); r != nil {
		return nil, r
	}
	return m, nil
}

func handoffSocket(m *egressv1.HandoffSocket, hasFD bool) *Refusal {
	return vmGeneration("handoff_socket", m.GetVm(), m.GetGeneration(), true, hasFD)
}

func netdStart(m *egressv1.NetdStart, hasFD bool) *Refusal {
	return vmGeneration("netd_start", m.GetVm(), m.GetGeneration(), true, hasFD)
}

func policyChange(m *egressv1.PolicyChange, hasFD bool) *Refusal {
	return vmSequence("policy_change", m.GetVm(), m.GetSequence(), true, hasFD)
}

func policyAck(m *egressv1.PolicyAck, hasFD bool) *Refusal {
	return vmSequence("policy_ack", m.GetVm(), m.GetSequence(), false, hasFD)
}

func handoffSocketClosed(m *egressv1.HandoffSocketClosed, hasFD bool) *Refusal {
	if r := vmGeneration("handoff_socket_closed", m.GetVm(), m.GetGeneration(), false, hasFD); r != nil {
		return r
	}
	if m.GetRule() != RuleNameReportMalformed {
		return refuse(CheckMessageForm, "handoff_socket_closed.rule", "isn't "+RuleNameReportMalformed)
	}
	if !closedChecks[m.GetCheck()] {
		return refuse(CheckMessageForm, "handoff_socket_closed.check", "isn't a check that makes a message malformed")
	}
	return nil
}

// vmGeneration checks the fields a message with a VM and a generation
// shares, and its descriptor count.
func vmGeneration(kind, vm string, generation uint64, wantFD, hasFD bool) *Refusal {
	if r := descriptors(kind, wantFD, hasFD); r != nil {
		return r
	}
	if _, r := VM(kind+".vm", vm); r != nil {
		return r
	}
	return atLeastOne(kind+".generation", generation)
}

// vmSequence checks the fields a message with a VM and a sequence
// number shares, and its descriptor count.
func vmSequence(kind, vm string, sequence uint64, wantFD, hasFD bool) *Refusal {
	if r := descriptors(kind, wantFD, hasFD); r != nil {
		return r
	}
	if _, r := VM(kind+".vm", vm); r != nil {
		return r
	}
	return atLeastOne(kind+".sequence", sequence)
}

// StartOrder holds a daemon's check of the order of its control
// channel: the start message first, and once (control.proto,
// S04-architecture "Control channels"). The zero StartOrder expects the
// start message. Once it refuses a message it refuses every later one,
// since the daemon ends the channel. It isn't safe for concurrent use:
// one reader decides the channel's messages in order.
type StartOrder struct {
	started bool
	refused bool
}

// NextNetd checks the order of the next message a wb-netd decoded from
// its control channel, with DecodeHostdToNetd. NetdStart is its start
// message.
func (o *StartOrder) NextNetd(m *egressv1.HostdToNetd) *Refusal {
	return o.next(m.GetNetdStart() != nil)
}

// NextProxyd checks the order of the next message wb-proxyd decoded
// from its control channel, with DecodeHostdToProxyd. ProxydStart is
// its start message.
func (o *StartOrder) NextProxyd(m *egressv1.HostdToProxyd) *Refusal {
	return o.next(m.GetProxydStart() != nil)
}

// next refuses, with CheckMessageForm, a message before the start, a
// second start, and every message after a refusal.
func (o *StartOrder) next(isStart bool) *Refusal {
	var r *Refusal
	switch {
	case o.refused:
		r = refuse(CheckMessageForm, "kind", "a message after a refused one")
	case isStart && o.started:
		r = refuse(CheckMessageForm, "kind", "a second start message")
	case !isStart && !o.started:
		r = refuse(CheckMessageForm, "kind", "a message before the start message")
	}
	if r != nil {
		o.refused = true
		return r
	}
	o.started = true
	return nil
}
