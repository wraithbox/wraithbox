package httppolicy

import (
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// call is one request under decision, after its form checks.
type call struct {
	req    Request
	method string
	path   string
	host   *hostRules
}

// Decide decides one request, logs the decision with its rule and
// source (SEC10-audit), and returns it. A decision that doesn't allow
// is answered with a 403 whose body is Decision.RefusalBody.
func (e *Engine) Decide(req Request) Decision {
	d := e.decide(req)
	e.logDecision(d)
	return d
}

func (e *Engine) decide(req Request) Decision {
	d := Decision{Generation: e.generation, Host: req.Host, Port: req.Port}
	method, ok := checkMethod(req.Method)
	if !ok {
		return refused(d, RuleMethodForm, "the method is not 1 to 32 token characters in upper case")
	}
	d.Method = method
	path, r := match.CanonicalPath(req.Path)
	if r != nil {
		// The refusal's message holds offsets, never the path's bytes.
		return refused(d, RulePathForm, strings.TrimPrefix(r.Error(), r.Rule+": "))
	}
	d.Path = path
	if req.WebSocket {
		// An upgrade matches as a GET on its path (S07-11-http-policy,
		// "HTTP policy"). Message rules are refused at load.
		method = "GET"
	}
	c := call{req: req, method: method, path: path, host: e.hosts[req.Host]}
	d.Mode = e.mode(c)
	if rule, ok := c.builtinDeny(); ok {
		return refused(d, rule, "")
	}
	if a, offBy, ok := e.partOff(c); ok {
		d.Source = builtinSource(a)
		d.OffBy = offBy
		return refused(d, RulePartOff, "")
	}
	if c.host != nil && c.host.isGraphQL(path) {
		return refused(d, RuleGraphQL, "the GraphQL operation filter is not in place yet, so every request to this path is refused")
	}
	if src, ok := e.denyRule(c); ok {
		d.Source = src
		return violation(d, RuleDenyRule)
	}
	if src, ok := e.allowRule(c); ok {
		d.Allow, d.Rule, d.Source = true, RuleAllowed, src
		return d
	}
	return violation(d, RuleNoRule)
}

func refused(d Decision, rule, detail string) Decision {
	d.Allow, d.Rule, d.Detail = false, rule, detail
	return d
}

// violation refuses a request that broke RuleNoRule or RuleDenyRule,
// or lets it through on a host and port in `audit`. These two are the
// only refusals `audit` relaxes (S07-16-tls-and-tunnels, "Not relaxed by
// audit mode or approvals").
func violation(d Decision, rule string) Decision {
	if d.Mode != policy.ModeAudit {
		return refused(d, rule, "")
	}
	d.Allow, d.Rule, d.Broke = true, RuleAuditAllowed, rule
	return d
}

// checkMethod accepts 1 to match.MaxMethod RFC 9110 token characters
// with no lowercase letter and no `*`. A method is case-sensitive, and
// rules compare it without regard to case (match.Method), so a
// lowercase method could match a rule and reach a server as another
// method.
func checkMethod(s string) (string, bool) {
	if s == match.AnyMethod {
		return "", false
	}
	m, r := match.CompileMethod(s)
	if r != nil || string(m) != s {
		return "", false
	}
	return s, true
}

// mode is the mode of the request's host and port, resolved over the
// union (S09-02-precedence, "Mode per host and port"). A guarded host
// is never in `audit` (S07-07-hosts-and-modes, "audit"). Check refuses
// a policy that puts one there, and the engine holds it in `enforce`
// too, so that no built-in host is ever relaxed.
func (e *Engine) mode(c call) policy.HostMode {
	if c.host != nil {
		return policy.ModeEnforce
	}
	return policy.Mode(e.policy.Endpoints, c.req.Host, c.req.Port)
}

// builtinDeny returns the rule of the first built-in deny the request
// meets. These hold in every mode and whatever any rule allows
// (S09-02-precedence, "Network rules merge as a union").
func (c call) builtinDeny() (string, bool) {
	h := c.host
	if h == nil {
		return "", false
	}
	// S07-11-http-policy, "model API", "No percent-escapes": on the raw
	// path, before canonicalization decodes any escape.
	if h.profile == builtin.ModelAPI && strings.Contains(c.req.Path, "%") {
		return RuleModelPathEscape, true
	}
	// The denies match the path in lowercase, so a server that routes
	// without regard to case can't be reached past them.
	lower := strings.ToLower(c.path)
	for _, d := range h.denies {
		if d.Rule.Match(c.req.Host, c.method, lower) {
			return d.Name, true
		}
	}
	// S07-11-http-policy, "package registries": metadata and downloads
	// only.
	if h.profile == builtin.PackageRegistries && !isRead(c.method) {
		return RuleRegistryPublish, true
	}
	// SEC06-repo-writes: writes only to the project set.
	if h.profile == builtin.GitHosting && h.isWrite(c.method, c.path, c.req.RawQuery) && !h.inProjectSet(c.path) {
		return RuleWriteOutside, true
	}
	return "", false
}

// partOff puts the request in the part of the first built-in rule that
// matches it, and reports that rule and the sources that turned its
// part off, when any did (S07-11-http-policy, "Profile parts",
// "Effect"). The request is then refused whatever other rule allows it.
func (e *Engine) partOff(c call) (builtin.Allow, []policy.Source, bool) {
	a, ok := c.firstBuiltin()
	if !ok {
		return builtin.Allow{}, nil, false
	}
	offBy := e.offBy(a)
	return a, offBy, len(offBy) > 0
}

// firstBuiltin returns the first built-in allow rule of the host that
// matches the request on a port the rules apply on.
func (c call) firstBuiltin() (builtin.Allow, bool) {
	h := c.host
	if h == nil || !slices.Contains(h.ports, c.req.Port) {
		return builtin.Allow{}, false
	}
	for _, a := range h.allows {
		if a.Rule.Match(c.req.Host, c.method, c.path) {
			return a, true
		}
	}
	return builtin.Allow{}, false
}

// offBy returns the sources that turned a rule's part off. A remote
// host's kind isn't known, so its `git` part is off when the `git` part
// of any kind is, which fails closed.
func (e *Engine) offBy(a builtin.Allow) []policy.Source {
	if a.Profile != builtin.GitHosting || a.Kind != builtin.NoKind {
		return e.partsOff[partKey{a.Profile, a.Kind, a.Part}]
	}
	var out []policy.Source
	for _, k := range []builtin.Kind{builtin.GitHub, builtin.GitLab, builtin.Gitea} {
		out = append(out, e.partsOff[partKey{a.Profile, k, a.Part}]...)
	}
	return out
}

// denyRule returns the source of the first `deny_rules` entry of an
// endpoint that names the host and port and matches the request.
func (e *Engine) denyRule(c call) (Source, bool) {
	for i := range e.endpoints {
		ep := &e.endpoints[i]
		if !ep.e.Names(c.req.Host, c.req.Port) {
			continue
		}
		for _, r := range ep.deny {
			if r.Match(c.req.Host, c.method, c.path) {
				return ep.src, true
			}
		}
	}
	return Source{}, false
}

// allowRule returns the source of a rule that allows the request: a
// built-in rule of a part that is on, or a rule of an endpoint that
// names the host and port.
func (e *Engine) allowRule(c call) (Source, bool) {
	// partOff ran first, so the first built-in rule's part is on.
	if a, ok := c.firstBuiltin(); ok {
		return builtinSource(a), true
	}
	for i := range e.endpoints {
		ep := &e.endpoints[i]
		if !ep.e.Names(c.req.Host, c.req.Port) {
			continue
		}
		for _, r := range ep.allow {
			if r.Match(c.req.Host, c.method, c.path) {
				return ep.src, true
			}
		}
	}
	return Source{}, false
}

func builtinSource(a builtin.Allow) Source {
	return Source{Builtin: true, Profile: a.Profile, Kind: a.Kind, Part: a.Part}
}
