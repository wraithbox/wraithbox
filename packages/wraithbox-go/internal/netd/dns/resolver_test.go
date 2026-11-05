package dns

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// TestAllowedA checks an A answer for an allowed name (S07-03-dns,
// "Synthetic addresses"): an address in 198.18.0.0/15 at the pool's TTL,
// the question echoed in the guest's case, and the entry's record and
// name report made before the answer was sent.
func TestAllowedA(t *testing.T) {
	f := newFixture(t, nil)
	f.names.set("api.example.com", netd.NameExact)
	var callsAtSend int
	msg := query(7, "API.Example.com", TypeA, true)
	o := f.r.Handle(t.Context(), msg, time.Now(), "guest", func(b []byte) {
		callsAtSend = len(f.calls.List())
		p := parse(t, b)
		if p.header.ID != 7 || !p.header.Response || p.header.RCode != dnsmessage.RCodeSuccess || !p.header.RecursionDesired {
			t.Errorf("header %+v", p.header)
		}
		if p.question.Name.String() != "API.Example.com." {
			t.Errorf("question %s, want the guest's case", p.question.Name)
		}
		if len(p.answers) != 1 || len(p.authority) != 0 {
			t.Fatalf("%d answers, %d authority records", len(p.answers), len(p.authority))
		}
		a, ok := p.answers[0].Body.(*dnsmessage.AResource)
		if !ok || !inPool(a.A) || p.answers[0].Header.TTL != ttl || ttl != 60 {
			t.Errorf("answer %s", p.answers[0].GoString())
		}
	})
	if o.Rule != netd.RuleDNSAnswer || o.When != SendNow || o.Debited {
		t.Fatalf("outcome %+v", o)
	}
	calls := f.calls.List()
	if callsAtSend != 2 || len(calls) != 2 || calls[0].Kind != nt.CallEntry || calls[1].Kind != nt.CallReport {
		t.Fatalf("calls %+v, %d before the answer", calls, callsAtSend)
	}
}

// TestOtherTypes checks the empty answers to AAAA, HTTPS and SVCB for an
// allowed name, with the root SOA at the A answer's TTL, and REFUSED at
// once for TXT (S07-03-dns, "Other record types", "Negative answers").
func TestOtherTypes(t *testing.T) {
	f := newFixture(t, nil)
	f.names.set("api.example.com", netd.NameExact)
	for _, typ := range []uint16{TypeAAAA, TypeHTTPS, TypeSVCB} {
		out := &outbox{}
		o := f.ask(query(1, "api.example.com", typ, false), out)
		if o.When != SendNow || o.Rule != netd.RuleDNSEmptyAnswer || len(out.list()) != 1 {
			t.Fatalf("%s: outcome %+v", TypeName(typ), o)
		}
		p := parse(t, out.list()[0].msg)
		if p.header.RCode != dnsmessage.RCodeSuccess || len(p.answers) != 0 {
			t.Errorf("%s: header %+v, %d answers", TypeName(typ), p.header, len(p.answers))
		}
		checkSOA(t, p, 60)
	}
	out := &outbox{}
	o := f.ask(query(1, "api.example.com", TypeTXT, false), out)
	if o.When != SendNow || o.Rule != netd.RuleDNSTypeRefused || len(out.list()) != 1 {
		t.Fatalf("TXT: outcome %+v", o)
	}
	p := parse(t, out.list()[0].msg)
	if p.header.RCode != dnsmessage.RCodeRefused || len(p.answers)+len(p.authority) != 0 {
		t.Errorf("TXT: %+v", p)
	}
	if len(f.calls.List()) != 0 {
		t.Errorf("calls %+v", f.calls.List())
	}
}

// TestLocalAnswers checks the names wb-netd answers itself: NXDOMAIN at
// once with the zero SOA, with no debit and no approval event
// (S07-03-dns, "Local answers"). _dns.resolver.arpa fails the name form,
// and is still a local answer, because the local names are matched
// first.
func TestLocalAnswers(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Background = []dnsname.Name{nt.Name("printer.local")} })
	f.names.budget = 0
	for _, host := range []string{"15.2.0.10.in-addr.arpa", "_dns.resolver.arpa", "ipv4only.arpa", "home.arpa", "printer.local", "Printer.LOCAL", "arpa"} {
		out := &outbox{}
		o := f.ask(query(3, host, TypeSVCB, false), out)
		if o.When != SendNow || o.Rule != netd.RuleDNSLocalAnswer || len(out.list()) != 1 {
			t.Fatalf("%s: outcome %+v", host, o)
		}
		p := parse(t, out.list()[0].msg)
		if p.header.RCode != dnsmessage.RCodeNameError || len(p.answers) != 0 {
			t.Errorf("%s: %+v", host, p.header)
		}
		checkSOA(t, p, 0)
	}
	if len(f.calls.List()) != 0 || len(f.approvals.list()) != 0 {
		t.Errorf("calls %+v, approvals %v", f.calls.List(), f.approvals.list())
	}
	if n := f.r.Recorder().Count(netd.RuleDNSLocalAnswer); n != 7 {
		t.Errorf("%d local answers logged, want 7", n)
	}
}

