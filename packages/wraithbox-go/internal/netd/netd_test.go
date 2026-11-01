package netd_test

import (
	"bytes"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*netd.Config)
	}{
		{"short guest MAC", func(c *netd.Config) { c.GuestMAC = c.GuestMAC[:5] }},
		{"multicast gateway MAC", func(c *netd.Config) { c.GatewayMAC = net.HardwareAddr{1, 0, 0x5e, 0, 0, 1} }},
		{"shared MAC", func(c *netd.Config) { c.GatewayMAC = c.GuestMAC }},
		{"IPv6 subnet", func(c *netd.Config) { c.Subnet = netip.MustParsePrefix("fd00::/64") }},
		{"subnet of 2", func(c *netd.Config) { c.Subnet = netip.MustParsePrefix("192.168.127.0/31") }},
		{"subnet not masked", func(c *netd.Config) { c.Subnet = netip.MustParsePrefix("192.168.127.1/24") }},
		{"gateway outside", func(c *netd.Config) { c.Gateway = netip.MustParseAddr("10.0.0.1") }},
		{"leased is the network", func(c *netd.Config) { c.Leased = netip.MustParseAddr("192.168.127.0") }},
		{"leased is the broadcast", func(c *netd.Config) { c.Leased = netip.MustParseAddr("192.168.127.255") }},
		{"leased is the gateway", func(c *netd.Config) { c.Leased = c.Gateway }},
		{"synthetic overlaps", func(c *netd.Config) { c.Synthetic = netip.MustParsePrefix("192.168.0.0/16") }},
		{"no synthetic pool", func(c *netd.Config) { c.Synthetic = netip.Prefix{} }},
		{"MTU under 1500", func(c *netd.Config) { c.MTU = 1499 }},
		{"MTU over 65535", func(c *netd.Config) { c.MTU = 65536 }},
		{"no half-open cap", func(c *netd.Config) { c.Caps.HalfOpenTCP = 0 }},
		{"no established cap", func(c *netd.Config) { c.Caps.EstablishedTCP = 0 }},
		{"no frame cap", func(c *netd.Config) { c.Caps.QueueFrames = 0 }},
		{"small TCP buffer", func(c *netd.Config) { c.Caps.TCPBuffer = 4095 }},
		{"byte cap under a frame", func(c *netd.Config) { c.Caps.QueueBytes = 65548 }},
	}
	good := nt.Config()
	if err := good.Validate(); err != nil {
		t.Fatalf("the test configuration: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := nt.Config()
			tc.change(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("Validate took it")
			}
		})
	}
}

func TestBroadcastOf(t *testing.T) {
	for p, want := range map[string]string{
		"192.168.127.0/24": "192.168.127.255",
		"10.0.0.0/8":       "10.255.255.255",
		"172.16.4.0/30":    "172.16.4.3",
	} {
		if got := netd.BroadcastOf(netip.MustParsePrefix(p)).String(); got != want {
			t.Errorf("BroadcastOf(%s) = %s, want %s", p, got, want)
		}
	}
}

type recorded struct {
	d     netd.Decision
	rule  netd.Rule
	count uint64
}

type eventLog []recorded

func (e *eventLog) Event(d netd.Decision, rule netd.Rule, count uint64, _ []slog.Attr) {
	*e = append(*e, recorded{d, rule, count})
}

// TestRecorder checks that every drop is counted and passed on, and that
// the log record names the decision and the rule.
func TestRecorder(t *testing.T) {
	var buf bytes.Buffer
	var ev eventLog
	r := netd.NewRecorderWith(slog.New(slog.NewTextHandler(&buf, nil)), &ev)
	r.Record(netd.Drop, netd.RuleFrameMAC, slog.Int("bytes", 60))
	r.Record(netd.Drop, netd.RuleFrameMAC)
	r.Record(netd.Reset, netd.RuleTCPCap)
	if r.Count(netd.RuleFrameMAC) != 2 || r.Count(netd.RuleTCPCap) != 1 || r.Count(netd.RuleARPForm) != 0 {
		t.Fatalf("counts %d, %d", r.Count(netd.RuleFrameMAC), r.Count(netd.RuleTCPCap))
	}
	want := eventLog{{netd.Drop, netd.RuleFrameMAC, 1}, {netd.Drop, netd.RuleFrameMAC, 2}, {netd.Reset, netd.RuleTCPCap, 1}}
	if len(ev) != len(want) {
		t.Fatalf("events %v", ev)
	}
	for i := range want {
		if ev[i] != want[i] {
			t.Fatalf("event %d = %v, want %v", i, ev[i], want[i])
		}
	}
	for _, s := range []string{"decision=drop rule=frame-mac count=1 bytes=60", "decision=reset rule=tcp-cap count=1"} {
		if !strings.Contains(buf.String(), s) {
			t.Fatalf("log %q lacks %q", buf.String(), s)
		}
	}
}

// TestRecorderRateLimit checks that NewRecorder logs one record per rule
// per window, and still counts every drop.
func TestRecorderRateLimit(t *testing.T) {
	var buf bytes.Buffer
	r := netd.NewRecorder(slog.New(slog.NewTextHandler(&buf, nil)), nil)
	for range 100 {
		r.Record(netd.Drop, netd.RuleFrameSize)
	}
	if n := strings.Count(buf.String(), "rule=frame-size"); n != 1 {
		t.Fatalf("%d records, want 1", n)
	}
	if r.Count(netd.RuleFrameSize) != 100 {
		t.Fatalf("count %d", r.Count(netd.RuleFrameSize))
	}
}
