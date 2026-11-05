package policyfile

import (
	"fmt"
	"strconv"
	"strings"
)

// The rules of a refusal at load. A refusal from the host, path or
// method checks keeps the rule of package match or dnsname, such as
// dns-name-form, and a refusal from the model's own check
// (policy.Policy.Check) keeps the model's rule.
const (
	// RuleYAMLBound: the file is past a bound of the decoder: its size,
	// its depth or its number of nodes (S10-01-libraries, "Network
	// policy schema"; X24-openshell-artifacts, "Policy schema").
	RuleYAMLBound = "policy-yaml-bound"
	// RuleYAMLSyntax: the file isn't YAML the decoder can read.
	RuleYAMLSyntax = "policy-yaml-syntax"
	// RuleYAMLForm: the file uses a YAML feature policy files don't
	// need: an anchor, an alias, a merge key, a tag outside the core
	// schema, a duplicate key, a key that isn't a string, or a second
	// document.
	RuleYAMLForm = "policy-yaml-form"
	// RuleVersion: `version` is missing or isn't 1.
	RuleVersion = "policy-version"
	// RuleKeyUnknown: a key that OpenShell v0.1.2 doesn't have at that
	// level, and that isn't a Wraith Box extension key
	// (S09-01-policy-files, "Closed keys").
	RuleKeyUnknown = "policy-key-unknown"
	// RuleKeyRefused: an OpenShell v0.1.2 key that Wraith Box doesn't
	// implement (S09-01-policy-files, "Refused keys").
	RuleKeyRefused = "policy-key-refused"
	// RuleKeyUnsupported: a Wraith Box extension key whose feature isn't
	// built yet (S09-01-policy-files, "Extension keys").
	RuleKeyUnsupported = "policy-key-unsupported"
	// RuleSource: a source that may not hold the key, such as a
	// `wraithbox` key in repository configuration
	// (S09-03-project-settings, "Closed keys"). It is the model's rule
	// of the same name.
	RuleSource = "policy-source"
	// RuleAuditSource: `enforcement: audit` in repository configuration
	// (S09-03-project-settings, "Repository-supplied configuration").
	RuleAuditSource = "policy-audit-source"
	// RuleValue: a value of the wrong YAML type, or outside its set.
	RuleValue = "policy-value-invalid"
	// RuleString: a string that is empty, too long, or not printable
	// ASCII (S09-01-policy-files, "Strings"). It is the model's rule of
	// the same name.
	RuleString = "policy-string"
	// RulePort: a port outside 1 to 65535, or both `port` and `ports`.
	RulePort = "policy-port"
	// RuleBinaryNarrowing: a `binaries` entry other than `path: "/**"`,
	// or a rule without it (S09-01-policy-files, "Program narrowing").
	RuleBinaryNarrowing = "policy-binary-narrowing"
	// RuleTunnel: a rule that names the method CONNECT
	// (S07-16-tls-and-tunnels, "`CONNECT` and other tunnels").
	RuleTunnel = "http-tunnel"
	// RuleProtocolUnsupported: an endpoint `protocol` other than `rest`
	// or none (I489, maintainer decision 1).
	RuleProtocolUnsupported = "policy-protocol-unsupported"
	// RuleRuleName: two rules with one name in a file, or a rule with
	// the name of a built-in rule (S09-02-precedence, "Network rules
	// merge as a union").
	RuleRuleName = "policy-rule-name"
	// RulePassHost: a pass entry with a wildcard host, without a
	// reason, or on a covered host (S07-07-hosts-and-modes, "pass").
	RulePassHost = "pass-host-refused"
	// RuleRawTCP: the same for a raw TCP entry (S07-15-non-http-streams).
	RuleRawTCP = "raw-tcp-refused"
	// RuleAuditGuarded: an `audit` endpoint on a covered host
	// (S07-07-hosts-and-modes, "Guarded hosts").
	RuleAuditGuarded = "audit-guarded-host"
	// RuleProtocolConflict: a host and port that the file gives raw TCP
	// and an endpoint (S07-15-non-http-streams, "One protocol per host
	// and port").
	RuleProtocolConflict = "protocol-conflict"
	// RuleGraphQLPath: a rule on a covered host that can match the
	// host's GraphQL path (S09-02-precedence, "Network rules merge as a
	// union").
	RuleGraphQLPath = "builtin-graphql-path"
	// RuleModelTokenPath: a rule that can allow POST to an OAuth token
	// path on a model API host (S09-02-precedence, "The OAuth token
	// paths").
	RuleModelTokenPath = "model-token-path"
	// RuleFileUnreadable: the file exists but isn't a regular file or
	// can't be read.
	RuleFileUnreadable = "policy-file-unreadable"
)