// refusalCase sets a fixture up so that query(9, refusedHost, TypeA,
// false) is refused with rule.
type refusalCase struct {
	rule  netd.Rule
	setup func(f *fixture)
}

const refusedHost = "x.example.com"

var refusalCases = []refusalCase{
	{netd.RuleNameNotAllowed, func(*fixture) {}},
	{netd.RuleNameDenied, func(f *fixture) { f.names.set(refusedHost, netd.NameDenied) }},
	{netd.RuleProfilePartOff, func(f *fixture) { f.names.set(refusedHost, netd.NamePartOff) }},
	{netd.RuleGuestOSBackground, func(f *fixture) {
		f.r.background[nt.Name(refusedHost)] = 0
	}},
	{netd.RuleWildcardBudget, func(f *fixture) {
		f.names.set(refusedHost, netd.NameWildcard)
		f.names.budget = 0
	}},
	{netd.RuleDNSQueryRate, func(f *fixture) {
		for f.r.rate.take(time.Now()) {
		}
	}},
	{netd.RuleAccountingWriteFailed, func(f *fixture) {
		f.calls.Fail(nt.CallDebit, nt.Fault{Err: errors.New("down")})
	}},
	{netd.RuleHandoffSendFailed, func(f *fixture) {
		f.names.set(refusedHost, netd.NameExact)
		f.calls.Fail(nt.CallReport, nt.Fault{Err: errors.New("down")})
	}},
	{netd.RuleAuditBudget, func(f *fixture) { f.host.rule = netd.RuleAuditBudget }},
	{netd.RuleAuditBytes, func(f *fixture) { f.host.rule = netd.RuleAuditBytes }},
	{netd.RuleEventChannelBlocked, func(f *fixture) { f.host.rule = netd.RuleEventChannelBlocked }},
}

// TestRefusalsAreAlike checks that every reason a name is refused gives
// the same bytes for the same query, NXDOMAIN with the zero SOA, sent 4
// seconds after the query arrived and not before, also when the
// accounting channel takes 99 ms for each write (S07-04-dns-refusals,
// "Hold", "Same answer for every refusal", C01-network-and-dns, "Hold").
// The name form refusal is for another name, so its bytes are compared
// with the refusal of its own query.
func TestRefusalsAreAlike(t *testing.T) {
	msg := query(9, refusedHost, TypeA, true)
	want := mustParse(t, msg).Refusal()
	for _, delay := range []time.Duration{0, 99 * time.Millisecond} {
		for _, tc := range refusalCases {
			t.Run(fmt.Sprintf("%s/%v", tc.rule, delay), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newFixture(t, nil)
					tc.setup(f)
					f.calls.Fail(nt.CallEntry, nt.Fault{Delay: delay})
					if tc.rule != netd.RuleAccountingWriteFailed {
						f.calls.Fail(nt.CallDebit, nt.Fault{Delay: delay})
					}
					checkHeld(t, f, msg, tc.rule, want)
				})
			})
		}
		t.Run(fmt.Sprintf("name-form/%v", delay), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, nil)
				f.calls.Fail(nt.CallDebit, nt.Fault{Delay: delay})
				bad := query(9, "x_y.example.com", TypeA, true)
				checkHeld(t, f, bad, netd.RuleDNSNameForm, mustParse(t, bad).Refusal())
			})
		})
	}
}

// checkHeld sends msg in a synctest bubble and checks that it is refused
// with rule and answered with want exactly Hold after it arrived.
func checkHeld(t *testing.T, f *fixture, msg []byte, rule netd.Rule, want []byte) {
	t.Helper()
	out := &outbox{}
	arrival := time.Now()
	o := f.r.Handle(t.Context(), msg, arrival, "guest", out.send)
	if o.Rule != rule || o.When != SendHeld {
		t.Fatalf("outcome %s %v, want %s held", o.Rule, o.When, rule)
	}
	time.Sleep(time.Until(arrival.Add(Hold)) - time.Nanosecond)
	synctest.Wait()
	if n := len(out.list()); n != 0 {
		t.Fatalf("%d responses before the hold ended", n)
	}
	time.Sleep(time.Nanosecond)
	synctest.Wait()
	got := out.list()
	if len(got) != 1 || !got[0].at.Equal(arrival.Add(Hold)) {
		t.Fatalf("responses %+v, want one at %v", got, arrival.Add(Hold))
	}
	if !bytes.Equal(got[0].msg, want) {
		t.Fatalf("response %x, want %x", got[0].msg, want)
	}
	p := parse(t, got[0].msg)
	if p.header.RCode != dnsmessage.RCodeNameError || len(p.answers) != 0 {
		t.Fatalf("header %+v", p.header)
	}
	checkSOA(t, p, 0)
	if n := f.r.Recorder().Count(rule); n == 0 {
		t.Errorf("%s not counted", rule)
	}
	if f.r.HeldCount() != 0 {
		t.Errorf("%d queries still held", f.r.HeldCount())
	}
}

