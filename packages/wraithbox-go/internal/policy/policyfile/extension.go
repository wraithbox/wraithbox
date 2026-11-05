package policyfile

import (
	"math"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
)

// extension reads the `wraithbox` key (S09-01-policy-files, "Extension
// keys"). keyChecker has refused it in repository configuration, so the
// source here is global or project.
func (d *decoder) extension(ext *yaml.Node) {
	if !d.mappingNode(ext, extensionKey) {
		return
	}
	if v := lookup(ext, "pass"); v != nil {
		d.passHosts(v, child(extensionKey, "pass"))
	}
	if v := lookup(ext, "raw_tcp"); v != nil {
		d.rawTCPs(v, child(extensionKey, "raw_tcp"))
	}
	if v := lookup(ext, "wildcard_budget"); v != nil {
		d.wildcardBudget(v, child(extensionKey, "wildcard_budget"))
	}
	if v := lookup(ext, "parts_off"); v != nil {
		d.parts(v, child(extensionKey, "parts_off"))
	}
}

// exactHost reads the host of a pass or raw TCP entry: one exact host,
// in name form (S07-03-dns, "Name form"), that no built-in profile
// covers (S07-07-hosts-and-modes, "Guarded hosts"). rule is the rule
// of the entry's own refusals.
func (d *decoder) exactHost(n *yaml.Node, path, rule string) (dnsname.Name, bool) {
	v := lookup(n, "host")
	if v == nil {
		d.c.add(rule, n.Line, path, "", "the entry names no host")
		return dnsname.Name{}, false
	}
	s, ok := d.str(v, child(path, "host"))
	if !ok {
		return dnsname.Name{}, false
	}
	if strings.Contains(s, "*") {
		d.c.add(rule, v.Line, child(path, "host"), s, "the entry names a wildcard host: it names one exact host")
		return dnsname.Name{}, false
	}
	h, r := dnsname.FromHost(s)
	if r != nil {
		d.c.add(r.Rule, v.Line, child(path, "host"), s, "%s", strings.TrimPrefix(r.Error(), r.Rule+": "))
		return dnsname.Name{}, false
	}
	if e, covered := builtin.Lookup(h); covered {
		d.c.add(rule, v.Line, child(path, "host"), s,
			"the %s profile covers %s, so it is a guarded host: credential replacement, HTTP policy and the dependency gate stay on it (SEC06-repo-writes)", e.Profile, h)
		return dnsname.Name{}, false
	}
	return h, true
}

// reason reads the reason of a pass or raw TCP entry, which it needs.
func (d *decoder) reason(n *yaml.Node, path, rule string) (string, bool) {
	v := lookup(n, "reason")
	if v == nil {
		d.c.add(rule, n.Line, path, "", "the entry has no reason: each pass host and raw TCP entry says why")
		return "", false
	}
	return d.checkedStr(v, child(path, "reason"), policy.MaxReason)
}

func (d *decoder) passHosts(n *yaml.Node, path string) {
	if !d.sequenceNode(n, path) {
		return
	}
	for i, e := range n.Content {
		p := index(path, i)
		if !d.mappingNode(e, p) {
			continue
		}
		h, hok := d.exactHost(e, p, RulePassHost)
		reason, rok := d.reason(e, p, RulePassHost)
		if hok && rok {
			d.file.PassHosts = append(d.file.PassHosts, policy.PassHost{Host: h, Source: d.file.Source, Reason: reason})
			d.passHostLocs = append(d.passHostLocs, loc{p, e.Line})
		}
	}
}

func (d *decoder) rawTCPs(n *yaml.Node, path string) {
	if !d.sequenceNode(n, path) {
		return
	}
	for i, e := range n.Content {
		p := index(path, i)
		if !d.mappingNode(e, p) {
			continue
		}
		h, hok := d.exactHost(e, p, RuleRawTCP)
		reason, rok := d.reason(e, p, RuleRawTCP)
		pv := lookup(e, "port")
		if pv == nil {
			d.c.add(RulePort, e.Line, p, "", "the entry names no port")
			continue
		}
		port, pok := d.port(pv, child(p, "port"))
		if hok && rok && pok {
			d.file.RawTCP = append(d.file.RawTCP, policy.RawTCP{Host: h, Port: port, Source: d.file.Source, Reason: reason})
			d.rawTCPLocs = append(d.rawTCPLocs, loc{p, e.Line})
		}
	}
}

// wildcardBudget reads the wildcard budget (S07-03-dns, "Wildcard
// budget").
func (d *decoder) wildcardBudget(n *yaml.Node, path string) {
	v, ok := uintValue(n, policy.MaxWildcardBudget)
	if !ok || v > math.MaxUint32 {
		d.c.add(RuleValue, n.Line, path, scalarValue(n), "isn't a decimal integer from 0 to %d", policy.MaxWildcardBudget)
		return
	}
	b := uint32(v)
	d.file.WildcardBudget = &b
}

// parts reads the profile parts the file turns off (S07-11-http-policy,
// "Profile parts"), each checked against the catalog.
func (d *decoder) parts(n *yaml.Node, path string) {
	if !d.sequenceNode(n, path) {
		return
	}
	for i, e := range n.Content {
		p := index(path, i)
		if !d.mappingNode(e, p) {
			continue
		}
		po, ok := d.part(e, p)
		if !ok {
			continue
		}
		if !builtin.KnownPart(builtin.Profile(po.Profile), builtin.Kind(po.Kind), po.Part) {
			d.c.add(policy.RulePart, e.Line, p, po.Profile+"/"+po.Kind+"/"+po.Part,
				"no built-in profile and kind has this part (S07-11-http-policy, \"Profile parts\")")
			continue
		}
		d.file.PartsOff = append(d.file.PartsOff, po)
		d.partOffLocs = append(d.partOffLocs, loc{p, e.Line})
	}
}

// part reads one entry of `parts_off`. `kind` is needed for the git
// hosting profile only, whose parts are per kind.
func (d *decoder) part(n *yaml.Node, path string) (policy.PartOff, bool) {
	po := policy.PartOff{Source: d.file.Source}
	ok := true
	for _, f := range []struct {
		key      string
		into     *string
		optional bool
	}{{"profile", &po.Profile, false}, {"kind", &po.Kind, true}, {"part", &po.Part, false}} {
		v := lookup(n, f.key)
		if v == nil {
			if !f.optional {
				d.c.add(RuleValue, n.Line, child(path, f.key), "", "is missing")
				ok = false
			}
			continue
		}
		s, sok := d.checkedStr(v, child(path, f.key), policy.MaxPartString)
		*f.into = s
		ok = ok && sok
	}
	return po, ok
}
