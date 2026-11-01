// Package upstream is the one dialer every upstream connection of
// wb-proxyd goes through: inspected, pass and raw TCP streams, and
// wb-proxyd's own fetches. It resolves a name once, refuses it when any
// address it resolves to is in a range of S07-07-hosts-and-modes,
// "Upstream address", or on one of the host's own networks, and then
// connects only to the addresses it checked (SEC05-default-deny). Each
// refusal is logged with its rule, the host and the address
// (SEC10-audit).
package upstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// Rule names the rule behind a refusal, for the log and the caller's
// answer to the guest.
type Rule string

// The rules of this package (S07-07-hosts-and-modes, "Upstream
// address").
const (
	// RuleAddressRefused: the name resolved to an address in a refused
	// range or on one of the host's networks, or the host's networks
	// couldn't be read.
	RuleAddressRefused Rule = "upstream-address-refused"
	// RuleResolveFailed: the lookup failed, or gave no address.
	RuleResolveFailed Rule = "upstream-resolve-failed"
	// RuleConnectFailed: no checked address connected.
	RuleConnectFailed Rule = "upstream-connect-failed"
)

// Bounds of one Dial (S04-08-records-and-bounds, "Host work the guest
// can cause"). A Dial runs one lookup and at most maxAttempts connects,
// one at a time.
const (
	resolveTimeout = 5 * time.Second
	connectTimeout = 10 * time.Second
	maxAttempts    = 4
)

// Refusal is the error of every refusal of Dial.
type Refusal struct {
	Rule Rule
	// Host is the name Dial was given.
	Host string
	// Addr is the address refused, or the last one that failed to
	// connect. It is the zero Addr when the lookup gave none.
	Addr netip.Addr
	// Reason says which range, host network or failure it was.
	Reason string

	err error
}

func (r *Refusal) Error() string {
	msg := "upstream: " + string(r.Rule) + ": " + r.Host
	if r.Addr.IsValid() {
		msg += " at " + r.Addr.String()
	}
	msg += ": " + r.Reason
	if r.err != nil {
		msg += ": " + r.err.Error()
	}
	return msg
}

// Unwrap returns the cause, such as the lookup's or the last connect's
// error.
func (r *Refusal) Unwrap() error { return r.err }

// resolver is the part of *net.Resolver the dialer uses.
type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer opens upstream connections for wb-proxyd. Its range table, the
// reader of the host's networks, the resolver and the connect function
// are set by New, and only tests in this package replace them: no
// caller can widen what it reaches.
type Dialer struct {
	log *slog.Logger

	ranges   []addrRange
	networks func() ([]netip.Prefix, error)
	resolver resolver
	connect  func(ctx context.Context, network, address string) (net.Conn, error)
}

// New returns a Dialer that resolves with the host's resolver and logs
// through a ratelog handler over log.
func New(log *slog.Logger) *Dialer {
	return &Dialer{
		log:      ratelog.New(log),
		ranges:   defaultRanges,
		networks: hostNetworks,
		resolver: net.DefaultResolver,
		connect:  (&net.Dialer{}).DialContext,
	}
}

// Dial resolves host once, checks every address it resolves to, and
// connects to the checked addresses in the order the lookup gave them,
// one at a time, returning the first connection that opens. It never
// resolves the name again (S07-07-hosts-and-modes, "Upstream address").
// Every refusal is a *Refusal, and is logged. When the caller's ctx
// ends during the lookup or the connects, Dial returns ctx's error and
// logs nothing: the caller gave up, and a refusal record would use up
// the rule's ratelog window.
func (d *Dialer) Dial(ctx context.Context, host string, port uint16) (net.Conn, error) {
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return nil, d.refuseUnlessDone(ctx, err)
	}
	if err := d.check(host, addrs); err != nil {
		return nil, d.refuse(err)
	}
	conn, err := d.dialChecked(ctx, host, addrs, port)
	if err != nil {
		return nil, d.refuseUnlessDone(ctx, err)
	}
	d.log.Debug("upstream connected", "host", host, "addr", conn.RemoteAddr().String())
	return conn, nil
}

// resolve runs the one lookup of a Dial under resolveTimeout.
func (d *Dialer) resolve(ctx context.Context, host string) ([]netip.Addr, *Refusal) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := d.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, &Refusal{Rule: RuleResolveFailed, Host: host, Reason: "lookup failed", err: err}
	}
	if len(addrs) == 0 {
		return nil, &Refusal{Rule: RuleResolveFailed, Host: host, Reason: "no address"}
	}
	return addrs, nil
}

// check refuses the name when any of its addresses is in a refused
// range or on a network of the host, read now (SEC05-default-deny,
// S07-07-hosts-and-modes, "Upstream address"). A host whose networks
// can't be read refuses every name.
func (d *Dialer) check(host string, addrs []netip.Addr) *Refusal {
	for _, a := range addrs {
		if r, bad := match(d.ranges, a); bad {
			return &Refusal{Rule: RuleAddressRefused, Host: host, Addr: a, Reason: "range " + r.name}
		}
	}
	nets, err := d.networks()
	if err != nil {
		return &Refusal{Rule: RuleAddressRefused, Host: host, Reason: "host networks unreadable", err: err}
	}
	for _, a := range addrs {
		if p, bad := onHostNetwork(nets, a); bad {
			return &Refusal{Rule: RuleAddressRefused, Host: host, Addr: a, Reason: "host network " + p.String()}
		}
	}
	return nil
}

// dialChecked connects to the checked addresses in order, each under
// connectTimeout, and stops after maxAttempts.
func (d *Dialer) dialChecked(ctx context.Context, host string, addrs []netip.Addr, port uint16) (net.Conn, *Refusal) {
	var last netip.Addr
	var errs []error
	for i, a := range addrs {
		if i == maxAttempts {
			break
		}
		last = a
		conn, err := d.connectOne(ctx, netip.AddrPortFrom(a, port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, &Refusal{
		Rule: RuleConnectFailed, Host: host, Addr: last,
		Reason: fmt.Sprintf("%d of %d addresses tried", len(errs), len(addrs)),
		err:    errors.Join(errs...),
	}
}

func (d *Dialer) connectOne(ctx context.Context, ap netip.AddrPort) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	return d.connect(ctx, "tcp", ap.String())
}

// refuseUnlessDone returns ctx's error when ctx has ended, and
// otherwise refuses with r.
func (d *Dialer) refuseUnlessDone(ctx context.Context, r *Refusal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.refuse(r)
}

// refuse logs a refusal with its rule, the host and the address, and
// returns it.
func (d *Dialer) refuse(r *Refusal) error {
	attrs := []any{"rule", string(r.Rule), "host", r.Host, "reason", r.Reason}
	if r.Addr.IsValid() {
		attrs = append(attrs, "addr", r.Addr.String())
	}
	if r.err != nil {
		attrs = append(attrs, "err", r.err.Error())
	}
	d.log.Warn("upstream refused", attrs...)
	return r
}