func mustParse(t *testing.T, msg []byte) Query {
	t.Helper()
	q, err := ParseQuery(msg)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestBackground checks the quiet refusals (S07-04-dns-refusals, "Quiet
// refusals", C01-network-and-dns, "Lookups"): a listed name in mixed
// case is refused with guest-os-background, counted, and doesn't reach
// the approvals. A wire name always ends at the root label, so the
// trailing dot of a host is the same query. A name one label longer
// reaches the approvals, and a listed name the policy allows is
// answered.
func TestBackground(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listed := nt.Name("init.push.apple.com")
		f := newFixture(t, func(c *Config) { c.Background = []dnsname.Name{listed, nt.Name("time.apple.com")} })
		out := &outbox{}
		if o := f.ask(query(1, "Init.PUSH.apple.COM", TypeA, false), out); o.Rule != netd.RuleGuestOSBackground || o.Approve {
			t.Fatalf("outcome %+v", o)
		}
		if o := f.ask(query(2, "x.init.push.apple.com", TypeA, false), out); o.Rule != netd.RuleNameNotAllowed || !o.Approve {
			t.Fatalf("outcome %+v", o)
		}
		f.names.set("time.apple.com", netd.NameExact)
		if o := f.ask(query(3, "time.apple.com", TypeA, false), out); o.Rule != netd.RuleDNSAnswer {
			t.Fatalf("an allowed listed name: outcome %+v", o)
		}
		if got := f.approvals.list(); len(got) != 1 || got[0] != nt.Name("x.init.push.apple.com") {
			t.Errorf("approvals %v", got)
		}
		counts := f.r.BackgroundCounts()
		if len(counts) != 2 || counts[listed] != 1 || counts[nt.Name("time.apple.com")] != 0 {
			t.Errorf("counts %v", counts)
		}
	})
}

// debits returns the names of the debit calls.
func debits(f *fixture) []string {
	var out []string
	for _, c := range f.calls.List() {
		if c.Kind == nt.CallDebit {
			out = append(out, c.Name.String())
		}
	}
	return out
}

// TestBudget checks the wildcard budget (S07-03-dns, "Wildcard budget",
// "Order of checks"): a new name under a wildcard and a refused name
// debit once each, an exact name never, and neither does a refusal for
// the name form, the query rate or the host state. Past the budget a
// new name is refused with wildcard-budget and raises no event. A new
// active period clears the debits.
func TestBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, nil)
		f.names.budget = 2
		f.names.set("a.wild.example", netd.NameWildcard)
		f.names.set("exact.example.com", netd.NameExact)
		out := &outbox{}
		for range 2 {
			f.ask(query(1, "a.wild.example", TypeA, false), out)
			f.ask(query(1, "A.Wild.Example", TypeAAAA, false), out)
			f.ask(query(1, "refused.example.com", TypeA, false), out)
			f.ask(query(1, "exact.example.com", TypeA, false), out)
			f.ask(wireQuery(1, [][]byte{[]byte("bad_name"), []byte("com")}, TypeA, false), out)
		}
		f.host.rule = netd.RuleAuditBudget
		f.ask(query(1, "host.example.com", TypeA, false), out)
		f.host.rule = ""
		for f.r.rate.take(time.Now()) {
		}
		f.ask(query(1, "rate.example.com", TypeA, false), out)
		f.r.rate = newBucket(QueryRate, QueryBurst)
		if got := debits(f); !slices.Equal(got, []string{"a.wild.example", "refused.example.com"}) {
			t.Fatalf("debits %v", got)
		}
		if f.r.Spent() != 2 {
			t.Fatalf("spent %d", f.r.Spent())
		}

		f.names.set("b.wild.example", netd.NameWildcard)
		for _, host := range []string{"b.wild.example", "new.example.com"} {
			if o := f.ask(query(1, host, TypeA, false), out); o.Rule != netd.RuleWildcardBudget || o.Approve || o.Debited {
				t.Errorf("%s: outcome %+v", host, o)
			}
		}
		if o := f.ask(query(1, "a.wild.example", TypeA, false), out); o.Rule != netd.RuleDNSAnswer || !o.Debited {
			t.Errorf("a debited name past the budget: outcome %+v", o)
		}
		if got := f.approvals.list(); len(got) != 2 {
			t.Errorf("approvals %v, want refused.example.com twice", got)
		}

		f.r.NewActivePeriod()
		f.calls.Reset()
		if o := f.ask(query(1, "b.wild.example", TypeA, false), out); o.Rule != netd.RuleDNSAnswer {
			t.Errorf("after a new active period: outcome %+v", o)
		}
		if got := debits(f); !slices.Equal(got, []string{"b.wild.example"}) {
			t.Errorf("debits %v", got)
		}
	})
}

