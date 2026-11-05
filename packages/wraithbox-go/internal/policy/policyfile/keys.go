package policyfile

import (
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// level is the closed key list of one level of the file. A key in
// implemented is read. A key in refused is an OpenShell v0.1.2 key that
// Wraith Box doesn't implement, and a key in unsupported a Wraith Box
// extension key whose feature isn't built yet. Every other key is
// unknown. The OpenShell lists are those of `inspect_document` and
// `inspect_endpoint` in `crates/openshell-policy-schema/src/lib.rs` at
// v0.1.2 (S09-01-policy-files, "Pinned release").
type level struct {
	implemented []string
	refused     []string
	unsupported []string
}

var (
	topLevel = level{
		implemented: []string{"version", "network_policies", extensionKey},
		refused:     []string{"filesystem_policy", "landlock", "process", "network_middlewares"},
	}
	ruleLevel     = level{implemented: []string{"name", "endpoints", "binaries"}}
	binaryLevel   = level{implemented: []string{"path"}}
	endpointLevel = level{
		implemented: []string{
			"host", "path", "port", "ports", "protocol", "enforcement", "access", "rules", "deny_rules",
		},
		refused: []string{
			"tls", "allowed_ips", "allow_encoded_slash", "websocket_credential_rewrite",
			"request_body_credential_rewrite", "allow_uninspected_credentials", "persisted_queries",
			"graphql_persisted_queries", "graphql_max_body_bytes", "credential_signing",
			"signing_service", "signing_region", "credential_binding", "json_rpc", "mcp",
		},
	}
	allowWrapLevel = level{implemented: []string{"allow"}}
	httpRuleLevel  = level{
		implemented: []string{"method", "path"},
		refused:     []string{"command", "query", "operation_type", "operation_name", "fields", "tool", "params"},
	}
	// extensionLevel is the `wraithbox` key (S09-01-policy-files,
	// "Extension keys"). The unsupported keys are added by the packages
	// that enforce them: credential bindings (M4.10), dependency-gate
	// thresholds (M5.7) and the kind of a self-hosted git host
	// (S07-08-requests-and-credentials, "Named places").
	extensionLevel = level{
		implemented: []string{"pass", "raw_tcp", "wildcard_budget", "parts_off"},
		unsupported: []string{"credential_bindings", "dependency_gate", "git_hosts"},
	}
	passLevel    = level{implemented: []string{"host", "reason"}}
	rawTCPLevel  = level{implemented: []string{"host", "port", "reason"}}
	partOffLevel = level{implemented: []string{"profile", "kind", "part"}}
)

// extensionKey is the top-level key of the Wraith Box extensions. The
// document the prover gets is the file without it (S09-01-policy-files,
// "Closed keys").
const extensionKey = "wraithbox"

// keyChecker checks every level of a file against its closed key list,
// before any value is read (I489, decision 5). Like OpenShell, it skips
// a level whose value isn't a mapping or sequence. decode refuses that
// value.
type keyChecker struct {
	c   *collector
	src policy.Source
}

// check checks the keys of one mapping.
func (k keyChecker) check(n *yaml.Node, path string, l level) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for key := range pairs(n) {
		name := key.Value
		switch {
		case slices.Contains(l.implemented, name):
			continue
		case slices.Contains(l.refused, name):
			k.c.add(RuleKeyRefused, key.Line, child(path, name), name,
				"Wraith Box doesn't implement this OpenShell key, so it refuses it rather than ignore it")
		case slices.Contains(l.unsupported, name):
			k.c.add(RuleKeyUnsupported, key.Line, child(path, name), name,
				"this Wraith Box extension isn't built yet, so it is refused rather than ignored")
		default:
			k.c.add(RuleKeyUnknown, key.Line, child(path, name), name,
				"OpenShell v0.1.2 has no such key here, and neither has Wraith Box")
		}
	}
}

// top checks the whole file.
func (k keyChecker) top(root *yaml.Node) {
	k.check(root, "", topLevel)
	if ext := lookup(root, extensionKey); ext != nil {
		k.extension(ext)
	}
	rules := lookup(root, "network_policies")
	if rules == nil || rules.Kind != yaml.MappingNode {
		return
	}
	for key, rule := range pairs(rules) {
		path := child("network_policies", key.Value)
		k.check(rule, path, ruleLevel)
		k.each(lookup(rule, "binaries"), child(path, "binaries"), binaryLevel)
		eps := lookup(rule, "endpoints")
		if eps == nil || eps.Kind != yaml.SequenceNode {
			continue
		}
		for i, ep := range eps.Content {
			k.endpoint(ep, index(child(path, "endpoints"), i))
		}
	}
}

func (k keyChecker) endpoint(ep *yaml.Node, path string) {
	k.check(ep, path, endpointLevel)
	if rules := lookup(ep, "rules"); rules != nil && rules.Kind == yaml.SequenceNode {
		for i, r := range rules.Content {
			p := index(child(path, "rules"), i)
			k.check(r, p, allowWrapLevel)
			k.check(lookup(r, "allow"), child(p, "allow"), httpRuleLevel)
		}
	}
	k.each(lookup(ep, "deny_rules"), child(path, "deny_rules"), httpRuleLevel)
}

// extension checks the `wraithbox` key. Repository configuration may
// hold only `version` and `network_policies` (S09-03-project-settings,
// "Closed keys"), so there it is refused whole.
func (k keyChecker) extension(ext *yaml.Node) {
	if k.src.Kind == policy.SourceRepository {
		k.c.add(RuleSource, ext.Line, extensionKey, "",
			"repository configuration can't hold the %s key: it can't add pass hosts, raw TCP entries or a limit (SEC09-host-policy)", extensionKey)
		return
	}
	k.check(ext, extensionKey, extensionLevel)
	k.each(lookup(ext, "pass"), child(extensionKey, "pass"), passLevel)
	k.each(lookup(ext, "raw_tcp"), child(extensionKey, "raw_tcp"), rawTCPLevel)
	k.each(lookup(ext, "parts_off"), child(extensionKey, "parts_off"), partOffLevel)
}

// each checks every entry of a sequence against one level.
func (k keyChecker) each(seq *yaml.Node, path string, l level) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for i, e := range seq.Content {
		k.check(e, index(path, i), l)
	}
}
