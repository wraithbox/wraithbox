// Package dns is wb-netd's DNS server for one VM (S07-03-dns,
// S07-04-dns-refusals): the guest's only resolver, on the gateway's
// port 53. It answers allowed names with synthetic addresses from
// internal/netd/synth, answers the local names itself, and refuses every
// other name with the same bytes, 4 seconds after the query arrived.
//
// Every message comes from the guest, so every byte is adversarial
// (S04-01-design-principles). A message that isn't one well-formed query
// is dropped. The name form rule runs on the question's labels as they
// are on the wire before any check that can debit, raise an event or
// reach a log record unescaped (SEC14-no-fake-approvals). The query
// rate, the wildcard budget and the held-query table are bounded per VM
// (SEC13-bounded-resources), and no name that isn't allowed gets an
// address (SEC05-default-deny).
package dns

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/netip"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// The values of S07-03-dns and S07-04-dns-refusals. The specs name the
// properties, and the values are here.
const (
	// Hold is how long after its query a refusal is sent
	// (S07-04-dns-refusals, "Hold").
	Hold = 4 * time.Second
	// QueryRate and QueryBurst are the VM's query rate: a token bucket of
	// QueryBurst tokens that fills at QueryRate a second (S07-03-dns,
	// "Wildcard budget"). They stop a flood, not normal use.
	QueryRate  = 100
	QueryBurst = 200
	// ttl is the TTL of an A answer and of the SOA record of an allowed
	// name's empty answers, in seconds: the pool's TTL (S07-03-dns,
	// "Negative answers").
	ttl = uint32(synth.TTL / time.Second)
	// DebitTimeout bounds each wildcard budget debit's accounting record.
	// A write that takes longer counts as failed
	// (S04-08-records-and-bounds, "Accounting records").
	DebitTimeout = synth.SendTimeout
)

// Addresses gives an allowed name its synthetic address. *synth.Pool
// implements it: it writes the entry's accounting record and sends the
// name report before it returns, and returns the rule of a failure
// (S07-03-dns, "Synthetic addresses").
type Addresses interface {
	Answer(ctx context.Context, name dnsname.Name) (netip.Addr, netd.Rule)
}

// Config is what a Resolver answers with. Addresses and Accounting are
// required. A nil hook refuses what it would decide: a nil Names allows
// no name and gives no budget.
type Config struct {
	// Addresses answers A queries for allowed names.
	Addresses Addresses
	// Accounting takes the wildcard budget's debits.
	Accounting netd.Accounting
	// Names is the VM's effective policy for names.
	Names netd.NamePolicy
	// Host is the host state that refuses new lookups. Nil refuses none.
	Host netd.HostState
	// Approvals takes the not-allowed names. Nil does nothing.
	Approvals netd.Approvals
	// Background is the guest OS's background list
	// (internal/guestos/background), picked by wb-hostd from the booted
	// image.
	Background []dnsname.Name
	// HeldQueries bounds the held-query table. Zero means the default of
	// netd.DefaultCaps.
	HeldQueries int
	// Debited and DebitCount are the wildcard budget's state that
	// wb-hostd kept for the active period (S04-07-supervision, "What a
	// restart keeps"): the debited names, or only their count. The count
	// spent is the larger of DebitCount and len(Debited).
	Debited    []dnsname.Name
	DebitCount int
	// Log gets every decision, rate-limited per rule. Nil means
	// slog.Default.
	Log *slog.Logger
	// Events gets every refusal and drop with its rule.
	Events netd.Events
}

// Resolver is the DNS server of one VM. It is safe for concurrent use.
type Resolver struct {
	cfg  Config
	rec  *netd.Recorder
	rate *bucket
	hold *holdTable

	// background is the background list, with its count of refusals per
	// name. mu guards the counts.
	mu         sync.Mutex
	background map[dnsname.Name]uint64

	// debitMu orders the debits: it is held from the check of the budget
	// to the end of the debit's record, so a name's record is written
	// before any query depends on the debit (S07-04-dns-refusals, "Order
	// at arrival"). It guards debited and spent.
	debitMu sync.Mutex
	debited map[dnsname.Name]bool
	spent   int
}

// New returns the DNS server of one VM.
func New(cfg Config) (*Resolver, error) {
	if cfg.Addresses == nil || cfg.Accounting == nil {
		return nil, errors.New("dns: a resolver needs addresses and accounting")
	}
	if cfg.HeldQueries == 0 {
		cfg.HeldQueries = netd.DefaultCaps().HeldQueries
	}
	if cfg.HeldQueries < 1 {
		return nil, errors.New("dns: the held-query cap must be at least 1")
	}
	r := &Resolver{
		cfg:        cfg,
		rec:        netd.NewRecorder(cfg.Log, cfg.Events),
		rate:       newBucket(QueryRate, QueryBurst),
		hold:       newHoldTable(cfg.HeldQueries),
		background: map[dnsname.Name]uint64{},
		debited:    map[dnsname.Name]bool{},
	}
	for _, n := range cfg.Background {
		r.background[n] = 0
	}
	for _, n := range cfg.Debited {
		r.debited[n] = true
	}
	r.spent = max(cfg.DebitCount, len(r.debited))
	return r, nil
}