// TestKeptBudget checks that a resolver starts with the budget state
// wb-hostd kept (S04-07-supervision, "What a restart keeps").
func TestKeptBudget(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.Debited = []dnsname.Name{nt.Name("a.example.com")}
		c.DebitCount = 2
	})
	f.names.budget = 3
	if f.r.Spent() != 2 {
		t.Fatalf("spent %d, want 2", f.r.Spent())
	}
	out := &outbox{}
	f.ask(query(1, "a.example.com", TypeA, false), out)
	f.ask(query(1, "b.example.com", TypeA, false), out)
	if o := f.ask(query(1, "c.example.com", TypeA, false), out); o.Rule != netd.RuleWildcardBudget {
		t.Errorf("outcome %+v", o)
	}
	if got := debits(f); !slices.Equal(got, []string{"b.example.com"}) {
		t.Errorf("debits %v", got)
	}
}

// TestHoldFull checks the cap on held queries (S07-04-dns-refusals,
// "Over the cap"): with 1,024 refusals held, the next is dropped without
// an answer and counted with dns-hold-full, after its debit, and raises
// no approval event.
func TestHoldFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, nil)
		f.names.budget = 2000
		// The query rate would refuse most of them, and a rate refusal
		// doesn't debit. The rate isn't what this test is about.
		f.r.rate = newBucket(1e6, 1e6)
		out := &outbox{}
		limit := netd.DefaultCaps().HeldQueries
		for i := range limit {
			if o := f.ask(query(1, fmt.Sprintf("n%d.example.com", i), TypeA, false), out); o.When != SendHeld {
				t.Fatalf("query %d: outcome %+v", i, o)
			}
		}
		last := fmt.Sprintf("n%d.example.com", limit)
		o := f.ask(query(1, last, TypeA, false), out)
		if o.When != SendNever || o.Rule != netd.RuleDNSHoldFull || !o.Debited {
			t.Fatalf("outcome %+v", o)
		}
		d := debits(f)
		if len(d) != limit+1 || d[limit] != last {
			t.Fatalf("%d debits, the last %v", len(d), d[len(d)-1])
		}
		if n := len(f.approvals.list()); n != limit {
			t.Fatalf("%d approvals, want %d", n, limit)
		}
		if f.r.Recorder().Count(netd.RuleDNSHoldFull) != 1 || f.r.HeldCount() != limit {
			t.Fatalf("hold-full %d, held %d", f.r.Recorder().Count(netd.RuleDNSHoldFull), f.r.HeldCount())
		}
		time.Sleep(Hold)
		synctest.Wait()
		if n := len(out.list()); n != limit {
			t.Fatalf("%d responses, want %d", n, limit)
		}
	})
}

// TestClose checks that a closed resolver sends no held refusal.
func TestClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, nil)
		out := &outbox{}
		f.ask(query(1, "x.example.com", TypeA, false), out)
		f.r.Close()
		time.Sleep(2 * Hold)
		synctest.Wait()
		if len(out.list()) != 0 || f.r.HeldCount() != 0 {
			t.Fatalf("sent %d after Close", len(out.list()))
		}
	})
}

// TestPoolFull checks SERVFAIL at once for a new name while the pool has
// no free address (S07-03-dns, "Synthetic pool").
func TestPoolFull(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Addresses = fullPool{} })
	f.names.set("api.example.com", netd.NameExact)
	out := &outbox{}
	o := f.ask(query(1, "api.example.com", TypeA, false), out)
	if o.When != SendNow || o.Rule != netd.RuleSyntheticPoolFull || len(out.list()) != 1 {
		t.Fatalf("outcome %+v", o)
	}
	if p := parse(t, out.list()[0].msg); p.header.RCode != dnsmessage.RCodeServerFailure {
		t.Fatalf("header %+v", p.header)
	}
}
