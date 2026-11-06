package egressmsg

import (
	"net"
	"net/netip"
	"testing"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

type controlCase struct {
	name string
	msg  proto.Message
	fd   bool
	want Check // "" for accepted
}

func runControl(t *testing.T, decode func([]byte, bool) *Refusal, cases []controlCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := decode(marshal(t, tc.msg), tc.fd)
			if tc.want == "" {
				if r != nil {
					t.Fatal(r)
				}
				return
			}
			wantCheck(t, r, tc.want)
		})
	}
}

func TestDecodeHostdToProxyd(t *testing.T) {
	start := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ProxydStart{ProxydStart: &egressv1.ProxydStart{}}}
	sock := func(vm string, gen uint64) proto.Message {
		return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_HandoffSocket{HandoffSocket: &egressv1.HandoffSocket{Vm: vm, Generation: gen}}}
	}
	change := func(vm string, seq uint64) proto.Message {
		return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_PolicyChange{PolicyChange: &egressv1.PolicyChange{Vm: vm, Sequence: seq}}}
	}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeHostdToProxyd(b, fd); return r }, []controlCase{
		{"start", start, true, ""},
		{"start without payload", start, false, CheckMessageForm},
		{"socket", sock("work", 1), true, ""},
		{"socket isolated", sock("isolated", 4), true, ""},
		{"socket without descriptor", sock("work", 1), false, CheckMessageForm},
		{"socket for build VM", sock("build", 1), true, CheckVMSlot},
		{"socket for no VM", sock("", 1), true, CheckVMSlot},
		{"socket generation 0", sock("work", 0), true, CheckMessageForm},
		{"change", change("work", 2), true, ""},
		{"change sequence 0", change("work", 0), true, CheckMessageForm},
		{"change without payload", change("work", 2), false, CheckMessageForm},
		{"change bad VM", change("Work", 2), true, CheckVMSlot},
		{"no kind", &egressv1.HostdToProxyd{}, false, CheckMessageForm},
	})
	// Field 1 of HostdToNetd is NetdStart, whose VM and generation are
	// fields ProxydStart doesn't have.
	b := marshal(t, &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: &egressv1.NetdStart{Vm: "work", Generation: 1}}})
	if _, r := DecodeHostdToProxyd(b, true); r == nil {
		t.Fatal("accepted a NetdStart as a ProxydStart")
	}
}

func TestDecodeHostdToNetd(t *testing.T) {
	start := func(vm string, gen uint64) proto.Message {
		return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: NewNetdStart(vm, gen, testLink())}}
	}
	link := func(edit func(l *Link)) proto.Message {
		l := testLink()
		edit(&l)
		return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: NewNetdStart("work", 1, l)}}
	}
	wire := func(edit func(m *egressv1.NetdStart)) proto.Message {
		m := NewNetdStart("work", 1, testLink())
		edit(m)
		return &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: m}}
	}
	sock := &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_HandoffSocket{HandoffSocket: &egressv1.HandoffSocket{Vm: "work", Generation: 2}}}
	change := &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{PolicyChange: &egressv1.PolicyChange{Vm: "isolated", Sequence: 1}}}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeHostdToNetd(b, fd); return r }, []controlCase{
		{"start", start("work", 1), true, ""},
		{"start without payload", start("work", 1), false, CheckMessageForm},
		{"start generation 0", start("work", 0), true, CheckMessageForm},
		{"start bad VM", start("../work", 1), true, CheckVMSlot},
		{"no addressing", &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: &egressv1.NetdStart{Vm: "work", Generation: 1}}}, true, CheckAddressing},
		{"short guest MAC", link(func(l *Link) { l.GuestMAC = l.GuestMAC[:5] }), true, CheckAddressing},
		{"multicast gateway MAC", link(func(l *Link) { l.GatewayMAC = net.HardwareAddr{1, 0, 0, 0, 0, 1} }), true, CheckAddressing},
		{"one MAC", link(func(l *Link) { l.GatewayMAC = l.GuestMAC }), true, CheckAddressing},
		{"prefix 31", wire(func(m *egressv1.NetdStart) { m.PrefixLen = 31 }), true, CheckAddressing},
		{"prefix 7", wire(func(m *egressv1.NetdStart) { m.PrefixLen = 7 }), true, CheckAddressing},
		{"host bits", wire(func(m *egressv1.NetdStart) { m.Subnet++ }), true, CheckAddressing},
		{"gateway outside", link(func(l *Link) { l.Gateway = netip.MustParseAddr("10.0.0.1") }), true, CheckAddressing},
		{"leased network", link(func(l *Link) { l.Leased = netip.MustParseAddr("192.168.127.0") }), true, CheckAddressing},
		{"leased broadcast", link(func(l *Link) { l.Leased = netip.MustParseAddr("192.168.127.255") }), true, CheckAddressing},
		{"leased gateway", link(func(l *Link) { l.Leased = l.Gateway }), true, CheckAddressing},
		{"socket", sock, true, ""},
		{"socket without descriptor", sock, false, CheckMessageForm},
		{"change", change, true, ""},
		{"change without payload", change, false, CheckMessageForm},
		{"no kind", &egressv1.HostdToNetd{}, false, CheckMessageForm},
	})
}

