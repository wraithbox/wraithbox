package main

import (
	"log/slog"
	"strconv"
	"sync/atomic"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// policies holds the VM's current effective policy, which the control
// channel replaces at each policy change, and answers the DNS server's
// and the stream policy's questions from it. Until the first change it
// holds none, and allows no name and no port (SEC05-default-deny).
type policies struct {
	p atomic.Pointer[policy.Policy]
}

// ApplyPolicy makes p the current policy (hostlink.PolicyTarget).
func (h *policies) ApplyPolicy(p *policy.Policy) { h.p.Store(p) }

// Class returns the class of name in the current policy, with the
// policy's generation as the rule it came from (netd.NamePolicy).
// A name the policy doesn't allow is NameNotAllowed.
func (h *policies) Class(name dnsname.Name) (netd.NameClass, string) {
	p := h.p.Load()
	if p == nil {
		return netd.NameNotAllowed, "no-policy"
	}
	from := "effective-policy-" + strconv.FormatUint(p.Generation, 10)
	switch p.NameAllowance(name) {
	case policy.NameByExact:
		return netd.NameExact, from
	case policy.NameByWildcardOnly:
		return netd.NameWildcard, from
	case policy.NameRefused:
		return netd.NameNotAllowed, from
	}
	return netd.NameNotAllowed, from
}

// WildcardBudget returns the current policy's wildcard budget, and 0
// before the first policy (netd.NamePolicy).
func (h *policies) WildcardBudget() int {
	p := h.p.Load()
	if p == nil {
		return 0
	}
	return int(p.WildcardBudget)
}

// PortAllowed reports whether the current policy allows port for name
// (netd.PortPolicy). policy.Policy.PortAllowed allows none on nil.
func (h *policies) PortAllowed(name dnsname.Name, port uint16) bool {
	return h.p.Load().PortAllowed(name, port)
}

// runEvents counts each drop, reset and refusal of the stack, the DNS
// server and the pool in a run of the event channel, as a network
// activity event of the VM (S07-02-packet-path, "Events and rate
// limits"; S04-08-records-and-bounds, "Event records").
type runEvents struct {
	s  *eventchan.Sender
	vm eventchan.VM
}

// Event counts one event (netd.Events). The record carries no guest
// field: the attributes go to the rate-limited log only.
func (e runEvents) Event(d netd.Decision, rule netd.Rule, _ uint64, _ []slog.Attr) {
	e.s.CountRun(e.vm, auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY, string(d), string(rule))
}

// hostState refuses new lookups and connections while the event channel
// refuses new network activity for the VM: over its audit budget, or
// with a blocked channel (S09-11-audit, netd.HostState).
type hostState struct {
	s  *eventchan.Sender
	vm eventchan.VM
}

// Refusal returns the event channel's rule, or "".
func (h hostState) Refusal() netd.Rule {
	return netd.Rule(h.s.Admit(h.vm, auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY))
}