// Recorder returns the recorder the server counts its decisions with.
func (r *Resolver) Recorder() *netd.Recorder { return r.rec }

// When is when an outcome's response leaves.
type When int

// The times a response leaves.
const (
	// SendNow: at once.
	SendNow When = iota
	// SendHeld: Hold after the query arrived, unless the held-query table is
	// full, and then never.
	SendHeld
	// SendNever: the message is dropped without an answer.
	SendNever
)

// Outcome is the decision for one query.
type Outcome struct {
	// Response is the answer's bytes, built at arrival. Nil when When is
	// Never.
	Response []byte
	When     When
	// Rule is why: the refusal's rule, or the note of an answer.
	Rule netd.Rule
	// Name is the name when it passed the name form.
	Name dnsname.Name
	// Debited reports that the name is in the wildcard budget's debited
	// names, so a re-run of a held query doesn't debit it again.
	Debited bool
	// Approve reports that the name raises an approval event, once its
	// query is held.
	Approve bool
	// Attrs are the decision's log attributes.
	Attrs []slog.Attr
}

// Decide decides one query that passed the message form, in the order of
// S07-03-dns, "Order of checks":
//  1. a name under arpa or local gets NXDOMAIN at once;
//  2. the query rate;
//  3. the name form rule, on the labels as they are on the wire;
//  4. the host state;
//  5. the name's class in the effective policy, and the background list;
//  6. the wildcard budget's debit and its accounting record, for every
//     name that isn't allowed by an exact entry, refused names included;
//  7. the class's refusal, or for an allowed name the answer for its
//     type.
//
// Every refusal after step 1 is held and has the bytes of
// Query.Refusal.
func (r *Resolver) Decide(ctx context.Context, q Query) Outcome {
	typ := slog.String("type", TypeName(q.Type))
	// S07-03-dns, "Local answers": matched on the raw last label, before
	// the name form, so _dns.resolver.arpa is answered too. The name is
	// logged escaped, since it passed no check.
	if q.lastLabelIs("arpa") || q.lastLabelIs("local") {
		return Outcome{
			Response: q.Refusal(), When: SendNow, Rule: netd.RuleDNSLocalAnswer,
			Attrs: []slog.Attr{typ, slog.String("name", q.LogName())},
		}
	}
	// SEC13-bounded-resources: every query that isn't a local answer
	// spends a token, a name that fails the form included, so a flood of
	// bad names is refused with dns-query-rate like any other. The name
	// is logged escaped, since it passed no check yet.
	if !r.rate.take(time.Now()) {
		return refuse(q, netd.RuleDNSQueryRate, []slog.Attr{typ, slog.String("name", q.LogName())})
	}
	// SEC14-no-fake-approvals: a name that fails the form reaches no event
	// and no other check.
	name, bad := dnsname.FromWire(q.Labels)
	if bad != nil {
		return refuse(q, netd.RuleDNSNameForm, []slog.Attr{typ, slog.String("name", q.LogName()), slog.String("fault", string(bad.Fault))})
	}
	attrs := []slog.Attr{typ, slog.String("name", name.String())}
	if r.cfg.Host != nil {
		if rule := r.cfg.Host.Refusal(); rule != "" {
			return refuse(q, rule, attrs)
		}
	}
	class, source := netd.NameNotAllowed, ""
	if r.cfg.Names != nil {
		class, source = r.cfg.Names.Class(name)
	}
	attrs = append(attrs, slog.String("class", class.String()), slog.String("source", source))
	return r.decideName(ctx, q, name, class, attrs)
}

// refuse is a held refusal.
func refuse(q Query, rule netd.Rule, attrs []slog.Attr) Outcome {
	return Outcome{Response: q.Refusal(), When: SendHeld, Rule: rule, Attrs: attrs}
}

