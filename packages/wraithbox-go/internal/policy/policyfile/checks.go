package policyfile

import (
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The OAuth token paths of S09-02-precedence: a rule that can allow
// POST to a path starting with oauthPathPrefix on a model API host is
// refused.
const (
	oauthPathPrefix = "/v1/oauth/token"
	oauthMethod     = "POST"
)

// crossChecks runs the checks that look at more than one entry of the
// file, once every entry is read.
func (d *decoder) crossChecks() {
	d.auditGuarded()
	d.protocolConflicts()
	d.builtinLocks()
}

// auditGuarded refuses an `audit` endpoint whose host pattern matches a
// covered host. `audit` would let a refused write through with a
// credential injected (S07-07-hosts-and-modes, "Guarded hosts";
// SEC06-repo-writes). Hosts that a project's remotes name, and hosts
// with a binding, depend on other sources and are checked over the
// union (M3.3c).
func (d *decoder) auditGuarded() {
	for i := range d.file.Endpoints {
		e := &d.file.Endpoints[i]
		if e.Enforcement != policy.EnforcementAudit {
			continue
		}
		for _, b := range builtin.Entries() {
			if e.Host.Match(b.Host) {
				l := d.endpointLocs[i]
				d.c.add(RuleAuditGuarded, l.line, l.path, e.Host.String(),
					"the audit endpoint matches %s, which the %s profile covers: a guarded host is never in audit (SEC06-repo-writes)", b.Host, b.Profile)
				break
			}
		}
	}
}

// protocolConflicts refuses a host and port that a raw TCP entry and an
// endpoint both name (S07-15-non-http-streams, "One protocol per host
// and port").
func (d *decoder) protocolConflicts() {
	for i, raw := range d.file.RawTCP {
		for j := range d.file.Endpoints {
			if d.file.Endpoints[j].Names(raw.Host, raw.Port) {
				l := d.rawTCPLocs[i]
				d.c.add(RuleProtocolConflict, l.line, l.path, raw.Host.String()+":"+strconv.Itoa(int(raw.Port)),
					"the host and port is raw TCP here and TLS or HTTP at %s, line %d", d.endpointLocs[j].path, d.endpointLocs[j].line)
				break
			}
		}
	}
}

// builtinLocks runs the two load checks of S09-02-precedence on each
// allow rule of each endpoint, with access presets and L4 endpoints
// expanded (policy.Endpoint.Allows):
//
//   - the GraphQL lock: no rule on a covered host can match the host's
//     GraphQL path, with any method;
//   - the OAuth token paths: no rule can match POST to a path that
//     starts with /v1/oauth/token on a model API host. It enforces
//     SEC04-no-guest-secrets for the model credential's OAuth tokens.
func (d *decoder) builtinLocks() {
	for i := range d.file.Endpoints {
		e := &d.file.Endpoints[i]
		l := d.endpointLocs[i]
		for _, rule := range e.Allows() {
			d.lockedPaths(e, rule, l)
		}
	}
}

func (d *decoder) lockedPaths(e *policy.Endpoint, rule match.Rule, l loc) {
	for _, b := range builtin.Entries() {
		if b.GraphQLPath != "" && match.CanMatch(rule, b.Host, nil, b.GraphQLPath) {
			d.c.add(RuleGraphQLPath, l.line, l.path, describe(rule),
				"the rule %s can match the GraphQL path %s of %s, which only the built-in profile allows (S09-02-precedence)",
				escape(e.RuleName), b.GraphQLPath, b.Host)
		}
		if b.Profile == builtin.ModelAPI && match.CanMatch(rule, b.Host, []string{oauthMethod}, oauthPathPrefix) {
			d.c.add(RuleModelTokenPath, l.line, l.path, describe(rule),
				"the rule %s in %s can allow POST to %s on %s. Policy can't allow the OAuth token paths, so no login in the guest leaves tokens there: "+
					"the model credential comes from `claude setup-token` on the host and is stored with `wb cred set`",
				escape(e.RuleName), d.c.file, oauthPathPrefix, b.Host)
		}
	}
}

// describe writes a rule as `METHOD host path`, for a refusal.
func describe(r match.Rule) string {
	var b strings.Builder
	b.WriteString(string(r.Method) + " " + r.Host.String())
	if r.EndpointPath != nil {
		b.WriteString(" " + r.EndpointPath.String())
	}
	b.WriteString(" " + r.Path.String())
	return b.String()
}

// modelCheck runs the model's own check (policy.Policy.Check) on the
// file alone, as a second layer: a file that loads is a valid part of
// an effective policy. Its refusal names the entry's line when the
// model names an entry of the file.
func (d *decoder) modelCheck() {
	p := d.file.policy()
	r := p.Check()
	if r == nil {
		return
	}
	l := d.locate(r.Field)
	d.c.add(r.Rule, l.line, l.path, "", "%s", strings.TrimPrefix(r.Error(), r.Field+": "+r.Rule+": "))
}

// locate maps a field of the model, such as `endpoints[3].ports`, to
// where the entry is in the file. A field it can't map is kept as the
// model names it.
func (d *decoder) locate(field string) loc {
	lists := map[string][]loc{
		"endpoints": d.endpointLocs, "pass_hosts": d.passHostLocs,
		"raw_tcp": d.rawTCPLocs, "parts_off": d.partOffLocs,
	}
	name, rest, ok := strings.Cut(field, "[")
	locs := lists[name]
	if !ok || locs == nil {
		return loc{path: field}
	}
	num, after, _ := strings.Cut(rest, "]")
	i, err := strconv.Atoi(num)
	if err != nil || i < 0 || i >= len(locs) {
		return loc{path: field}
	}
	return loc{path: locs[i].path + after, line: locs[i].line}
}
