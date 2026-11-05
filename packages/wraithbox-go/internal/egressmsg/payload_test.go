package egressmsg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

func payloadBytes(t *testing.T, m proto.Message) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WritePayload(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func lengthOnly(n uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, n)
}

func TestReadPayload(t *testing.T) {
	good := &egressv1.NetdStartPayload{
		Entries: []*egressv1.MappingEntry{{Name: "a.example", Address: addr1}},
		Debited: []string{"b.example"}, DebitCount: 2,
	}
	b := payloadBytes(t, good)
	got := &egressv1.NetdStartPayload{}
	if r := readPayload(bytes.NewReader(b), got); r != nil {
		t.Fatal(r)
	}
	if !proto.Equal(got, good) {
		t.Fatalf("read %v, wrote %v", got, good)
	}
	if r := readPayload(bytes.NewReader(lengthOnly(0)), &egressv1.NetdStartPayload{}); r != nil {
		t.Fatalf("an empty payload: %v", r)
	}

	for name, rec := range map[string][]byte{
		"nothing":        nil,
		"short length":   {0, 0},
		"over the cap":   lengthOnly(MaxPayload + 1),
		"largest length": lengthOnly(^uint32(0)),
		"cut payload":    b[:len(b)-1],
		"a byte after":   append(append([]byte(nil), b...), 0),
	} {
		t.Run(name, func(t *testing.T) {
			wantCheck(t, readPayload(bytes.NewReader(rec), &egressv1.NetdStartPayload{}), CheckPayload)
		})
	}
	t.Run("unknown field", func(t *testing.T) {
		inner := withField(marshal(t, good), 9)
		rec := append(lengthOnly(uint32(len(inner))), inner...)
		wantCheck(t, readPayload(bytes.NewReader(rec), &egressv1.NetdStartPayload{}), CheckMessageForm)
	})
}

func TestCheckNetdStart(t *testing.T) {
	entry := func(name string, addr uint32) *egressv1.MappingEntry {
		return &egressv1.MappingEntry{Name: name, Address: addr}
	}
	if r := CheckNetdStart(&egressv1.NetdStartPayload{}); r != nil {
		t.Fatalf("empty: %v", r)
	}
	ok := &egressv1.NetdStartPayload{
		Entries: []*egressv1.MappingEntry{entry("a.example", addr1), entry("a.example", addr1+1)},
		Debited: []string{"a.example", "b.example"}, DebitCount: MaxWildcardNames,
	}
	if r := CheckNetdStart(ok); r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		name string
		p    *egressv1.NetdStartPayload
		want Check
	}{
		{"address twice", &egressv1.NetdStartPayload{Entries: []*egressv1.MappingEntry{entry("a.example", addr1), entry("b.example", addr1)}}, CheckMessageForm},
		{"entry outside range", &egressv1.NetdStartPayload{Entries: []*egressv1.MappingEntry{entry("a.example", 1)}}, CheckSyntheticRange},
		{"entry bad name", &egressv1.NetdStartPayload{Entries: []*egressv1.MappingEntry{entry("A.example", addr1)}}, CheckNameForm},
		{"too many entries", &egressv1.NetdStartPayload{Entries: make([]*egressv1.MappingEntry, MaxEntries+1)}, CheckMessageForm},
		{"debited twice", &egressv1.NetdStartPayload{Debited: []string{"a.example", "a.example"}}, CheckMessageForm},
		{"debited bad name", &egressv1.NetdStartPayload{Debited: []string{"a.example."}}, CheckNameForm},
		{"too many debited", &egressv1.NetdStartPayload{Debited: make([]string, MaxWildcardNames+1)}, CheckMessageForm},
		{"debit count over", &egressv1.NetdStartPayload{DebitCount: MaxWildcardNames + 1}, CheckMessageForm},
	} {
		t.Run(tc.name, func(t *testing.T) { wantCheck(t, CheckNetdStart(tc.p), tc.want) })
	}
}

func TestCheckProxydStart(t *testing.T) {
	set := func(vm string, names ...string) *egressv1.WildcardNames {
		return &egressv1.WildcardNames{Vm: vm, Names: names}
	}
	ok := &egressv1.ProxydStartPayload{WildcardNames: []*egressv1.WildcardNames{set("work", "a.example"), set("isolated", "a.example")}}
	if r := CheckProxydStart(ok); r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		name string
		sets []*egressv1.WildcardNames
		want Check
	}{
		{"VM twice", []*egressv1.WildcardNames{set("work"), set("work")}, CheckMessageForm},
		{"bad VM", []*egressv1.WildcardNames{set("build")}, CheckVMSlot},
		{"name twice", []*egressv1.WildcardNames{set("work", "a.example", "a.example")}, CheckMessageForm},
		{"bad name", []*egressv1.WildcardNames{set("work", "a_b..example")}, CheckNameForm},
		{"too many names", []*egressv1.WildcardNames{{Vm: "work", Names: make([]string, MaxWildcardNames+1)}}, CheckMessageForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCheck(t, CheckProxydStart(&egressv1.ProxydStartPayload{WildcardNames: tc.sets}), tc.want)
		})
	}
}

