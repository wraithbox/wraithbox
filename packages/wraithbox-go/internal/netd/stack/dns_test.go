package stack

import (
	"log/slog"
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/dns"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// allowOne is a name policy that allows one name exactly.
type allowOne dnsname.Name

func (a allowOne) Class(name dnsname.Name) (netd.NameClass, string) {
	if name == dnsname.Name(a) {
		return netd.NameExact, "test"
	}
	return netd.NameNotAllowed, ""
}

func (allowOne) WildcardBudget() int { return 10 }

// dnsQuery is an A query for host from the guest's port 5353 to dest,
// as a frame.
func dnsQuery(t *testing.T, host string, dest netip.Addr) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 42, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(host), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return nt.FromGuest(uint16(header.IPv4ProtocolNumber), nt.IPv4(nt.Leased, dest, uint8(header.UDPProtocolNumber), nt.UDP(5353, 53, msg)))
}

// TestDNSOverUDP checks the DNS server behind the stack (S07-03-dns,
// C01-network-and-dns, "Lookups"): a query to the gateway's port 53 for
// an allowed name gets a synthetic address back from the gateway, and a
// query to another server is dropped with udp-not-dns and gets nothing.
func TestDNSOverUDP(t *testing.T) {
	calls := &nt.Calls{}
	log := slog.New(slog.DiscardHandler)
	pool, err := synth.New(netd.DefaultSynthetic, nil, synth.Deps{
		Accounting: nt.Accounting{Calls: calls},
		Reports:    nt.NameReports{Calls: calls},
		Log:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := dns.New(dns.Config{
		Addresses:  pool,
		Accounting: nt.Accounting{Calls: calls},
		Names:      allowOne(nt.Name("api.example.com")),
		Log:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	h := start(t, nt.Config(), netd.Hooks{DNSOverUDP: r}, nil)

	h.send(dnsQuery(t, "API.example.com.", nt.Gateway))
	reply := h.receive()
	ip := header.IPv4(reply[header.EthernetMinimumSize:])
	u := header.UDP(ip.Payload())
	if netip.AddrFrom4(ip.SourceAddress().As4()) != nt.Gateway || u.SourcePort() != 53 || u.DestinationPort() != 5353 {
		t.Fatalf("reply %x", reply)
	}
	var m dnsmessage.Message
	if err := m.Unpack(u.Payload()); err != nil {
		t.Fatal(err)
	}
	if m.ID != 42 || m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 1 {
		t.Fatalf("response %+v", m)
	}
	a, ok := m.Answers[0].Body.(*dnsmessage.AResource)
	if !ok || !netd.DefaultSynthetic.Contains(netip.AddrFrom4(a.A)) {
		t.Fatalf("answer %s", m.Answers[0].GoString())
	}
	if r.Recorder().Count(netd.RuleDNSAnswer) != 1 {
		t.Fatalf("%d answers counted", r.Recorder().Count(netd.RuleDNSAnswer))
	}

	h.send(dnsQuery(t, "api.example.com.", public))
	h.waitRule(netd.RuleUDPNotDNS)
	h.quiet()
}
