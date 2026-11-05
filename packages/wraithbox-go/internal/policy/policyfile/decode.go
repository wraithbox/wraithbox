package policyfile

import (
	"math"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/builtin"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// universalBinary is the one `binaries` entry a rule may have until
// X14-flow-attribution delivers program labels (S09-01-policy-files,
// "Program narrowing").
const universalBinary = "/**"

// loc is where an entry of the file is: its key path and line.
type loc struct {
	path string
	line int
}

// decoder reads the values of a file whose bounds, form and keys are
// checked, into File. It goes on after a refusal, so one load reports
// the first refusal of each rule (I489, decision 10).
type decoder struct {
	c    *collector
	file *File
	// names maps each rule name to the line of its rule.
	names map[string]int
	// The locations of the entries in file, by index, so a refusal of
	// the model's check names a line.
	endpointLocs, passHostLocs, rawTCPLocs, partOffLocs []loc
}

func (d *decoder) top(root *yaml.Node) {
	if !d.mappingNode(root, "") {
		return
	}
	d.version(lookup(root, "version"), root.Line)
	if rules := lookup(root, "network_policies"); rules != nil && d.mappingNode(rules, "network_policies") {
		for key, rule := range pairs(rules) {
			d.rule(key, rule)
		}
	}
	if ext := lookup(root, extensionKey); ext != nil {
		d.extension(ext)
	}
}

// version refuses a file without `version: 1`, as OpenShell does.
func (d *decoder) version(n *yaml.Node, line int) {
	if n == nil {
		d.c.add(RuleVersion, line, "version", "", "is missing: a policy file starts with version: 1")
		return
	}
	if v, ok := uintValue(n, 1<<16); !ok || v != 1 {
		d.c.add(RuleVersion, n.Line, "version", scalarValue(n), "isn't 1, the only version of the schema")
	}
}

// rule reads one entry of `network_policies`. Its name is OpenShell's
// effective name: `name` when the rule has one, and its key otherwise.
func (d *decoder) rule(key, rule *yaml.Node) {
	path := child("network_policies", key.Value)
	keyName, ok := d.checkedStr(key, path, policy.MaxRuleName)
	if !ok || !d.mappingNode(rule, path) {
		return
	}
	name := keyName
	if n := lookup(rule, "name"); n != nil {
		if name, ok = d.checkedStr(n, child(path, "name"), policy.MaxRuleName); !ok {
			return
		}
	}
	d.ruleName(name, keyName, path, key.Line)
	d.binaries(lookup(rule, "binaries"), path, key.Line)
	eps := lookup(rule, "endpoints")
	if eps == nil || !d.sequenceNode(eps, child(path, "endpoints")) {
		return
	}
	for i, ep := range eps.Content {
		p := index(child(path, "endpoints"), i)
		if e, ok := d.endpoint(ep, p, name); ok {
			d.file.Endpoints = append(d.file.Endpoints, e)
			d.endpointLocs = append(d.endpointLocs, loc{p, ep.Line})
		}
	}
}

// ruleName refuses a name used twice in the file, and the name of a
// built-in rule (S09-02-precedence, "Network rules merge as a union").
func (d *decoder) ruleName(name, keyName, path string, line int) {
	for _, n := range []string{name, keyName} {
		if builtinRuleName(n) {
			d.c.add(RuleRuleName, line, path, n, "the name is reserved for the built-in rules")
			return
		}
	}
	if first, dup := d.names[name]; dup {
		d.c.add(RuleRuleName, line, path, name, "another rule in the file, at line %d, has this name", first)
		return
	}
	d.names[name] = line
}

// builtinRuleName reports whether a name is reserved for the rules of
// the built-in profiles: a profile name, or a name that starts with
// `builtin`. The built-in rules are named in this space (M3.7a), so
// no user rule can take one's name.
func builtinRuleName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "builtin") {
		return true
	}
	for _, p := range []builtin.Profile{builtin.GitHosting, builtin.ModelAPI, builtin.PackageRegistries} {
		if lower == string(p) {
			return true
		}
	}
	return false
}

