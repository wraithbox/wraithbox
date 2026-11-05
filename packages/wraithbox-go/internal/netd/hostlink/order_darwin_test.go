package hostlink_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/dns"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// logBuffer is a log the test reads back.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// clock is a clock the test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// exact is a name policy that allows every name by an exact rule.
type exact struct{}

func (exact) Class(dnsname.Name) (netd.NameClass, string) { return netd.NameExact, "test" }
func (exact) WildcardBudget() int                         { return 100 }

// wire is wb-netd's DNS server and synthetic pool over the real
// hand-off Sender and accounting writer, with a fake wb-proxyd and a
// fake wb-hostd at the far ends of fdchan socketpairs. Neither fake has
// sent its version record yet, so every send waits until it does.
type wire struct {
	sender *handoff.Sender
	acct   *hostlink.Accounting
	proxyd *nt.Peer
	hostd  *nt.Peer
	pool   *synth.Pool
	dns    *dns.Resolver
	clock  *clock
	log    *logBuffer
}

func newWire(t *testing.T, prefix netip.Prefix, kept []synth.Entry) *wire {
	t.Helper()
	w := &wire{clock: &clock{now: time.Unix(1_700_000_000, 0)}, log: &logBuffer{}}
	log := slog.New(slog.NewTextHandler(w.log, nil))
	ours, theirs := nt.ChanPair(t)
	w.sender = handoff.New(handoff.Config{Own: nt.Own, Log: log})
	w.sender.Replace(ours, 1)
	t.Cleanup(w.sender.Close)
	w.proxyd = nt.NewPeer(t, theirs)
	w.proxyd.ReadVersion()

	ours, theirs = nt.ChanPair(t)
	acct, err := hostlink.NewAccounting(hostlink.AccountingConfig{Conn: ours, Own: nt.Own, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	w.acct = acct
	w.hostd = nt.NewPeer(t, theirs)
	w.hostd.ReadVersion()

	w.pool, err = synth.New(prefix, kept, synth.Deps{
		Accounting: acct, Reports: w.sender, Now: w.clock.Now, Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	w.dns, err = dns.New(dns.Config{Addresses: w.pool, Accounting: acct, Names: exact{}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.dns.Close)
	return w
}

// query returns an A query for host.
func query(t *testing.T, host string) dns.Query {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	q := dnsmessage.Question{Name: dnsmessage.MustNewName(host + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	if err := b.Question(q); err != nil {
		t.Fatal(err)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := dns.ParseQuery(msg)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// ask decides an A query for host and returns its outcome and how long
// the decision took.
func (w *wire) ask(t *testing.T, host string) (dns.Query, dns.Outcome, time.Duration) {
	t.Helper()
	q := query(t, host)
	start := time.Now()
	o := w.dns.Decide(context.Background(), q)
	return q, o, time.Since(start)
}

// answered returns the address of an answer, or fails the test.
func answered(t *testing.T, o dns.Outcome) netip.Addr {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(o.Response); err != nil {
		t.Fatal(err)
	}
	if o.Rule != netd.RuleDNSAnswer || len(m.Answers) != 1 {
		t.Fatalf("outcome %s with %d answers, want one answer", o.Rule, len(m.Answers))
	}
	a, ok := m.Answers[0].Body.(*dnsmessage.AResource)
	if !ok {
		t.Fatalf("answer %T, want A", m.Answers[0].Body)
	}
	return netip.AddrFrom4(a.A)
}

// sent is one message a fake peer read, in a form a test compares.
type sent struct {
	kind string
	name string
	addr netip.Addr
}

// fromProxyd reads every message queued for wb-proxyd now.
func fromProxyd(t *testing.T, p *nt.Peer) []sent {
	t.Helper()
	var out []sent
	for _, m := range p.Queued() {
		msg, ref := egressmsg.DecodeNetdMessage(m.Record, m.File != nil)
		_ = m.Close()
		if ref != nil {
			t.Fatal(ref)
		}
		out = append(out, sent{kind: msg.Kind.String(), name: msg.Name.String(), addr: msg.Addr})
	}
	return out
}

// fromNetd reads every accounting record queued for wb-hostd now.
func fromNetd(t *testing.T, p *nt.Peer) []sent {
	t.Helper()
	var out []sent
	for _, m := range p.Queued() {
		rec, ref := egressmsg.DecodeAccountingRecord(m.Record, m.File != nil, egressmsg.SenderNetd)
		_ = m.Close()
		if ref != nil {
			t.Fatal(ref)
		}
		out = append(out, sent{kind: rec.Kind.String(), name: rec.Name.String(), addr: rec.Addr})
	}
	return out
}

func equal(t *testing.T, what string, got, want []sent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

// TestAnswerAfterRecords checks that a DNS answer is decided only after
// its accounting record and its name report went out: when the decision
// is made, both are already queued (S07-06-name-mapping, "Name
// reports").
func TestAnswerAfterRecords(t *testing.T) {
	const host = "api.example.com"
	w := newWire(t, netd.DefaultSynthetic, nil)
	w.hostd.SendVersion()
	w.proxyd.SendVersion()
	_, o, _ := w.ask(t, host)
	a := answered(t, o)
	equal(t, "accounting", fromNetd(t, w.hostd), []sent{{"mapping-entry", host, a}})
	equal(t, "hand-off", fromProxyd(t, w.proxyd), []sent{{"name-report", host, a}})
}

// TestAnswerBlocked blocks the accounting record and then the name
// report, each with a peer that withholds its version record. A write
// or send that doesn't go out within 100 ms refuses the name with the
// answer every refusal gets, logged with its rule and with no approval
// event. The report isn't sent when the record failed
// (S07-05-stream-hand-off, "Transport").
func TestAnswerBlocked(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ready     func(w *wire)
		rule      netd.Rule
		acct, rep int // records each peer gets
	}{
		{"accounting blocked", func(w *wire) { w.proxyd.SendVersion() }, netd.RuleAccountingWriteFailed, 0, 0},
		{"report blocked", func(w *wire) { w.hostd.SendVersion() }, netd.RuleHandoffSendFailed, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWire(t, netd.DefaultSynthetic, nil)
			tc.ready(w)
			q, o, took := w.ask(t, "api.example.com")
			w.refused(t, q, o, took, tc.rule)
			if a, r := len(fromNetd(t, w.hostd)), len(fromProxyd(t, w.proxyd)); a != tc.acct || r != tc.rep {
				t.Errorf("wb-hostd got %d records and wb-proxyd %d messages, want %d and %d", a, r, tc.acct, tc.rep)
			}
		})
	}
}

// refused checks that a name was refused with rule, with the answer
// every refusal gets and no approval event, once the 100 ms of a
// blocked write or send passed, and that a log record names the rule.
func (w *wire) refused(t *testing.T, q dns.Query, o dns.Outcome, took time.Duration, rule netd.Rule) {
	t.Helper()
	if o.Rule != rule || !bytes.Equal(o.Response, q.Refusal()) || o.Approve {
		t.Fatalf("outcome %s, approve %v, response %x, want %s with the refusal answer",
			o.Rule, o.Approve, o.Response, rule)
	}
	if took < synth.SendTimeout || took > 10*synth.SendTimeout {
		t.Errorf("refused after %v, want just over %v", took, synth.SendTimeout)
	}
	if !strings.Contains(w.log.String(), "rule="+string(rule)) {
		t.Errorf("no log record with %s:\n%s", rule, w.log)
	}
}

// TestReleaseRetried checks that a release that couldn't be sent keeps
// its address out of the pool, and goes out again before the next name
// report, on the next hand-off socket. Its accounting record is written
// once (S07-06-name-mapping, "Releases").
func TestReleaseRetried(t *testing.T) {
	w := newWire(t, netd.DefaultSynthetic, nil)
	w.hostd.SendVersion()
	w.proxyd.SendVersion()
	_, o, _ := w.ask(t, "old.example.com")
	old := answered(t, o)
	fromNetd(t, w.hostd)
	fromProxyd(t, w.proxyd)

	// A new hand-off socket whose wb-proxyd hasn't sent its version
	// record: the release can't go out.
	ours, theirs := nt.ChanPair(t)
	w.sender.Replace(ours, 2)
	proxyd := nt.NewPeer(t, theirs)
	proxyd.ReadVersion()
	w.clock.add(synth.TTL + synth.Guard + time.Second)
	if rule := w.pool.Sweep(context.Background()); rule != netd.RuleHandoffSendFailed {
		t.Fatalf("sweep: %q, want %s", rule, netd.RuleHandoffSendFailed)
	}
	if live := w.pool.Live(); len(live) != 0 {
		t.Fatalf("live after the sweep: %v", live)
	}
	equal(t, "accounting", fromNetd(t, w.hostd), []sent{{"mapping-release", "", old}})

	proxyd.SendVersion()
	_, o, _ = w.ask(t, "new.example.com")
	addr := answered(t, o)
	if addr == old {
		t.Fatalf("the new name got %v before its release", old)
	}
	equal(t, "hand-off", fromProxyd(t, proxyd), []sent{
		{"release", "", old},
		{"name-report", "new.example.com", addr},
	})
	equal(t, "accounting", fromNetd(t, w.hostd), []sent{{"mapping-entry", "new.example.com", addr}})
}

// TestKeptMapping checks that a pool started with the kept mapping of a
// NetdStart answers each kept name with its kept address, and gives a
// kept address to a new name only after its release went to both peers
// (S04-07-supervision, "The kept name mapping").
func TestKeptMapping(t *testing.T) {
	kept := []synth.Entry{
		{Name: nt.Name("kept.example.com"), Addr: netip.MustParseAddr("198.18.0.0")},
		{Name: nt.Name("other.example.com"), Addr: netip.MustParseAddr("198.18.0.3")},
	}
	k := start(t, kept)
	w := newWire(t, netip.MustParsePrefix("198.18.0.0/30"), k.Entries)
	w.hostd.SendVersion()
	w.proxyd.SendVersion()
	for _, e := range kept {
		_, o, _ := w.ask(t, e.Name.String())
		if a := answered(t, o); a != e.Addr {
			t.Errorf("%s answered %v, want its kept %v", e.Name, a, e.Addr)
		}
	}
	for _, host := range []string{"a.example.com", "b.example.com"} {
		_, o, _ := w.ask(t, host)
		if a := answered(t, o); a == kept[0].Addr || a == kept[1].Addr {
			t.Fatalf("%s got the kept address %v", host, a)
		}
	}
	// Every address not kept is taken, and no kept one is released.
	if _, o, _ := w.ask(t, "full.example.com"); o.Rule != netd.RuleSyntheticPoolFull {
		t.Fatalf("a new name in a full pool: %s", o.Rule)
	}
	fromNetd(t, w.hostd)
	fromProxyd(t, w.proxyd)

	// The kept entries expire, and the new names are renewed.
	w.clock.add(synth.TTL + synth.Guard + time.Second)
	for _, host := range []string{"a.example.com", "b.example.com"} {
		_, o, _ := w.ask(t, host)
		answered(t, o)
	}
	fromNetd(t, w.hostd)
	fromProxyd(t, w.proxyd)
	_, o, _ := w.ask(t, "new.example.com")
	if a := answered(t, o); a != kept[0].Addr {
		t.Fatalf("new.example.com got %v, want the released %v", a, kept[0].Addr)
	}
	equal(t, "hand-off", fromProxyd(t, w.proxyd), []sent{
		{"release", "", kept[0].Addr},
		{"release", "", kept[1].Addr},
		{"name-report", "new.example.com", kept[0].Addr},
	})
	equal(t, "accounting", fromNetd(t, w.hostd), []sent{
		{"mapping-release", "", kept[0].Addr},
		{"mapping-release", "", kept[1].Addr},
		{"mapping-entry", "new.example.com", kept[0].Addr},
	})
}
