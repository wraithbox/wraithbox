// Package httppolicy decides each request on an inspected host by the
// VM's HTTP policy (S07-11-http-policy), in the order of
// S07-19-request-decisions. It is step 4 of
// S07-08-requests-and-credentials, "Order of the steps": wb-proxyd
// calls Engine.Decide after it has parsed the request, and before the
// dependency gate and credential injection.
//
// The order of evaluation:
//
//  1. the form of the method and the path (`http-method-form`,
//     `http-path-form`);
//  2. the built-in denies, whatever any rule allows and in every mode
//     (S09-02-precedence, "Network rules merge as a union"):
//     `model-path-escape`, `git-archive`, `git-create-denied`,
//     `model-event-log`, `registry-token`, `registry-publish` and
//     `git-write-outside-project-set` (SEC06-repo-writes);
//  3. `profile-part-off`: a request that the rule of a part that is
//     off would allow;
//  4. `graphql-operation`: every request to a built-in host's GraphQL
//     path, until the operation filter (M3.7b) lands;
//  5. the `deny_rules` of every source (`http-deny-rule`);
//  6. the allow rules: the built-in rules of the parts that are on and
//     every source's rules. No match is `http-no-rule`
//     (SEC05-default-deny).
//
// On a host and port in `audit`, only `http-deny-rule` and
// `http-no-rule` are relaxed: the request goes on with the rule
// `audit-allowed`, which names the rule it broke (S07-16-tls-and-tunnels,
// "Not relaxed by audit mode or approvals"). Every decision is logged
// once with its rule and source (SEC10-audit).
//
// Every input is treated as adversarial: the request comes from the
// guest. Host and path rules match with package match, the matcher
// wb-hostd checks policy with, so a rule means the same in both.
package httppolicy

import (
	"errors"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The rules of the decisions of this package (S07-11-http-policy,
// "Rule names"). The built-in denies of the catalog keep the rules of
// package builtin.
const (
	// RuleAllowed: a rule allows the request.
	RuleAllowed = "http-allowed"
	// RuleNoRule: no rule allows the request (SEC05-default-deny).
	RuleNoRule = "http-no-rule"
	// RuleDenyRule: a `deny_rules` entry of a source matches.
	RuleDenyRule = "http-deny-rule"
	// RuleAuditAllowed: a request that broke RuleNoRule or RuleDenyRule
	// on a host and port in `audit` goes on.
	RuleAuditAllowed = "audit-allowed"
	// RulePartOff: the rule of a part that is off would allow the
	// request.
	RulePartOff = "profile-part-off"
	// RuleGraphQL: a request to a built-in host's GraphQL path. Until
	// the operation filter (M3.7b) lands, every one is refused.
	RuleGraphQL = "graphql-operation"
	// RuleWriteOutside: a write on a git hosting host whose repository
	// isn't in the project set, or can't be read from the path
	// (SEC06-repo-writes).
	RuleWriteOutside = "git-write-outside-project-set"
	// RuleRegistryPublish: a method other than GET, HEAD and OPTIONS on
	// a package registry host.
	RuleRegistryPublish = "registry-publish"
	// RuleModelPathEscape: a percent-escape in the path on a model API
	// host.
	RuleModelPathEscape = "model-path-escape"
	// RuleMethodForm: a method that isn't 1 to 32 RFC 9110 token
	// characters in upper case.
	RuleMethodForm = "http-method-form"
	// RulePathForm: a path CanonicalPath refuses
	// (S07-08-requests-and-credentials, "Request form").
	RulePathForm = match.RulePathForm
)

// Request is a request as wb-proxyd parsed it.
type Request struct {
	Method string
	// Host is the stream's host name, which the request's `Host` or
	// `:authority` equals (S07-08-requests-and-credentials, "Request
	// form").
	Host dnsname.Name
	Port uint16
	// Path is the path as received, before canonicalization, without
	// the query. Decide puts it in canonical form itself, and reads the
	// percent-escapes of the raw form for `model-path-escape`.
	Path     string
	RawQuery string
	// WebSocket marks an upgrade to WebSocket, which matches as a GET
	// on its path.
	WebSocket bool
}

// Source is where the rule that decided comes from: a built-in
// profile's part, or a policy source and its rule name.
type Source struct {
	Builtin  bool
	Profile  builtin.Profile
	Kind     builtin.Kind
	Part     string
	Policy   policy.Source
	RuleName string
}

// Decision is the answer to one request.
type Decision struct {
	Allow bool
	// Rule is the rule of the decision: RuleAllowed, RuleAuditAllowed,
	// or the rule of the refusal.
	Rule string
	// Broke is, for RuleAuditAllowed, the rule the request broke.
	Broke string
	// Source is the rule that allowed or refused, when one did: a
	// built-in part or a policy rule. A built-in deny has none.
	Source Source
	// OffBy is, for RulePartOff, every source that turned the part off.
	OffBy      []policy.Source
	Mode       policy.HostMode
	Generation uint64
	// Method is the request's method, and "" when it failed its check.
	Method string
	Host   dnsname.Name
	Port   uint16
	// Path is the canonical path, and "" when it failed its check.
	Path string
	// Detail says why a request form check failed. It holds offsets,
	// never the request's bytes.
	Detail string
}

// Engine decides requests by one generation of a VM's policy. It is
// immutable and safe for concurrent use. wb-proxyd builds a new one for
// each generation and swaps it whole, so a request sees one generation.
type Engine struct {
	vm         string
	generation uint64
	log        *slog.Logger
	policy     *policy.Policy
	endpoints  []endpoint
	hosts      map[dnsname.Name]*hostRules
	partsOff   map[partKey][]policy.Source
}

// endpoint is a policy endpoint with its rules as the matcher sees
// them.
type endpoint struct {
	src   Source
	e     *policy.Endpoint
	allow []match.Rule
	deny  []match.Rule
}

type partKey struct {
	profile builtin.Profile
	kind    builtin.Kind
	part    string
}

// New builds the engine of a policy for the VM vm. It checks the
// policy again (policy.Check), and refuses one that breaks a bound or
// invariant. log is nil for slog.Default.
func New(p *policy.Policy, vm string, log *slog.Logger) (*Engine, error) {
	if p == nil {
		return nil, errors.New("httppolicy: no policy")
	}
	if r := p.Check(); r != nil {
		return nil, r
	}
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		vm:         vm,
		generation: p.Generation,
		log:        log,
		policy:     p,
		partsOff:   map[partKey][]policy.Source{},
	}
	for i := range p.Endpoints {
		pe := &p.Endpoints[i]
		ep := endpoint{src: Source{Policy: pe.Source, RuleName: pe.RuleName}, e: pe, allow: pe.Allows()}
		for _, r := range pe.DenyRules {
			ep.deny = append(ep.deny, match.Rule{Host: pe.Host, EndpointPath: pe.Path, Method: r.Method, Path: r.Path})
		}
		e.endpoints = append(e.endpoints, ep)
	}
	for _, po := range p.PartsOff {
		k := partKey{builtin.Profile(po.Profile), builtin.Kind(po.Kind), po.Part}
		e.partsOff[k] = append(e.partsOff[k], po.Source)
	}
	e.hosts = buildHosts(p.Projects)
	return e, nil
}

// Generation returns the generation of the policy the engine decides
// by.
func (e *Engine) Generation() uint64 { return e.generation }
