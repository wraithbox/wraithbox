// Package policy is the network policy model: the effective policy of a
// VM as wb-hostd computes it and wb-netd and wb-proxyd enforce it
// (S07-01-per-vm-policy, S09-01-policy-files, S09-02-precedence). It
// holds the endpoints with their source and rule name, the pass hosts,
// the raw TCP entries, the wildcard budget, the profile parts that are
// off and the project set, and answers two questions on them: the mode
// of a host and port (Mode) and the protocol a port expects
// (Policy.Protocol).
//
// The effective policy reaches the daemons as the message
// wraithbox.policy.v1.EffectivePolicy. FromProto checks every bound and
// invariant of that message and refuses a message that breaks one. It
// never repairs one. Check runs the same checks on a Policy built in
// Go.
//
// The matcher is in package match, and the catalog of covered hosts in
// package builtin.
package policy

import (
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// Bounds of the effective policy. Each one is named in the comments of
// policy.proto, and FromProto refuses a message past it with
// RuleBound. The string caps are those S09-01-policy-files, "Strings",
// leaves to code.
const (
	MaxEndpoints        = 4096
	MaxRulesPerEndpoint = 256
	// MaxRules caps the allow and deny rules of all endpoints together.
	MaxRules          = 16384
	MaxPortsPerEnd    = 64
	MaxPassHosts      = 256
	MaxRawTCP         = 256
	MaxPartsOff       = 64
	MaxProjects       = 64
	MaxRepositories   = 64
	MaxRuleName       = 128
	MaxReason         = 256
	MaxRepository     = 2048
	MaxWildcardBudget = 65535
	// ProjectIDLen is the length of a project id: lowercase
	// hexadecimal (S05-04-naming).
	ProjectIDLen = 32
)

// SourceKind says where a rule comes from (S09-02-precedence,
// "Network rules merge as a union").
type SourceKind int32

// The sources, in the order of S09-02-precedence.
const (
	SourceUnset SourceKind = iota
	SourceBuiltin
	SourceGlobal
	SourceProject
	SourceRepository
	SourceApproval
)

var sourceNames = [...]string{"unset", "builtin", "global", "project", "repository", "approval"}

func (k SourceKind) String() string {
	if k < 0 || int(k) >= len(sourceNames) {
		return "invalid"
	}
	return sourceNames[k]
}

// Source is the source of a rule or entry. Project is set for a project
// and a repository source, and may be set for an approval. Repository
// is the compare form of the repository (gitremote.Result.Compare) and
// is set for a repository source only.
type Source struct {
	Kind       SourceKind
	Project    string
	Repository string
}

// EndpointProtocol is an endpoint's `protocol`: L4 for none (or `tcp`),
// REST for `rest`. Other OpenShell protocols are refused at load.
type EndpointProtocol int32

// The endpoint protocols.
const (
	ProtocolUnset EndpointProtocol = iota
	ProtocolL4
	ProtocolREST
)

// Enforcement is an endpoint's `enforcement`. EnforcementUnset is an
// endpoint without the field, which enforces (S09-02-precedence, "Mode
// per host and port").
type Enforcement int32

// The enforcement values.
const (
	EnforcementUnset Enforcement = iota
	EnforcementEnforce
	EnforcementAudit
)

// Access is an endpoint's `access` preset, or AccessNone.
type Access int32

// The access presets of OpenShell v0.1.2.
const (
	AccessNone Access = iota
	AccessReadOnly
	AccessReadWrite
	AccessFull
)

// HTTPRule is one allow or deny rule of an endpoint: a method and a
// path pattern.
type HTTPRule struct {
	Method match.Method
	Path   match.Pattern
}

// Endpoint is one endpoint of a named rule (S09-01-policy-files,
// "Implemented keys").
type Endpoint struct {
	Source Source
	// RuleName is OpenShell's effective name of the rule: its `name`,
	// else its key under `network_policies`. Rules are keyed by source
	// and rule name.
	RuleName    string
	Host        match.Host
	Ports       []uint16
	Path        *match.Pattern
	Protocol    EndpointProtocol
	Enforcement Enforcement
	Access      Access
	Rules       []HTTPRule
	DenyRules   []HTTPRule
}

// PassHost is a host in pass mode (S07-07-hosts-and-modes, "pass").
type PassHost struct {
	Host   dnsname.Name
	Source Source
	Reason string
}

// RawTCP is a raw TCP entry (S07-15-non-http-streams, "Raw TCP
// entries"): one exact host and one port.
type RawTCP struct {
	Host   dnsname.Name
	Port   uint16
	Source Source
	Reason string
}

// PartOff is a built-in profile part that a source turned off
// (S07-11-http-policy, "Profile parts").
type PartOff struct {
	Profile string
	Kind    string
	Part    string
	Source  Source
}

// Project is a project with a session in the VM, and the compare forms
// of its repositories (S07-01-per-vm-policy).
type Project struct {
	ID           string
	Repositories []string
}

// Policy is the effective policy of one VM.
type Policy struct {
	// Generation increases with each recompute, so a daemon can tell a
	// newer policy from an older one.
	Generation uint64
	Endpoints  []Endpoint
	PassHosts  []PassHost
	RawTCP     []RawTCP
	// WildcardBudget is the number of new names a VM may resolve under
	// wildcards in an active period (S07-03-dns, "Wildcard budget").
	WildcardBudget uint32
	PartsOff       []PartOff
	Projects       []Project
}
