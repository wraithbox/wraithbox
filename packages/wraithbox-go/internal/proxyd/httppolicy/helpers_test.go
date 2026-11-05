package httppolicy

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

const (
	projectA = "0123456789abcdef0123456789abcdef"
	projectB = "fedcba9876543210fedcba9876543210"
)

var (
	globalSrc   = policy.Source{Kind: policy.SourceGlobal}
	projectASrc = policy.Source{Kind: policy.SourceProject, Project: projectA}
	projectBSrc = policy.Source{Kind: policy.SourceProject, Project: projectB}
)

func mustHost(s string) match.Host {
	h, r := match.CompileHost(s)
	if r != nil {
		panic(r)
	}
	return h
}

// rules compiles rules written as "METHOD /path".
func rules(specs ...string) []policy.HTTPRule {
	var out []policy.HTTPRule
	for _, s := range specs {
		m, p, _ := strings.Cut(s, " ")
		cm, r := match.CompileMethod(m)
		if r != nil {
			panic(r)
		}
		cp, r := match.Compile(p)
		if r != nil {
			panic(r)
		}
		out = append(out, policy.HTTPRule{Method: cm, Path: cp})
	}
	return out
}

// rest returns a REST endpoint of src on host, port 443.
func rest(src policy.Source, name, host string, allow []string, deny ...string) policy.Endpoint {
	return policy.Endpoint{
		Source: src, RuleName: name, Host: mustHost(host), Ports: []uint16{443},
		Protocol: policy.ProtocolREST, Rules: rules(allow...), DenyRules: rules(deny...),
	}
}

func audit(e policy.Endpoint) policy.Endpoint {
	e.Enforcement = policy.EnforcementAudit
	return e
}

func project(id string, repos ...string) policy.Project {
	return policy.Project{ID: id, Repositories: repos}
}

// engine builds an engine of p, which must pass Check, logging to a
// buffer the test can read.
func engine(t testing.TB, p policy.Policy) (*Engine, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	e, err := New(&p, "vm1", slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, &buf
}

func quietEngine(t testing.TB, p policy.Policy) *Engine {
	t.Helper()
	e, err := New(&p, "vm1", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func hostName(s string) dnsname.Name {
	n, r := dnsname.FromHost(s)
	if r != nil {
		panic(r)
	}
	return n
}

// req builds a request from "METHOD host/path?query" on port 443.
func req(s string) Request {
	m, rest, _ := strings.Cut(s, " ")
	target, query, _ := strings.Cut(rest, "?")
	h, p, _ := strings.Cut(target, "/")
	return Request{Method: m, Host: hostName(h), Port: 443, Path: "/" + p, RawQuery: query}
}

func onPort(r Request, port uint16) Request {
	r.Port = port
	return r
}

// want checks a decision's allow and rule.
func want(t *testing.T, e *Engine, r Request, allow bool, rule string) Decision {
	t.Helper()
	d := e.Decide(r)
	if d.Allow != allow || d.Rule != rule {
		t.Errorf("%s %s:%d %s?%s = allow %v rule %s (broke %q, source %q), want allow %v rule %s",
			r.Method, r.Host, r.Port, r.Path, r.RawQuery, d.Allow, d.Rule, d.Broke, d.Source, allow, rule)
	}
	return d
}
