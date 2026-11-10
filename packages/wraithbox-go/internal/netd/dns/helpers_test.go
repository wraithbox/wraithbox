package dns

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// names is a fake netd.NamePolicy: a class per name, NameNotAllowed for
// the rest, and a budget.
type names struct {
	mu      sync.Mutex
	classes map[dnsname.Name]netd.NameClass
	budget  int
}

func (n *names) set(host string, c netd.NameClass) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.classes == nil {
		n.classes = map[dnsname.Name]netd.NameClass{}
	}
	n.classes[nt.Name(host)] = c
}

func (n *names) Class(name dnsname.Name) (netd.NameClass, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.classes[name], "test"
}

func (n *names) WildcardBudget() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.budget
}

// host is a fake netd.HostState.
type host struct{ rule netd.Rule }

func (h *host) Refusal() netd.Rule { return h.rule }

// event is one decision passed to netd.Events.
type event struct {
	decision netd.Decision
	rule     netd.Rule
}

// events is a fake netd.Events that keeps the decisions.
type events struct {
	mu  sync.Mutex
	got []event
}

func (e *events) Event(d netd.Decision, rule netd.Rule, _ uint64, _ []slog.Attr) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, event{d, rule})
}

func (e *events) list() []event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]event(nil), e.got...)
}

// approvals is a fake netd.Approvals that keeps the names.
type approvals struct {
	mu    sync.Mutex
	names []dnsname.Name
}

func (a *approvals) NotAllowed(name dnsname.Name) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.names = append(a.names, name)
}

func (a *approvals) list() []dnsname.Name {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]dnsname.Name(nil), a.names...)
}

// fixture is a resolver with fakes: its own pool, accounting and name
// reports sharing calls, a name policy, a host state and approvals.
type fixture struct {
	r         *Resolver
	calls     *nt.Calls
	names     *names
	host      *host
	approvals *approvals
}

// newFixture returns a resolver with fakes. change may change its
// configuration before New.
func newFixture(t *testing.T, change func(*Config)) *fixture {
	t.Helper()
	f := &fixture{calls: &nt.Calls{}, names: &names{budget: 100}, host: &host{}, approvals: &approvals{}}
	log := slog.New(slog.DiscardHandler)
	pool, err := synth.New(netd.DefaultSynthetic, nil, synth.Deps{
		Accounting: nt.Accounting{Calls: f.calls},
		Reports:    nt.NameReports{Calls: f.calls},
		Log:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Addresses:  pool,
		Accounting: nt.Accounting{Calls: f.calls},
		Names:      f.names,
		Host:       f.host,
		Approvals:  f.approvals,
		Log:        log,
	}
	if change != nil {
		change(&cfg)
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	f.r = r
	return f
}

// sent is a response and when it was sent.
type sent struct {
	msg []byte
	at  time.Time
}

// outbox keeps what a resolver sends.
type outbox struct {
	mu   sync.Mutex
	msgs []sent
}

func (o *outbox) send(b []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, sent{msg: append([]byte(nil), b...), at: time.Now()})
}

func (o *outbox) list() []sent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]sent(nil), o.msgs...)
}

// ask sends one message to the fixture's resolver now.
func (f *fixture) ask(msg []byte, out *outbox) Outcome {
	return f.r.Handle(context.Background(), msg, time.Now(), "guest", out.send)
}

// query is a query for host, built by hand so a test can put any byte in
// a label: its labels are host split at dots, with the root label after
// them, and an OPT record when opt is set.
func query(id uint16, host string, typ uint16, opt bool) []byte {
	var labels [][]byte
	if host != "" {
		for l := range strings.SplitSeq(host, ".") {
			labels = append(labels, []byte(l))
		}
	}
	return wireQuery(id, labels, typ, opt)
}

// wireQuery is a query with these labels as they are on the wire.
func wireQuery(id uint16, labels [][]byte, typ uint16, opt bool) []byte {
	b := binary.BigEndian.AppendUint16(nil, id)
	b = binary.BigEndian.AppendUint16(b, flagRD)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint32(b, 0)
	ar := uint16(0)
	if opt {
		ar = 1
	}
	b = binary.BigEndian.AppendUint16(b, ar)
	for _, l := range labels {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, ClassIN)
	if opt {
		// The root, OPT, a UDP size of 1232, no extended flags, and no
		// options.
		b = append(b, 0, 0, 41, 0x04, 0xd0, 0, 0, 0, 0, 0, 0)
	}
	return b
}

// parsed is a response read with dnsmessage, a parser apart from the
// server's own.
type parsed struct {
	header    dnsmessage.Header
	question  dnsmessage.Question
	answers   []dnsmessage.Resource
	authority []dnsmessage.Resource
}

func parse(t testing.TB, msg []byte) parsed {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(msg); err != nil {
		t.Fatalf("response %x: %v", msg, err)
	}
	if len(m.Questions) != 1 || len(m.Additionals) != 0 {
		t.Fatalf("response %x: %d questions, %d additional records", msg, len(m.Questions), len(m.Additionals))
	}
	return parsed{header: m.Header, question: m.Questions[0], answers: m.Answers, authority: m.Authorities}
}

// checkSOA checks that p has one authority record, the root SOA with a
// TTL and MINIMUM of ttl.
func checkSOA(t testing.TB, p parsed, ttl uint32) {
	t.Helper()
	if len(p.authority) != 1 {
		t.Fatalf("%d authority records, want 1", len(p.authority))
	}
	rr := p.authority[0]
	soa, ok := rr.Body.(*dnsmessage.SOAResource)
	if !ok || rr.Header.Name.String() != "." || rr.Header.TTL != ttl || soa.MinTTL != ttl {
		t.Fatalf("authority %v, want the root SOA at TTL %d", rr.GoString(), ttl)
	}
}

// fullPool is an Addresses with no free address.
type fullPool struct{}

func (fullPool) Answer(context.Context, dnsname.Name) (netip.Addr, netd.Rule) {
	return netip.Addr{}, netd.RuleSyntheticPoolFull
}

// inPool reports whether addr is a synthetic address.
func inPool(addr [4]byte) bool { return netd.DefaultSynthetic.Contains(netip.AddrFrom4(addr)) }