// decideName takes steps 5 to 7 for a name that passed the form.
func (r *Resolver) decideName(ctx context.Context, q Query, name dnsname.Name, class netd.NameClass, attrs []slog.Attr) Outcome {
	refusal := r.refusalOf(name, class)
	if refusal == netd.RuleGuestOSBackground {
		// S07-04-dns-refusals, "Quiet refusals": a listed name is counted
		// whichever rule refuses it from here, wildcard-budget and
		// accounting-write-failed included.
		r.countBackground(name)
	}
	debited := false
	if class != netd.NameExact {
		// S07-03-dns, "Wildcard budget": a new name under a wildcard and a
		// refused name debit, once each per active period (T02-dns-names).
		if rule := r.debit(ctx, name); rule != "" {
			o := refuse(q, rule, attrs)
			o.Name = name
			return o
		}
		debited = true
	}
	if refusal != "" {
		o := refuse(q, refusal, attrs)
		o.Name, o.Debited = name, debited
		o.Approve = refusal == netd.RuleNameNotAllowed
		return o
	}
	o := r.answer(ctx, q, name)
	o.Name, o.Debited, o.Attrs = name, debited, append(attrs, o.Attrs...)
	return o
}

// refusalOf returns the rule that refuses a name of a class, or "" for
// an allowed name. A name on the background list that the policy
// allows is answered as allowed (S07-04-dns-refusals, "Quiet refusals").
func (r *Resolver) refusalOf(name dnsname.Name, class netd.NameClass) netd.Rule {
	switch class {
	case netd.NameExact, netd.NameWildcard:
		return ""
	case netd.NameDenied:
		return netd.RuleNameDenied
	case netd.NamePartOff:
		return netd.RuleProfilePartOff
	case netd.NameNotAllowed:
		if r.onBackground(name) {
			return netd.RuleGuestOSBackground
		}
		return netd.RuleNameNotAllowed
	}
	// A class this server doesn't know refuses, without an event.
	return netd.RuleNameDenied
}

// answer is step 7: the answer for an allowed name's type (S07-03-dns,
// "Synthetic addresses", "Other record types").
func (r *Resolver) answer(ctx context.Context, q Query, name dnsname.Name) Outcome {
	switch q.Type {
	case TypeA:
		addr, rule := r.cfg.Addresses.Answer(ctx, name)
		switch {
		case rule == netd.RuleSyntheticPoolFull:
			return Outcome{Response: q.servFail(), When: SendNow, Rule: rule}
		case rule != "":
			return refuse(q, rule, nil)
		}
		return Outcome{
			Response: q.answerA(addr, ttl), When: SendNow, Rule: netd.RuleDNSAnswer,
			Attrs: []slog.Attr{slog.String("addr", addr.String())},
		}
	case TypeAAAA, TypeHTTPS, TypeSVCB:
		return Outcome{Response: q.emptyAnswer(ttl), When: SendNow, Rule: netd.RuleDNSEmptyAnswer}
	}
	return Outcome{Response: q.refused(), When: SendNow, Rule: netd.RuleDNSTypeRefused}
}

// debit debits the wildcard budget for name unless it is already among
// the debited names, and writes the debit's record first. It returns
// wildcard-budget when the budget is used up, and
// accounting-write-failed when the record failed or took longer than
// DebitTimeout (S07-04-dns-refusals, "Order at arrival"). The debited
// names are at most the budget.
func (r *Resolver) debit(ctx context.Context, name dnsname.Name) netd.Rule {
	r.debitMu.Lock()
	defer r.debitMu.Unlock()
	if r.debited[name] {
		return ""
	}
	budget := 0
	if r.cfg.Names != nil {
		budget = r.cfg.Names.WildcardBudget()
	}
	if r.spent >= budget {
		return netd.RuleWildcardBudget
	}
	ctx, cancel := context.WithTimeout(ctx, DebitTimeout)
	defer cancel()
	err := r.cfg.Accounting.Debit(ctx, name)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		r.rec.Note(netd.RuleAccountingWriteFailed, slog.String("record", "debit"),
			slog.String("name", name.String()), slog.String("error", err.Error()))
		return netd.RuleAccountingWriteFailed
	}
	r.debited[name] = true
	r.spent++
	return ""
}

// NewActivePeriod clears the wildcard budget's debited names, when a new
// active period starts (S07-01-per-vm-policy, "Active period").
func (r *Resolver) NewActivePeriod() {
	r.debitMu.Lock()
	defer r.debitMu.Unlock()
	clear(r.debited)
	r.spent = 0
}

// Spent returns how many names debited the wildcard budget in the
// active period.
func (r *Resolver) Spent() int {
	r.debitMu.Lock()
	defer r.debitMu.Unlock()
	return r.spent
}

func (r *Resolver) onBackground(name dnsname.Name) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.background[name]
	return ok
}

func (r *Resolver) countBackground(name dnsname.Name) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.background[name]++
}

// BackgroundCounts returns the count of refusals per name on the
// background list, for `wb status`. It holds every listed name.
func (r *Resolver) BackgroundCounts() map[dnsname.Name]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.background)
}
