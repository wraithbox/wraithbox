package policy

import (
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// HostMode is the mode of a host and port, resolved over the union
// (S09-02-precedence, "Mode per host and port").
type HostMode int

// The modes. ModeNone means no endpoint names the host and port.
const (
	ModeNone HostMode = iota
	ModeEnforce
	ModeAudit
)

func (m HostMode) String() string {
	switch m {
	case ModeEnforce:
		return "enforce"
	case ModeAudit:
		return "audit"
	case ModeNone:
	}
	return "none"
}

// Names reports whether the endpoint's host pattern matches host and
// one of its ports is port. A wildcard endpoint names every host it
// matches.
func (e *Endpoint) Names(host dnsname.Name, port uint16) bool {
	return e.Host.Match(host) && slices.Contains(e.Ports, port)
}

// Mode resolves `enforcement` for one host and port over a set of
// endpoints (S09-02-precedence, "Mode per host and port"). The mode is
// audit only when every endpoint that names the host and port is in
// audit. Any one in enforce, or without the field, makes it enforce, so
// a wildcard endpoint in enforce wins over an exact host in audit, and
// the reverse. No endpoint gives ModeNone.
func Mode(endpoints []Endpoint, host dnsname.Name, port uint16) HostMode {
	mode := ModeNone
	for i := range endpoints {
		e := &endpoints[i]
		if !e.Names(host, port) {
			continue
		}
		if e.Enforcement != EnforcementAudit {
			return ModeEnforce
		}
		mode = ModeAudit
	}
	return mode
}

// StreamProtocol is the protocol a host and port expects
// (S07-15-non-http-streams, "One protocol per host and port").
type StreamProtocol int

// The stream protocols. StreamNone is a port the policy doesn't name
// for the host, which wb-netd resets with `port-not-allowed`
// (S07-02-packet-path, "Connections").
const (
	StreamNone StreamProtocol = iota
	StreamTLS
	StreamPlainHTTP
	StreamRawTCP
)

func (s StreamProtocol) String() string {
	switch s {
	case StreamTLS:
		return "tls"
	case StreamPlainHTTP:
		return "http"
	case StreamRawTCP:
		return "raw-tcp"
	case StreamNone:
	}
	return "none"
}

// PlainHTTPPort is the one port that carries plain HTTP.
const PlainHTTPPort = 80

// Protocol returns the protocol of host and port: raw TCP on a port a
// raw TCP entry names, plain HTTP on port 80 when an endpoint names
// it, TLS on every other port an endpoint names, and StreamNone
// otherwise. A policy that gives one host and port two protocols is
// refused by Check (RuleProtocolConflict), so the order here never
// decides between two.
func (p *Policy) Protocol(host dnsname.Name, port uint16) StreamProtocol {
	for _, r := range p.RawTCP {
		if r.Host == host && r.Port == port {
			return StreamRawTCP
		}
	}
	for i := range p.Endpoints {
		if !p.Endpoints[i].Names(host, port) {
			continue
		}
		if port == PlainHTTPPort {
			return StreamPlainHTTP
		}
		return StreamTLS
	}
	return StreamNone
}

// IsPassHost reports whether name is in the pass list
// (S07-07-hosts-and-modes, "pass"). A pass host is in pass mode on
// every TLS port.
func (p *Policy) IsPassHost(name dnsname.Name) bool {
	for _, h := range p.PassHosts {
		if h.Host == name {
			return true
		}
	}
	return false
}

// InspectsTLS reports whether wb-proxyd terminates the TLS of host and
// port: the port carries TLS and the host isn't a pass host
// (S07-07-hosts-and-modes, "Modes, per host"). Only such a stream gets a
// leaf (S09-07-ca-certificates, "Leaf issuance"). A nil policy inspects
// nothing.
func (p *Policy) InspectsTLS(host dnsname.Name, port uint16) bool {
	return p != nil && p.Protocol(host, port) == StreamTLS && !p.IsPassHost(host)
}

// The methods of OpenShell's access presets (`expand_access_presets`
// in OpenShell v0.1.2).
var (
	readOnlyMethods  = []match.Method{"GET", "HEAD", "OPTIONS"}
	readWriteMethods = []match.Method{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH"}
)

// Allows returns the allow rules of the endpoint as the matcher sees
// them: its rules, or its access preset expanded, or for an L4
// endpoint every method on every path. Each carries the endpoint's
// host and path. The load checks of S09-02-precedence ask
// match.CanMatch of each.
func (e *Endpoint) Allows() []match.Rule {
	all := match.All()
	var out []match.Rule
	add := func(m match.Method, path match.Pattern) {
		out = append(out, match.Rule{Host: e.Host, EndpointPath: e.Path, Method: m, Path: path})
	}
	switch {
	case e.Protocol == ProtocolL4, e.Access == AccessFull:
		add(match.AnyMethod, all)
	case e.Access == AccessReadOnly:
		for _, m := range readOnlyMethods {
			add(m, all)
		}
	case e.Access == AccessReadWrite:
		for _, m := range readWriteMethods {
			add(m, all)
		}
	}
	for _, r := range e.Rules {
		add(r.Method, r.Path)
	}
	return out
}