// longName is a canonical name of 253 bytes, unique by i.
func longName(i int) string {
	tail := fmt.Sprintf("%08x", i)
	return strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61-len(tail)) + tail
}

// TestMaxPayloads builds the largest NetdStartPayload,
// ProxydStartPayload and EffectivePolicy the bounds allow and checks
// that each fits MaxPayload, with the sizes control.proto states.
func TestMaxPayloads(t *testing.T) {
	if testing.Short() {
		t.Skip("builds payloads of tens of MiB")
	}
	netd := &egressv1.NetdStartPayload{DebitCount: MaxWildcardNames}
	for i := range MaxEntries {
		netd.Entries = append(netd.Entries, &egressv1.MappingEntry{Name: longName(i), Address: addr1 - 1 + uint32(i)})
	}
	for i := range MaxWildcardNames {
		netd.Debited = append(netd.Debited, longName(i))
	}
	if r := CheckNetdStart(netd); r != nil {
		t.Fatal(r)
	}
	proxyd := &egressv1.ProxydStartPayload{}
	for _, vm := range []string{"work", "isolated"} {
		proxyd.WildcardNames = append(proxyd.WildcardNames, &egressv1.WildcardNames{Vm: vm, Names: netd.Debited})
	}
	if r := CheckProxydStart(proxyd); r != nil {
		t.Fatal(r)
	}
	for _, tc := range []struct {
		name  string
		m     proto.Message
		under int
	}{
		{"NetdStartPayload", netd, 50 << 20},
		{"ProxydStartPayload", proxyd, 32 << 20},
		{"EffectivePolicy", maxPolicy(), 41 << 20},
	} {
		n := proto.Size(tc.m)
		t.Logf("largest %s: %d bytes (%.1f MiB)", tc.name, n, float64(n)/(1<<20))
		if n >= tc.under || n+payloadPrefix > MaxPayload {
			t.Errorf("largest %s is %d bytes, control.proto says under %d MiB, the cap is %d", tc.name, n, tc.under>>20, MaxPayload)
		}
	}
}

// maxPolicy builds an EffectivePolicy with every list at its bound and
// every string at its longest, as policy.FromProto bounds them. It
// isn't one FromProto accepts: only its size matters.
func maxPolicy() *policyv1.EffectivePolicy {
	src := &policyv1.Source{
		Kind: policyv1.SourceKind_SOURCE_KIND_REPOSITORY, ProjectId: strings.Repeat("a", policy.ProjectIDLen),
		Repository: strings.Repeat("r", policy.MaxRepository),
	}
	host := longName(0)
	p := &policyv1.EffectivePolicy{Generation: ^uint64(0), WildcardBudget: policy.MaxWildcardBudget}
	ports := make([]uint32, policy.MaxPortsPerEnd)
	for i := range ports {
		ports[i] = 65535 - uint32(i)
	}
	rule := &policyv1.HttpRule{Method: strings.Repeat("M", match.MaxMethod), Path: strings.Repeat("p", match.MaxPattern)}
	rules := policy.MaxRules
	for range policy.MaxEndpoints {
		e := &policyv1.Endpoint{
			Source: src, RuleName: strings.Repeat("n", policy.MaxRuleName), Host: host, Ports: ports,
			Path: strings.Repeat("p", match.MaxPattern), Protocol: policyv1.Protocol_PROTOCOL_REST,
			Enforcement: policyv1.Enforcement_ENFORCEMENT_AUDIT, Access: policyv1.Access_ACCESS_FULL,
		}
		for ; rules > 0 && len(e.Rules) < policy.MaxRulesPerEndpoint; rules-- {
			e.Rules = append(e.Rules, rule)
		}
		p.Endpoints = append(p.Endpoints, e)
	}
	reason := strings.Repeat("r", policy.MaxReason)
	for range policy.MaxPassHosts {
		p.PassHosts = append(p.PassHosts, &policyv1.PassHost{Host: host, Source: src, Reason: reason})
	}
	for range policy.MaxRawTCP {
		p.RawTcp = append(p.RawTcp, &policyv1.RawTcpEntry{Host: host, Port: 65535, Source: src, Reason: reason})
	}
	part := strings.Repeat("x", policy.MaxPartString)
	for range policy.MaxPartsOff {
		p.PartsOff = append(p.PartsOff, &policyv1.PartOff{Profile: part, Kind: part, Part: part, Source: src})
	}
	repos := make([]string, policy.MaxRepositories)
	for i := range repos {
		repos[i] = strings.Repeat("r", policy.MaxRepository)
	}
	for range policy.MaxProjects {
		p.Projects = append(p.Projects, &policyv1.Project{Id: strings.Repeat("a", policy.ProjectIDLen), Repositories: repos})
	}
	return p
}