// maxValueShown caps the bytes of a value at fault that a message
// shows, before it is escaped, so a 4 MiB key doesn't make a 4 MiB
// message.
const maxValueShown = 64

// Refusal is one reason a file is refused. It names the file, the line
// and the key path, and the value at fault, escaped
// (NFR06-explained-refusals).
type Refusal struct {
	Rule string
	File string
	// Line is 1-based, and 0 when the refusal isn't about one line.
	Line int
	// Key is the key path, such as
	// `network_policies.github.endpoints[0].host`, with each key escaped.
	Key string
	// Value is the value at fault, escaped and cut to maxValueShown
	// bytes, or "".
	Value string

	msg string
}

// Error returns the file, line, key, rule, reason and value. Every byte
// from the file in it is escaped, so it is safe to log and to show.
func (r *Refusal) Error() string {
	var b strings.Builder
	b.WriteString(r.File)
	if r.Line > 0 {
		b.WriteString(":" + strconv.Itoa(r.Line))
	}
	if r.Key != "" {
		b.WriteString(": " + r.Key)
	}
	b.WriteString(": " + r.Rule + ": " + r.msg)
	if r.Value != "" {
		b.WriteString(" (value " + r.Value + ")")
	}
	return b.String()
}

// Refusals is the error of a refused file: the first refusal of each
// rule, at most MaxRefusals of them (I489, decision 10).
type Refusals struct {
	File string
	List []*Refusal
}

// Error returns each refusal on its own line.
func (rs *Refusals) Error() string {
	lines := make([]string, 0, len(rs.List)+1)
	lines = append(lines, fmt.Sprintf("the policy file %s is refused:", rs.File))
	for _, r := range rs.List {
		lines = append(lines, "  "+r.Error())
	}
	return strings.Join(lines, "\n")
}

// Has reports whether one of the refusals has the rule.
func (rs *Refusals) Has(rule string) bool {
	for _, r := range rs.List {
		if r.Rule == rule {
			return true
		}
	}
	return false
}

// escape quotes a value from the file with Go's ASCII escapes, cut to
// maxValueShown bytes first.
func escape(s string) string {
	cut := ""
	if len(s) > maxValueShown {
		s, cut = s[:maxValueShown], "..."
	}
	return strconv.QuoteToASCII(s) + cut
}

// isPlainKey reports whether a key can be shown in a key path without
// quotes.
func isPlainKey(s string) bool {
	if s == "" || len(s) > maxValueShown {
		return false
	}
	for i := range len(s) {
		c := s[i]
		ok := c == '_' || c == '-' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// child returns the key path of a key under path.
func child(path, key string) string {
	if !isPlainKey(key) {
		key = escape(key)
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// index returns the key path of a list entry.
func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

// MaxRefusals caps the refusals of one file, so a hostile file can't
// make an unbounded list (I489, decision 10).
const MaxRefusals = 16

// collector keeps the first refusal of each rule, up to MaxRefusals.
type collector struct {
	file string
	list []*Refusal
	seen map[string]bool
}

func newCollector(file string) *collector {
	return &collector{file: file, seen: map[string]bool{}}
}

// add records a refusal, unless one of its rule is recorded already or
// the list is full.
func (c *collector) add(rule string, line int, key, value, format string, args ...any) {
	if c.seen[rule] || len(c.list) >= MaxRefusals {
		return
	}
	c.seen[rule] = true
	r := &Refusal{Rule: rule, File: c.file, Line: line, Key: key, msg: fmt.Sprintf(format, args...)}
	if value != "" {
		r.Value = escape(value)
	}
	c.list = append(c.list, r)
}

func (c *collector) failed() bool { return len(c.list) > 0 }

func (c *collector) err() *Refusals {
	if !c.failed() {
		return nil
	}
	return &Refusals{File: c.file, List: c.list}
}
