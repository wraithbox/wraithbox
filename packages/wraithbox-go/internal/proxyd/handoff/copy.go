package handoff

import (
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// mapping is wb-proxyd's copy of one VM's name mapping
// (S07-06-name-mapping), and its count of the names accepted only under
// a wildcard entry in the VM's active period ("Wildcard budget, second
// layer"). Only checked reports change it, so it holds no address
// outside egressmsg.SyntheticRange, no name outside the canonical form,
// and no address for two names. Its size is bounded by the range: at
// most egressmsg.MaxEntries addresses (SEC13-bounded-resources).
//
// Its methods don't lock. The VM's lock guards it.
type mapping struct {
	byAddr map[netip.Addr]dnsname.Name
	// wildcard holds the names counted toward the budget, from
	// ProxydStart and from the reports accepted since.
	wildcard map[dnsname.Name]struct{}
}

func newMapping() *mapping {
	return &mapping{byAddr: map[netip.Addr]dnsname.Name{}, wildcard: map[dnsname.Name]struct{}{}}
}

// lookup returns the name the copy holds for addr.
func (m *mapping) lookup(addr netip.Addr) (dnsname.Name, bool) {
	n, ok := m.byAddr[addr]
	return n, ok
}

// reportAction is what decideReport says to do with a name report.
type reportAction int

// The actions.
const (
	// reportRefuse: drop the report, logged with its check.
	reportRefuse reportAction = iota
	// reportAdd: add the entry.
	reportAdd
	// reportAddWildcard: write the name to wb-hostd as
	// ProxydWildcardName first, and add the entry and count the name
	// once the write succeeds.
	reportAddWildcard
	// reportRepeat: the copy already holds the entry; nothing changes.
	reportRepeat
	// reportConflict: the copy holds the address for another name.
	// Remove that entry too, and close every open stream to it.
	reportConflict
)

// decideReport decides a name report that egressmsg.DecodeNetdMessage
// accepted, so its name is in canonical form and its address in the
// synthetic range. The checks run in the order of S07-06-name-mapping,
// "Name reports": allowed by the effective policy, then the address
// conflict, and last the wildcard budget, so wb-proxyd writes no name
// to wb-hostd that it would refuse for another reason (I179).
func (m *mapping) decideReport(p *policy.Policy, name dnsname.Name, addr netip.Addr) (reportAction, egressmsg.Check) {
	allowance := p.NameAllowance(name)
	if allowance == policy.NameRefused {
		return reportRefuse, CheckNotAllowed
	}
	if held, ok := m.byAddr[addr]; ok {
		if held == name {
			return reportRepeat, ""
		}
		return reportConflict, CheckAddressConflict
	}
	if allowance != policy.NameByWildcardOnly {
		return reportAdd, ""
	}
	if _, counted := m.wildcard[name]; counted {
		return reportAdd, ""
	}
	if len(m.wildcard) >= int(p.WildcardBudget) {
		return reportRefuse, CheckWildcardBudget
	}
	return reportAddWildcard, ""
}

// apply carries out a decision of decideReport, other than a refusal.
// For reportAddWildcard the caller calls it only after the write
// succeeded.
func (m *mapping) apply(a reportAction, name dnsname.Name, addr netip.Addr) {
	switch a {
	case reportAddWildcard:
		m.wildcard[name] = struct{}{}
		m.byAddr[addr] = name
	case reportAdd:
		m.byAddr[addr] = name
	case reportConflict:
		delete(m.byAddr, addr)
	case reportRefuse, reportRepeat:
	}
}

// release removes the entry of addr, and reports whether the copy held
// one (S07-06-name-mapping, "Releases").
func (m *mapping) release(addr netip.Addr) bool {
	_, ok := m.byAddr[addr]
	delete(m.byAddr, addr)
	return ok
}

// newPeriod clears the wildcard names at the start of a new active
// period. The entries stay.
func (m *mapping) newPeriod() {
	m.wildcard = map[dnsname.Name]struct{}{}
}

// countWildcard adds the names wb-hostd kept for the VM's active period,
// from ProxydStart. egressmsg.CheckProxydStart checked their form.
func (m *mapping) countWildcard(names []dnsname.Name) {
	for _, n := range names {
		m.wildcard[n] = struct{}{}
	}
}

// streamFacts are what the I/O layer knows of a stream message besides
// its fields.
type streamFacts struct {
	// socketType: the descriptor is an AF_UNIX SOCK_STREAM socket.
	socketType bool
	// generation is the generation of the socket the message came on.
	generation uint64
	// open is the number of streams open from that socket.
	open int
	// maxStreams is Caps.Streams.
	maxStreams int
}

// decideStream checks a stream message that egressmsg.DecodeNetdMessage
// accepted, in the order of S07-05-stream-hand-off, "Streams", and
// returns the name the copy holds for its address. A refusal returns
// the check that failed. The name is never taken from the message: a
// stream message has none (SEC08-proj-isolation).
func decideStream(m *mapping, p *policy.Policy, msg egressmsg.NetdMessage, f streamFacts) (dnsname.Name, egressmsg.Check) {
	if !f.socketType {
		return dnsname.Name{}, CheckSocketType
	}
	if msg.Generation != f.generation {
		return dnsname.Name{}, CheckGeneration
	}
	name, ok := m.lookup(msg.Addr)
	if !ok {
		return dnsname.Name{}, CheckNoEntry
	}
	if check := streamAllowed(p, name, msg.Port); check != "" {
		return dnsname.Name{}, check
	}
	if f.open >= f.maxStreams {
		return dnsname.Name{}, CheckStreamCap
	}
	return name, ""
}

// streamAllowed checks a name and port against the effective policy: at
// hand-off, and for each open stream after a recompute
// (S07-01-per-vm-policy, "Open streams").
func streamAllowed(p *policy.Policy, name dnsname.Name, port uint16) egressmsg.Check {
	if p.NameAllowance(name) == policy.NameRefused {
		return CheckNotAllowed
	}
	if !p.PortAllowed(name, port) {
		return CheckPortNotAllowed
	}
	return ""
}
