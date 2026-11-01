package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
)

// fakeResolver answers every lookup with the same addresses or error,
// and counts the lookups.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
	calls int
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.addrs, r.err
}

// fakeConnect records each address dialed and fails those in fail.
type fakeConnect struct {
	mu     sync.Mutex
	dialed []string
	fail   map[string]bool
	// before runs ahead of each dial, unlocked.
	before func()
}

func (c *fakeConnect) dial(_ context.Context, network, address string) (net.Conn, error) {
	if c.before != nil {
		c.before()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if network != "tcp" {
		return nil, errors.New("network " + network)
	}
	c.dialed = append(c.dialed, address)
	if c.fail[address] {
		return nil, errors.New("connection refused")
	}
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}

// logBuf collects JSON log records.
type logBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuf) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for line := range bytes.Lines(l.buf.Bytes()) {
		rec := map[string]any{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

type harness struct {
	d    *Dialer
	res  *fakeResolver
	conn *fakeConnect
	log  *logBuf
}

// newHarness builds a Dialer with the default ranges, the given host
// networks, and a fake resolver and connect.
func newHarness(addrs []string, nets []string) *harness {
	h := &harness{res: &fakeResolver{}, conn: &fakeConnect{fail: map[string]bool{}}, log: &logBuf{}}
	for _, a := range addrs {
		h.res.addrs = append(h.res.addrs, netip.MustParseAddr(a))
	}
	var prefixes []netip.Prefix
	for _, n := range nets {
		prefixes = append(prefixes, netip.MustParsePrefix(n))
	}
	h.d = New(slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h.d.networks = func() ([]netip.Prefix, error) { return prefixes, nil }
	h.d.resolver = h.res
	h.d.connect = h.conn.dial
	return h
}

func wantRefusal(t *testing.T, err error, rule Rule) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("error %v, want a *Refusal with %s", err, rule)
	}
	if r.Rule != rule {
		t.Fatalf("rule %s, want %s: %v", r.Rule, rule, err)
	}
	return r
}

// A name with one bad address among good ones is refused, nothing is
// dialed, and the refusal is logged with the host, the address and
// upstream-address-refused (C02-streams-and-tls, "Upstream addresses").
func TestDialRefusesOneBadAddress(t *testing.T) {
	h := newHarness([]string{"8.8.8.8", "169.254.169.254", "1.1.1.1"}, nil)
	_, err := h.d.Dial(t.Context(), "evil.example", 443)
	r := wantRefusal(t, err, RuleAddressRefused)
	if r.Addr != netip.MustParseAddr("169.254.169.254") {
		t.Fatalf("refused address %s, want 169.254.169.254", r.Addr)
	}
	if len(h.conn.dialed) != 0 {
		t.Fatalf("dialed %v after a refusal", h.conn.dialed)
	}
	recs := h.log.records(t)
	if len(recs) != 1 {
		t.Fatalf("%d log records, want 1: %v", len(recs), recs)
	}
	rec := recs[0]
	if rec["rule"] != string(RuleAddressRefused) || rec["host"] != "evil.example" || rec["addr"] != "169.254.169.254" {
		t.Fatalf("log record %v, want the rule, the host and the address", rec)
	}
}

// A good name is dialed at the checked address, with one lookup.
func TestDialGoodName(t *testing.T) {
	h := newHarness([]string{"2606:4700:4700::1111", "8.8.8.8"}, nil)
	conn, err := h.d.Dial(t.Context(), "good.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if h.res.calls != 1 {
		t.Fatalf("%d lookups, want 1", h.res.calls)
	}
	if want := []string{"[2606:4700:4700::1111]:443"}; !slices.Equal(h.conn.dialed, want) {
		t.Fatalf("dialed %v, want %v", h.conn.dialed, want)
	}
}

// The checked addresses are dialed in order, one at a time, until one
// connects, and at most maxAttempts of them. When none connects the
// rule is upstream-connect-failed, and the lookup still ran once.
func TestDialInOrder(t *testing.T) {
	h := newHarness([]string{"8.8.8.8", "8.8.4.4"}, nil)
	h.conn.fail["8.8.8.8:443"] = true
	conn, err := h.d.Dial(t.Context(), "good.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if want := []string{"8.8.8.8:443", "8.8.4.4:443"}; !slices.Equal(h.conn.dialed, want) {
		t.Fatalf("dialed %v, want %v", h.conn.dialed, want)
	}

	addrs := []string{"1.0.0.1", "1.0.0.2", "1.0.0.3", "1.0.0.4", "1.0.0.5"}
	h = newHarness(addrs, nil)
	for _, a := range addrs {
		h.conn.fail[a+":443"] = true
	}
	_, err = h.d.Dial(t.Context(), "down.example", 443)
	r := wantRefusal(t, err, RuleConnectFailed)
	if len(h.conn.dialed) != maxAttempts || r.Addr != netip.MustParseAddr("1.0.0.4") {
		t.Fatalf("dialed %v and named %s, want the first %d", h.conn.dialed, r.Addr, maxAttempts)
	}
	if h.res.calls != 1 {
		t.Fatalf("%d lookups, want 1", h.res.calls)
	}
	if recs := h.log.records(t); len(recs) != 1 || recs[0]["rule"] != string(RuleConnectFailed) || recs[0]["host"] != "down.example" {
		t.Fatalf("log records %v, want one with upstream-connect-failed and the host", recs)
	}
}

// A failed lookup, and one with no address, give
// upstream-resolve-failed and dial nothing.
func TestDialResolveFailed(t *testing.T) {
	h := newHarness(nil, nil)
	_, err := h.d.Dial(t.Context(), "empty.example", 443)
	wantRefusal(t, err, RuleResolveFailed)

	h = newHarness(nil, nil)
	h.res.err = errors.New("no such host")
	_, err = h.d.Dial(t.Context(), "missing.example", 443)
	wantRefusal(t, err, RuleResolveFailed)
	if len(h.conn.dialed) != 0 {
		t.Fatalf("dialed %v", h.conn.dialed)
	}
	if recs := h.log.records(t); len(recs) != 1 || recs[0]["rule"] != string(RuleResolveFailed) {
		t.Fatalf("log records %v, want one with upstream-resolve-failed", recs)
	}
}

// The host's own networks: an address assigned to an interface, an
// address in an on-link IPv4 prefix, a global IPv6 address in an
// on-link prefix, and the IPv4-mapped form of an interface address are
// refused. An address outside them passes. A host whose networks
// can't be read refuses every name.
func TestDialHostNetworks(t *testing.T) {
	nets := []string{"198.51.100.7/24", "2a01:4f8:1:2::10/64", "203.0.113.9/32"}
	tests := []struct {
		addr    string
		refused bool
	}{
		{"198.51.100.7", true},
		{"203.0.113.9", true},
		{"198.51.100.200", true},
		{"2a01:4f8:1:2::99", true},
		{"::ffff:198.51.100.7", true},
		{"198.51.101.1", false},
		{"203.0.113.10", false},
		{"2a01:4f8:1:3::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			h := newHarness([]string{tt.addr}, nets)
			conn, err := h.d.Dial(t.Context(), "lan.example", 443)
			if !tt.refused {
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				return
			}
			wantRefusal(t, err, RuleAddressRefused)
			if len(h.conn.dialed) != 0 {
				t.Fatalf("dialed %v", h.conn.dialed)
			}
		})
	}

	h := newHarness([]string{"8.8.8.8"}, nil)
	h.d.networks = func() ([]netip.Prefix, error) { return nil, errors.New("no route socket") }
	_, err := h.d.Dial(t.Context(), "good.example", 443)
	wantRefusal(t, err, RuleAddressRefused)
	if len(h.conn.dialed) != 0 {
		t.Fatalf("dialed %v", h.conn.dialed)
	}
}

