package egressmsg

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// addr1 is 198.18.0.1, the first address of SyntheticRange.
const addr1 = 0xC6120001

func marshal(tb testing.TB, m proto.Message) []byte {
	tb.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// withField appends an unknown varint field to a record.
func withField(b []byte, num protowire.Number) []byte {
	return protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), b...), num, protowire.VarintType), 1)
}

func wantCheck(t *testing.T, r *Refusal, want Check) {
	t.Helper()
	if r == nil {
		t.Fatalf("accepted, want refused with %s", want)
	}
	if r.Check != want {
		t.Fatalf("refused with %s (%v), want %s", r.Check, r, want)
	}
}

func TestConstants(t *testing.T) {
	if SyntheticRange != netd.DefaultSynthetic {
		t.Errorf("SyntheticRange = %v, netd.DefaultSynthetic = %v", SyntheticRange, netd.DefaultSynthetic)
	}
	if MaxWildcardNames != policy.MaxWildcardBudget {
		t.Errorf("MaxWildcardNames = %d, policy.MaxWildcardBudget = %d", MaxWildcardNames, policy.MaxWildcardBudget)
	}
	if MaxEntries != 1<<(32-SyntheticRange.Bits()) {
		t.Errorf("MaxEntries = %d, the range has %d addresses", MaxEntries, 1<<(32-SyntheticRange.Bits()))
	}
	if a := AddrFromWire(addr1); a.String() != "198.18.0.1" {
		t.Errorf("AddrFromWire(0xC6120001) = %v", a)
	}
	if w, ok := AddrToWire(AddrFromWire(addr1)); !ok || w != addr1 {
		t.Errorf("AddrToWire = %#x, %v", w, ok)
	}
}

func nameReportMsg(name string, addr uint32) *egressv1.NetdMessage {
	return &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
		NameReport: &egressv1.NameReport{Name: name, Address: addr},
	}}
}

func streamMsg(change func(*egressv1.Stream)) *egressv1.NetdMessage {
	s := &egressv1.Stream{Generation: 3, Address: addr1, Port: 443, StreamId: 7}
	if change != nil {
		change(s)
	}
	return &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Stream{Stream: s}}
}

func TestDecodeNetdMessage(t *testing.T) {
	report := marshal(t, nameReportMsg("api.example.com", addr1))
	release := marshal(t, &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{Release: &egressv1.Release{Address: addr1 + 1}}})
	stream := marshal(t, streamMsg(nil))
	// Field 5 of Stream is kept for the X14 label, and this build
	// refuses it.
	labeled := withNestedField(t, stream, 5)

	for _, tc := range []struct {
		name   string
		record []byte
		fd     bool
		want   Check
	}{
		{"empty", nil, false, CheckMessageForm},
		{"over the cap", make([]byte, MaxRecord+1), false, CheckMessageForm},
		{"garbage", []byte{0xff, 0xff}, false, CheckMessageForm},
		{"no kind", withField(nil, 9), false, CheckMessageForm},
		{"unknown top field", withField(report, 9), false, CheckMessageForm},
		{"label field kept", labeled, true, CheckMessageForm},
		{"report with descriptor", report, true, CheckMessageForm},
		{"release with descriptor", release, true, CheckMessageForm},
		{"stream without descriptor", stream, false, CheckMessageForm},
		{"capital letter", marshal(t, nameReportMsg("API.example.com", addr1)), false, CheckNameForm},
		{"trailing dot", marshal(t, nameReportMsg("api.example.com.", addr1)), false, CheckNameForm},
		{"empty name", marshal(t, nameReportMsg("", addr1)), false, CheckNameForm},
		{"literal address", marshal(t, nameReportMsg("10.0.0.1", addr1)), false, CheckNameForm},
		{"long name", marshal(t, nameReportMsg(strings.Repeat("a.", 127)+"aa", addr1)), false, CheckNameForm},
		{"report outside range", marshal(t, nameReportMsg("a.example", 0x0A000001)), false, CheckSyntheticRange},
		{"report past range", marshal(t, nameReportMsg("a.example", 0xC6140000)), false, CheckSyntheticRange},
		{"stream generation 0", marshal(t, streamMsg(func(s *egressv1.Stream) { s.Generation = 0 })), true, CheckMessageForm},
		{"stream id 0", marshal(t, streamMsg(func(s *egressv1.Stream) { s.StreamId = 0 })), true, CheckMessageForm},
		{"port 0", marshal(t, streamMsg(func(s *egressv1.Stream) { s.Port = 0 })), true, CheckMessageForm},
		{"port 65536", marshal(t, streamMsg(func(s *egressv1.Stream) { s.Port = 65536 })), true, CheckMessageForm},
		{"stream outside range", marshal(t, streamMsg(func(s *egressv1.Stream) { s.Address = 0x7F000001 })), true, CheckSyntheticRange},
		{"another direction's message", marshal(t, &egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{StreamReset: &egressv1.StreamReset{StreamId: 1}}}), false, CheckMessageForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r := DecodeNetdMessage(tc.record, tc.fd)
			wantCheck(t, r, tc.want)
		})
	}
}

