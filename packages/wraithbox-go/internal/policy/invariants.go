package policy

import (
	"net/url"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
)

// MaxPartString caps a profile, kind or part name.
const MaxPartString = 64

func (p *Policy) checkPartsOff() *Refusal {
	for i, po := range p.PartsOff {
		f := at("parts_off", i)
		// Only the global and a project's network policy turn a part off
		// (S07-11-http-policy, "Profile parts").
		if r := checkSource(f+".source", po.Source, SourceGlobal, SourceProject); r != nil {
			return r
		}
		if r := checkString(f+".profile", po.Profile, MaxPartString); r != nil {
			return r
		}
		if po.Kind != "" {
			if r := checkString(f+".kind", po.Kind, MaxPartString); r != nil {
				return r
			}
		}
		if r := checkString(f+".part", po.Part, MaxPartString); r != nil {
			return r
		}
		if !builtin.KnownPart(builtin.Profile(po.Profile), builtin.Kind(po.Kind), po.Part) {
			return refuse(RulePart, f, "no built-in profile and kind has this part")
		}
	}
	return nil
}

// guardedNames returns the hosts the policy knows are guarded
// (S07-07-hosts-and-modes, "Guarded hosts"): each host the built-in
// catalog covers, and each host a project's repository names. A host
// with a credential binding is guarded too, but bindings aren't in the
// effective policy, so wb-hostd checks those where it adds a binding.
func (p *Policy) guardedNames() map[dnsname.Name]string {
	out := map[dnsname.Name]string{}
	for _, e := range builtin.Entries() {
		out[e.Host] = "the " + string(e.Profile) + " profile covers it"
	}
	for _, pr := range p.Projects {
		for _, repo := range pr.Repositories {
			if n, ok := repositoryHost(repo); ok {
				out[n] = "a repository of project " + pr.ID + " names it"
			}
		}
	}
	return out
}

// repositoryHost returns the host of a compare form
// (gitremote.Result.Compare) of the form `https://host[:port]/path`. A
// compare form without a host, such as `sha256:<hex>`, names none.
func repositoryHost(compare string) (dnsname.Name, bool) {
	if !strings.HasPrefix(compare, "https://") {
		return dnsname.Name{}, false
	}
	u, err := url.Parse(compare)
	if err != nil {
		return dnsname.Name{}, false
	}
	n, r := dnsname.FromHost(u.Hostname())
	return n, r == nil
}

// checkGuarded refuses a pass host, a raw TCP entry or an `audit`
// endpoint on a guarded host. It enforces SEC06-repo-writes and
// SEC07-dep-gate on those hosts: pass would switch off credential
// replacement, HTTP policy and the dependency gate, and `audit` would
// let a refused write through with a credential injected
// (S07-07-hosts-and-modes, "Guarded hosts").
func (p *Policy) checkGuarded() *Refusal {
	guarded := p.guardedNames()
	for i, h := range p.PassHosts {
		if why, ok := guarded[h.Host]; ok {
			return refuse(RuleGuarded, at("pass_hosts", i), "%s is a guarded host (%s), so it can't be a pass host", h.Host, why)
		}
	}
	for i, e := range p.RawTCP {
		if why, ok := guarded[e.Host]; ok {
			return refuse(RuleGuarded, at("raw_tcp", i), "%s is a guarded host (%s), so it can't have a raw TCP entry", e.Host, why)
		}
	}
	for i := range p.Endpoints {
		e := &p.Endpoints[i]
		if e.Enforcement != EnforcementAudit {
			continue
		}
		for n, why := range guarded {
			if e.Host.Match(n) {
				return refuse(RuleGuarded, at("endpoints", i), "the audit endpoint matches %s, a guarded host (%s)", n, why)
			}
		}
	}
	return nil
}

// checkProtocols refuses a host and port that both a raw TCP entry and
// an endpoint name, which would give it two protocols
// (S07-15-non-http-streams, "One protocol per host and port").
func (p *Policy) checkProtocols() *Refusal {
	for i, raw := range p.RawTCP {
		for j := range p.Endpoints {
			if p.Endpoints[j].Names(raw.Host, raw.Port) {
				return refuse(RuleProtocolConflict, at("raw_tcp", i),
					"%s:%d is raw TCP here and HTTP or TLS in endpoints[%d]", raw.Host, raw.Port, j)
			}
		}
	}
	return nil
}