// The host's networks are read at each Dial, not once.
func TestDialReadsNetworksEachTime(t *testing.T) {
	h := newHarness([]string{"198.51.100.7"}, nil)
	reads := 0
	h.d.networks = func() ([]netip.Prefix, error) {
		reads++
		if reads == 1 {
			return nil, nil
		}
		return []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, nil
	}
	conn, err := h.d.Dial(t.Context(), "lan.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	_, err = h.d.Dial(t.Context(), "lan.example", 443)
	wantRefusal(t, err, RuleAddressRefused)
}

// With the defaults of New, a name that resolves to 127.0.0.1 is
// refused and no connection reaches a listener there. With the
// loopback range taken out, which only a test in this package can do,
// the same Dial connects.
func TestDialLoopback(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()
	port := netip.MustParseAddrPort(ln.Addr().String()).Port()

	d := New(slog.New(slog.DiscardHandler))
	_, err = d.Dial(t.Context(), "127.0.0.1", port)
	wantRefusal(t, err, RuleAddressRefused)

	d.ranges = slices.DeleteFunc(slices.Clone(defaultRanges), func(r addrRange) bool { return r.name == "ipv4-loopback" })
	d.networks = func() ([]netip.Prefix, error) { return nil, nil }
	conn, err := d.Dial(t.Context(), "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	<-accepted
	select {
	case <-accepted:
		t.Fatal("the refused Dial reached the listener")
	default:
	}
}

// The real reader of the host's networks covers the loopback address.
func TestHostNetworks(t *testing.T) {
	nets, err := hostNetworks()
	if err != nil {
		t.Fatal(err)
	}
	_, v4 := onHostNetwork(nets, netip.MustParseAddr("127.0.0.1"))
	_, v6 := onHostNetwork(nets, netip.MustParseAddr("::1"))
	if !v4 && !v6 {
		t.Fatalf("host networks %v hold neither 127.0.0.1 nor ::1", nets)
	}
}

func TestInterfacePrefix(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{"ipv4 in 16 bytes", &net.IPNet{IP: net.ParseIP("192.0.2.5"), Mask: net.CIDRMask(24, 32)}, "192.0.2.5/24"},
		{"ipv6", &net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)}, "2001:db8::5/64"},
		{"mask not a prefix", &net.IPNet{IP: net.ParseIP("192.0.2.5").To4(), Mask: net.IPv4Mask(255, 0, 255, 0)}, ""},
		{"ipv4 with a 16-byte mask", &net.IPNet{IP: net.ParseIP("192.0.2.5"), Mask: net.CIDRMask(120, 128)}, "192.0.2.5/24"},
		{"ip addr", &net.IPAddr{IP: net.ParseIP("192.0.2.5")}, "192.0.2.5/32"},
		{"not an ip", &net.UnixAddr{Name: "x", Net: "unix"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := interfacePrefix(tt.addr)
			if ok != (tt.want != "") || (ok && p.String() != tt.want) {
				t.Fatalf("interfacePrefix = %v, %v; want %q", p, ok, tt.want)
			}
		})
	}
}

// A caller that gives up mid-Dial gets its context's error, the loop
// stops, and no refusal is logged, so the rule's window stays unused.
func TestDialCancelled(t *testing.T) {
	h := newHarness([]string{"8.8.8.8", "8.8.4.4"}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.conn.before = cancel
	h.conn.fail["8.8.8.8:443"] = true
	_, err := h.d.Dial(ctx, "good.example", 443)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	var r *Refusal
	if errors.As(err, &r) {
		t.Fatalf("a cancelled Dial gave refusal %v", r)
	}
	if want := []string{"8.8.8.8:443"}; !slices.Equal(h.conn.dialed, want) {
		t.Fatalf("dialed %v, want %v", h.conn.dialed, want)
	}
	if recs := h.log.records(t); len(recs) != 0 {
		t.Fatalf("log records %v, want none", recs)
	}
}