func TestDecodeProxydToHostd(t *testing.T) {
	ack := func(vm string, gen uint64) proto.Message {
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketAck{HandoffSocketAck: &egressv1.HandoffSocketAck{Vm: vm, Generation: gen}}}
	}
	pack := func(seq uint64) proto.Message {
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: seq}}}
	}
	closed := func(rule, check string) proto.Message {
		return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketClosed{HandoffSocketClosed: &egressv1.HandoffSocketClosed{
			Vm: "work", Generation: 3, Rule: rule, Check: check,
		}}}
	}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeProxydToHostd(b, fd); return r }, []controlCase{
		{"ack", ack("work", 1), false, ""},
		{"ack with descriptor", ack("work", 1), true, CheckMessageForm},
		{"ack bad VM", ack("work2", 1), false, CheckVMSlot},
		{"ack generation 0", ack("work", 0), false, CheckMessageForm},
		{"policy ack", pack(5), false, ""},
		{"policy ack 0", pack(0), false, CheckMessageForm},
		{"policy ack with descriptor", pack(5), true, CheckMessageForm},
		{"closed name-form", closed(RuleNameReportMalformed, "name-form"), false, ""},
		{"closed message-form", closed(RuleNameReportMalformed, "message-form"), false, ""},
		{"closed synthetic-range", closed(RuleNameReportMalformed, "synthetic-range"), false, ""},
		{"closed other rule", closed("name-report-refused", "name-form"), false, CheckMessageForm},
		{"closed other check", closed(RuleNameReportMalformed, "vm-slot"), false, CheckMessageForm},
		{"closed empty check", closed(RuleNameReportMalformed, ""), false, CheckMessageForm},
		{"closed with descriptor", closed(RuleNameReportMalformed, "name-form"), true, CheckMessageForm},
		{"no kind", &egressv1.ProxydToHostd{}, false, CheckMessageForm},
	})
}

func TestDecodeNetdToHostd(t *testing.T) {
	pack := func(vm string, seq uint64) proto.Message {
		return &egressv1.NetdToHostd{Kind: &egressv1.NetdToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: vm, Sequence: seq}}}
	}
	runControl(t, func(b []byte, fd bool) *Refusal { _, r := DecodeNetdToHostd(b, fd); return r }, []controlCase{
		{"ack", pack("work", 1), false, ""},
		{"ack with descriptor", pack("work", 1), true, CheckMessageForm},
		{"ack bad VM", pack("x", 1), false, CheckVMSlot},
		{"ack 0", pack("work", 0), false, CheckMessageForm},
		{"no kind", &egressv1.NetdToHostd{}, false, CheckMessageForm},
		// Field 2 of ProxydToHostd is PolicyAck too, a field NetdToHostd
		// doesn't have.
		{"proxyd's message", &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: 1}}}, false, CheckMessageForm},
	})
}

// testLink is the link of the netd tests (internal/netd/netdtest).
func testLink() Link {
	return Link{
		GuestMAC: net.HardwareAddr{2, 0, 0, 0, 0, 2}, GatewayMAC: net.HardwareAddr{2, 0, 0, 0, 0, 1},
		Subnet:  netip.MustParsePrefix("192.168.127.0/24"),
		Gateway: netip.MustParseAddr("192.168.127.1"), Leased: netip.MustParseAddr("192.168.127.2"),
	}
}

// TestLinkRoundTrip checks that LinkOf returns the link NewNetdStart
// wrote.
func TestLinkRoundTrip(t *testing.T) {
	want := testLink()
	got, r := LinkOf(NewNetdStart("work", 1, want))
	if r != nil {
		t.Fatal(r)
	}
	if got.GuestMAC.String() != want.GuestMAC.String() || got.GatewayMAC.String() != want.GatewayMAC.String() ||
		got.Subnet != want.Subnet || got.Gateway != want.Gateway || got.Leased != want.Leased {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestMACOrder: with both MACs wrong, the guest's is refused each time.
func TestMACOrder(t *testing.T) {
	l := testLink()
	l.GuestMAC, l.GatewayMAC = l.GuestMAC[:5], l.GatewayMAC[:5]
	for range 20 {
		if _, r := LinkOf(NewNetdStart("work", 1, l)); r == nil || r.Field != "netd_start.guest_mac" {
			t.Fatalf("refusal %v, want netd_start.guest_mac", r)
		}
	}
}

func TestStartOrder(t *testing.T) {
	netdStart := &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{NetdStart: &egressv1.NetdStart{}}}
	netdPolicy := &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{PolicyChange: &egressv1.PolicyChange{}}}
	proxydStart := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ProxydStart{ProxydStart: &egressv1.ProxydStart{}}}
	proxydSocket := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_HandoffSocket{HandoffSocket: &egressv1.HandoffSocket{}}}

	// In order: the start, then other messages.
	var ok StartOrder
	for i, m := range []*egressv1.HostdToNetd{netdStart, netdPolicy, netdPolicy} {
		if r := ok.NextNetd(m); r != nil {
			t.Fatalf("message %d: %v", i, r)
		}
	}
	var okProxyd StartOrder
	for i, m := range []*egressv1.HostdToProxyd{proxydStart, proxydSocket} {
		if r := okProxyd.NextProxyd(m); r != nil {
			t.Fatalf("proxyd message %d: %v", i, r)
		}
	}

	// A message before the start is refused, and so is every later
	// message, the start included.
	var early StartOrder
	wantCheck(t, early.NextProxyd(proxydSocket), CheckMessageForm)
	wantCheck(t, early.NextProxyd(proxydStart), CheckMessageForm)
	wantCheck(t, early.NextProxyd(proxydSocket), CheckMessageForm)

	// A second start is refused, and the channel stays refused.
	var twice StartOrder
	if r := twice.NextNetd(netdStart); r != nil {
		t.Fatal(r)
	}
	wantCheck(t, twice.NextNetd(netdStart), CheckMessageForm)
	wantCheck(t, twice.NextNetd(netdPolicy), CheckMessageForm)

	// A nil message isn't a start message.
	var none StartOrder
	wantCheck(t, none.NextNetd(nil), CheckMessageForm)
}