func TestDecodeNetdMessageAccepts(t *testing.T) {
	release := &egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Release{Release: &egressv1.Release{Address: addr1 + 1}}}
	for _, tc := range []struct {
		msg  *egressv1.NetdMessage
		fd   bool
		want string
	}{
		{nameReportMsg("api.example.com", addr1), false, "name-report api.example.com 198.18.0.1 0 0 0"},
		{release, false, "release  198.18.0.2 0 0 0"},
		{streamMsg(nil), true, "stream  198.18.0.1 3 443 7"},
		{nameReportMsg("a.example", 0xC613FFFF), false, "name-report a.example 198.19.255.255 0 0 0"},
	} {
		m, r := DecodeNetdMessage(marshal(t, tc.msg), tc.fd)
		if r != nil {
			t.Fatalf("%s: %v", tc.want, r)
		}
		got := fmt.Sprintf("%v %s %v %d %d %d", m.Kind, m.Name.String(), m.Addr, m.Generation, m.Port, m.StreamID)
		if got != tc.want {
			t.Errorf("decoded %q, want %q", got, tc.want)
		}
	}
}

// withNestedField appends an unknown field to the one message field of
// a record with a oneof, by re-encoding the inner message.
func withNestedField(t *testing.T, record []byte, num protowire.Number) []byte {
	t.Helper()
	outer, _, n := protowire.ConsumeTag(record)
	if n < 0 {
		t.Fatal("no tag")
	}
	inner, m := protowire.ConsumeBytes(record[n:])
	if m < 0 {
		t.Fatal("no bytes")
	}
	inner = withField(inner, num)
	return protowire.AppendBytes(protowire.AppendTag(nil, outer, protowire.BytesType), inner)
}

// TestMaxNameReport checks the largest name report and stream fit a
// record many times over, as handoff.proto says.
func TestMaxNameReport(t *testing.T) {
	name := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	if len(name) != 253 {
		t.Fatalf("name of %d bytes", len(name))
	}
	b, err := Marshal(nameReportMsg(name, 0xC613FFFF))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 264 {
		t.Errorf("largest name report is %d bytes, handoff.proto says 264", len(b))
	}
	if _, r := DecodeNetdMessage(b, false); r != nil {
		t.Fatal(r)
	}
	s := streamMsg(func(s *egressv1.Stream) {
		s.Generation, s.StreamId, s.Port, s.Address = ^uint64(0), ^uint64(0), 65535, 0xC613FFFF
	})
	if b, err := Marshal(s); err != nil || len(b) > 64 {
		t.Errorf("largest stream is %d bytes, %v", len(b), err)
	}
}

func TestMarshalRefusesOverCap(t *testing.T) {
	m := &egressv1.ProxydStartPayload{WildcardNames: []*egressv1.WildcardNames{{Vm: "work", Names: make([]string, 3000)}}}
	if _, err := Marshal(m); err == nil {
		t.Fatal("Marshal accepted a record over MaxRecord")
	}
}

func TestDecodeProxydMessage(t *testing.T) {
	reset := func(id uint64) []byte {
		return marshal(t, &egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{StreamReset: &egressv1.StreamReset{StreamId: id}}})
	}
	if id, r := DecodeProxydMessage(reset(9), false); r != nil || id != 9 {
		t.Fatalf("reset = %d, %v", id, r)
	}
	for name, tc := range map[string]struct {
		rec []byte
		fd  bool
	}{
		"id 0":          {reset(0), false},
		"descriptor":    {reset(9), true},
		"empty":         {nil, false},
		"no kind":       {withField(nil, 2), false},
		"unknown field": {withField(reset(9), 2), false},
		"nested field":  {withNestedField(t, reset(9), 2), false},
		"netd message":  {marshal(t, nameReportMsg("a.example", addr1)), false},
	} {
		t.Run(name, func(t *testing.T) {
			_, r := DecodeProxydMessage(tc.rec, tc.fd)
			wantCheck(t, r, CheckMessageForm)
		})
	}
}

func TestRefusalText(t *testing.T) {
	_, r := DecodeNetdMessage(marshal(t, nameReportMsg("Secret.example.com", addr1)), false)
	if r == nil || strings.Contains(strings.ToLower(r.Error()), "secret") {
		t.Fatalf("refusal %v holds the name", r)
	}
	_, r = VM("vm", "evil\x1b[2J"+strings.Repeat("x", 100))
	if r == nil || strings.Contains(r.Error(), "\x1b") || len(r.Error()) > 120 {
		t.Fatalf("refusal %q holds the VM unescaped or whole", r)
	}
	attrs := r.LogAttrs()
	if len(attrs) != 3 || attrs[0].Value.String() != string(CheckVMSlot) || attrs[1].Value.String() != "vm" {
		t.Fatalf("LogAttrs = %v", attrs)
	}
	_, r = DecodeNetdMessage([]byte{0xff}, false)
	if r == nil || errors.Unwrap(r) == nil {
		t.Fatalf("a decoding refusal %v has no cause", r)
	}
}
