package netdtest

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// CallKind names a call to a fake accounting channel or hand-off socket.
type CallKind string

// The calls the fakes take.
const (
	CallEntry         CallKind = "entry"          // Accounting.Entry
	CallRelease       CallKind = "release"        // Accounting.Release
	CallDebit         CallKind = "debit"          // Accounting.Debit
	CallReport        CallKind = "report"         // NameReports.Report
	CallReportRelease CallKind = "report-release" // NameReports.Release
)

// Call is one call to a fake, with the error it returned.
type Call struct {
	Kind CallKind
	Name dnsname.Name
	Addr netip.Addr
	Err  error
}

// Fault is how a fake answers a kind of call: after Delay, which it
// waits out whatever the deadline, with Err.
type Fault struct {
	Err   error
	Delay time.Duration
}

// Calls is the ordered list of the calls to the fakes that share it.
type Calls struct {
	mu     sync.Mutex
	list   []Call
	faults map[CallKind]Fault
}

// Fail makes every later call of kind answer with f. The zero Fault
// makes it succeed again.
func (c *Calls) Fail(kind CallKind, f Fault) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.faults == nil {
		c.faults = map[CallKind]Fault{}
	}
	c.faults[kind] = f
}

// List returns the calls so far, in order.
func (c *Calls) List() []Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Call(nil), c.list...)
}

// Reset forgets the calls so far, and keeps the faults.
func (c *Calls) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = nil
}

// call records one call and answers it with its kind's fault.
func (c *Calls) call(kind CallKind, name dnsname.Name, addr netip.Addr) error {
	c.mu.Lock()
	f := c.faults[kind]
	c.list = append(c.list, Call{Kind: kind, Name: name, Addr: addr, Err: f.Err})
	c.mu.Unlock()
	if f.Delay > 0 {
		time.Sleep(f.Delay)
	}
	return f.Err
}

// Accounting is an in-memory netd.Accounting.
type Accounting struct{ Calls *Calls }

func (a Accounting) Entry(_ context.Context, name dnsname.Name, addr netip.Addr) error {
	return a.Calls.call(CallEntry, name, addr)
}

func (a Accounting) Release(_ context.Context, addr netip.Addr) error {
	return a.Calls.call(CallRelease, dnsname.Name{}, addr)
}

func (a Accounting) Debit(_ context.Context, name dnsname.Name) error {
	return a.Calls.call(CallDebit, name, netip.Addr{})
}

// NameReports is an in-memory netd.NameReports.
type NameReports struct{ Calls *Calls }

func (r NameReports) Report(_ context.Context, name dnsname.Name, addr netip.Addr) error {
	return r.Calls.call(CallReport, name, addr)
}

func (r NameReports) Release(_ context.Context, addr netip.Addr) error {
	return r.Calls.call(CallReportRelease, dnsname.Name{}, addr)
}

// Ports is an in-memory netd.PortPolicy: the ports allowed per name.
type Ports struct {
	mu      sync.Mutex
	allowed map[dnsname.Name]map[uint16]bool
}

// Allow allows ports for name.
func (p *Ports) Allow(name dnsname.Name, ports ...uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowed == nil {
		p.allowed = map[dnsname.Name]map[uint16]bool{}
	}
	if p.allowed[name] == nil {
		p.allowed[name] = map[uint16]bool{}
	}
	for _, port := range ports {
		p.allowed[name][port] = true
	}
}

func (p *Ports) PortAllowed(name dnsname.Name, port uint16) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allowed[name][port]
}

// Stream is a connection a HandOff took.
type Stream struct {
	Conn net.Conn
	Flow netd.NamedFlow
}

// HandOff is an in-memory netd.StreamHandOff that passes each stream to
// a channel.
type HandOff chan Stream

func (h HandOff) HandOffStream(conn net.Conn, f netd.NamedFlow) { h <- Stream{Conn: conn, Flow: f} }

// Name is the Name of a host that passes the name form rule. It panics
// on one that doesn't.
func Name(host string) dnsname.Name {
	n, r := dnsname.FromHost(host)
	if r != nil {
		panic(r.Error())
	}
	return n
}