// binaries refuses program narrowing: each rule lists `path: "/**"`,
// and only that (S09-01-policy-files, "Program narrowing").
func (d *decoder) binaries(n *yaml.Node, rulePath string, line int) {
	path := child(rulePath, "binaries")
	if n == nil {
		d.c.add(RuleBinaryNarrowing, line, rulePath, "",
			`the rule has no binaries: list path: "/**", because rules can't name programs until X14-flow-attribution delivers program labels`)
		return
	}
	if !d.sequenceNode(n, path) {
		return
	}
	universal := false
	for i, b := range n.Content {
		p := index(path, i)
		if !d.mappingNode(b, p) {
			return
		}
		v := lookup(b, "path")
		if v == nil {
			d.c.add(RuleBinaryNarrowing, b.Line, p, "", "the entry has no path")
			return
		}
		s, ok := d.str(v, child(p, "path"))
		if !ok {
			return
		}
		if s != universalBinary {
			d.c.add(RuleBinaryNarrowing, v.Line, child(p, "path"), s,
				`names a program: rules can't narrow by program until X14-flow-attribution delivers program labels, so the only entry is path: "/**"`)
			return
		}
		universal = true
	}
	if !universal {
		d.c.add(RuleBinaryNarrowing, n.Line, path, "", `the rule lacks the entry path: "/**"`)
	}
}

// endpoint reads one endpoint.
func (d *decoder) endpoint(n *yaml.Node, path, ruleName string) (policy.Endpoint, bool) {
	e := policy.Endpoint{Source: d.file.Source, RuleName: ruleName}
	if !d.mappingNode(n, path) {
		return e, false
	}
	ok := d.host(n, path, &e)
	ok = d.ports(n, path, &e) && ok
	ok = d.endpointPath(n, path, &e) && ok
	ok = d.enums(n, path, &e) && ok
	ok = d.httpRules(n, path, &e) && ok
	return e, ok
}

func (d *decoder) host(n *yaml.Node, path string, e *policy.Endpoint) bool {
	v := lookup(n, "host")
	if v == nil {
		d.c.add(RuleValue, n.Line, child(path, "host"), "", "is missing: every endpoint names a host")
		return false
	}
	s, ok := d.str(v, child(path, "host"))
	if !ok {
		return false
	}
	h, r := match.CompileHost(s)
	if r != nil {
		d.c.add(r.Rule, v.Line, child(path, "host"), s, "%s", strings.TrimPrefix(r.Error(), r.Rule+": "))
		return false
	}
	e.Host = h
	return true
}

// ports reads `port` or `ports`. OpenShell takes `ports` when both are
// set, and its prover can't model that endpoint, so both is refused.
func (d *decoder) ports(n *yaml.Node, path string, e *policy.Endpoint) bool {
	one, many := lookup(n, "port"), lookup(n, "ports")
	switch {
	case one != nil && many != nil:
		d.c.add(RulePort, n.Line, path, "", "the endpoint sets both port and ports: set one")
		return false
	case one != nil:
		p, ok := d.port(one, child(path, "port"))
		e.Ports = []uint16{p}
		return ok
	case many == nil:
		d.c.add(RulePort, n.Line, path, "", "the endpoint names no port")
		return false
	}
	if !d.sequenceNode(many, child(path, "ports")) {
		return false
	}
	ok := true
	for i, v := range many.Content {
		p, pok := d.port(v, index(child(path, "ports"), i))
		e.Ports = append(e.Ports, p)
		ok = ok && pok
	}
	return ok
}

func (d *decoder) port(n *yaml.Node, path string) (uint16, bool) {
	v, ok := uintValue(n, math.MaxUint16)
	if !ok || v == 0 || v > math.MaxUint16 {
		d.c.add(RulePort, n.Line, path, scalarValue(n), "isn't a port: a decimal integer from 1 to 65535")
		return 0, false
	}
	return uint16(v), true
}

// endpointPath reads the endpoint-level `path`.
func (d *decoder) endpointPath(n *yaml.Node, path string, e *policy.Endpoint) bool {
	v := lookup(n, "path")
	if v == nil {
		return true
	}
	p, ok := d.pattern(v, child(path, "path"))
	if ok {
		e.Path = &p
	}
	return ok
}

// pattern reads a path pattern.
func (d *decoder) pattern(n *yaml.Node, path string) (match.Pattern, bool) {
	s, ok := d.checkedStr(n, path, match.MaxPattern)
	if !ok {
		return match.Pattern{}, false
	}
	p, r := match.Compile(s)
	if r != nil {
		d.c.add(r.Rule, n.Line, path, s, "%s", strings.TrimPrefix(r.Error(), r.Rule+": "))
		return match.Pattern{}, false
	}
	return p, true
}
