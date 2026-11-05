package policy

import "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"

// NameAllowance is how the effective policy allows a name, whatever the
// port (S07-03-dns, "Wildcard budget"). The zero NameAllowance is
// NameRefused, so a policy that says nothing refuses.
type NameAllowance int

// The allowances of a name.
const (
	// NameRefused: no endpoint and no raw TCP entry names the host.
	NameRefused NameAllowance = iota
	// NameByExact: an endpoint with the exact host, or a raw TCP entry,
	// names it. Such a name never counts toward the wildcard budget.
	NameByExact
	// NameByWildcardOnly: only a wildcard endpoint names it.
	NameByWildcardOnly
)

// String returns the allowance's name, for log records.
func (a NameAllowance) String() string {
	switch a {
	case NameByExact:
		return "exact"
	case NameByWildcardOnly:
		return "wildcard"
	case NameRefused:
	}
	return "refused"
}

// NameAllowance returns how the policy allows name: by an exact entry,
// only by a wildcard endpoint, or not at all. A raw TCP entry allows its
// host by itself (S07-15-non-http-streams, "Raw TCP entries"). A pass
// host alone doesn't: pass is a mode of a host an endpoint names
// (S07-07-hosts-and-modes, "pass"). A nil policy allows no name.
func (p *Policy) NameAllowance(name dnsname.Name) NameAllowance {
	if p == nil || name.String() == "" {
		return NameRefused
	}
	for _, r := range p.RawTCP {
		if r.Host == name {
			return NameByExact
		}
	}
	wildcard := false
	for i := range p.Endpoints {
		h := p.Endpoints[i].Host
		if !h.Match(name) {
			continue
		}
		if !h.Wildcard() {
			return NameByExact
		}
		wildcard = true
	}
	if wildcard {
		return NameByWildcardOnly
	}
	return NameRefused
}

// PortAllowed reports whether the policy allows port for name: an
// endpoint that names the host has the port, or a raw TCP entry names
// the host and port (S07-02-packet-path, "Connections"). A nil policy
// allows none.
func (p *Policy) PortAllowed(name dnsname.Name, port uint16) bool {
	return p != nil && name.String() != "" && p.Protocol(name, port) != StreamNone
}
