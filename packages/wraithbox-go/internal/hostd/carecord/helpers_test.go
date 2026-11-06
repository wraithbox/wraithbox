package carecord

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

const day = 24 * time.Hour

// zeros pads the test's CA ids to 32 characters.
var zeros = strings.Repeat("0", 30)

// start is day 0 of every test.
var start = time.Date(2027, 1, 4, 9, 0, 0, 0, time.UTC)

// order keeps the calls the fakes saw, in order: the boot order test
// reads it.
type order struct {
	mu    sync.Mutex
	calls []string
}

func (o *order) add(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, s)
}

func (o *order) take() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.calls
	o.calls = nil
	return out
}

// fakeProxyd answers the CA messages as wb-proxyd does, with software
// P-256 keys. Each message goes into the order as its kind: "issue",
// "set <signing or none> <n>", "accept <generation>", "remove <n>".
type fakeProxyd struct {
	o   *order
	now func() time.Time

	mu sync.Mutex
	// issueRule fails each issue with the rule.
	issueRule string
	// restoreFail fails a CaSet with that CA id.
	restoreFail string
	// refuse refuses a removal batch that holds one of its entries.
	refuse map[string]string
	// reply, when set, replaces the answer to each Call.
	reply func(m *egressv1.HostdToProxyd) []byte
	// sets holds each VM's last CaSet. A removal entry of a CA in one
	// is refused, as wb-proxyd's in-record check does.
	sets    map[string]*egressv1.CaSet
	removed []string
}

func (p *fakeProxyd) Call(_ context.Context, m *egressv1.HostdToProxyd) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var r *egressv1.ProxydToHostd
	switch k := m.GetKind().(type) {
	case *egressv1.HostdToProxyd_CaIssue:
		p.o.add("issue")
		r = p.issue(k.CaIssue)
	case *egressv1.HostdToProxyd_CaSet:
		r = p.set(k.CaSet)
	case *egressv1.HostdToProxyd_CaRemove:
		p.o.add(fmt.Sprintf("remove %d", len(k.CaRemove.GetItems())))
		r = p.remove(k.CaRemove.GetItems())
	default:
		return nil, fmt.Errorf("fake proxyd: no answer to %T", k)
	}
	if p.reply != nil {
		return p.reply(m), nil
	}
	return proto.Marshal(r)
}

func (p *fakeProxyd) Send(_ context.Context, m *egressv1.HostdToProxyd) error {
	a := m.GetCaAccept()
	if a == nil {
		return fmt.Errorf("fake proxyd: Send of %T", m.GetKind())
	}
	p.o.add(fmt.Sprintf("accept %d", a.GetGeneration()))
	return nil
}

func (p *fakeProxyd) issue(m *egressv1.CaIssue) *egressv1.ProxydToHostd {
	out := &egressv1.CaIssued{Vm: m.GetVm(), CaId: m.GetCaId(), Rule: p.issueRule}
	if p.issueRule == "" {
		out.Certificate = newCA(p.now(), nil)
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaIssued{CaIssued: out}}
}

func (p *fakeProxyd) set(m *egressv1.CaSet) *egressv1.ProxydToHostd {
	signing := m.GetSigningCaId()
	if signing == "" {
		signing = "none:" + m.GetNoSigningRule()
	} else {
		signing = signing[:2]
	}
	p.o.add(fmt.Sprintf("set %s %d", signing, len(m.GetCas())))
	if p.sets == nil {
		p.sets = map[string]*egressv1.CaSet{}
	}
	p.sets[m.GetVm()] = m
	for _, ca := range m.GetCas() {
		if ca.GetCaId() == p.restoreFail {
			return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRestoreFailed{CaRestoreFailed: &egressv1.CaRestoreFailed{
				Vm: m.GetVm(), Generation: m.GetGeneration(), CaId: ca.GetCaId(), Rule: egressmsg.RuleCAKeyRestore,
			}}}
		}
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaLoaded{CaLoaded: &egressv1.CaLoaded{
		Vm: m.GetVm(), Generation: m.GetGeneration(),
	}}}
}

func (p *fakeProxyd) inSet(it string) bool {
	for _, set := range p.sets {
		for _, ca := range set.GetCas() {
			if strings.HasSuffix(it, "/"+ca.GetCaId()) {
				return true
			}
		}
	}
	return false
}

func (p *fakeProxyd) remove(items []string) *egressv1.ProxydToHostd {
	var refused []*egressv1.CaRemovalRefusal
	var i uint32
	for _, it := range items {
		check, ok := p.refuse[it]
		if !ok && p.inSet(it) {
			check, ok = egressmsg.CheckCAInRecord, true
		}
		if ok {
			refused = append(refused, &egressv1.CaRemovalRefusal{Index: i, Check: check})
		}
		i++
	}
	out := &egressv1.CaRemoved{}
	if len(refused) > 0 {
		out.Rule, out.Refused = egressmsg.RuleCARemovalRefused, refused
	} else {
		out.Deleted = items
		p.removed = append(p.removed, items...)
	}
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: out}}
}

// newCA returns a CA certificate of the profile of S09-07, made at now
// as wb-proxyd makes it, with edit applied to the template first.
func newCA(now time.Time, edit func(*x509.Certificate, *crypto.Signer)) []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	name := pkix.Name{CommonName: "Wraith Box inspection CA test"}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: name, Issuer: name,
		NotBefore: now.Add(-backdate), NotAfter: now.Add(Lifetime),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
	}
	var signer crypto.Signer = key
	if edit != nil {
		edit(tmpl, &signer)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		panic(err)
	}
	return der
}

// fakeGuest records the guest calls of the boot order: "clock <reason>"
// and "install <n>".
type fakeGuest struct {
	o          *order
	installErr error
	// block makes InstallTrust wait for its context.
	block bool
}

func (g *fakeGuest) SetClock(_ context.Context, r Reason) error {
	g.o.add("clock " + r.String())
	return nil
}

func (g *fakeGuest) InstallTrust(ctx context.Context, certs [][]byte) error {
	g.o.add(fmt.Sprintf("install %d", len(certs)))
	if g.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return g.installErr
}

type fakeEnder struct {
	mu    sync.Mutex
	rules []string
}

func (e *fakeEnder) End(rule string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, rule)
}

func (e *fakeEnder) ended() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.rules)
}

// rig is a Keeper with fakes around it, on a clock the test moves.
type rig struct {
	k     *Keeper
	db    *state.DB
	o     *order
	px    *fakeProxyd
	guest *fakeGuest
	ender *fakeEnder
	audit *audittest.Recorder
	now   time.Time
	ids   int
	// seen is how many 5019 records wantChanges read before.
	seen int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db, err := state.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &rig{db: db, o: &order{}, ender: &fakeEnder{}, audit: &audittest.Recorder{}, now: start}
	r.px = &fakeProxyd{o: r.o, now: r.clock}
	r.guest = &fakeGuest{o: r.o}
	r.k = New(Config{
		Records: db, Proxyd: r.px, Ender: r.ender, Audit: r.audit, Clock: r.clock,
		NewID: r.newID, InstallTimeout: 50 * time.Millisecond,
		Log: slog.New(slog.DiscardHandler),
	})
	return r
}

func (r *rig) clock() time.Time { return r.now }

// newID numbers the CAs: the first id starts "a1", the second "a2".
func (r *rig) newID() (string, error) {
	r.ids++
	return fmt.Sprintf("a%d%s", r.ids, zeros), nil
}

// at moves the clock to day d of the test, plus extra.
func (r *rig) at(d int, extra time.Duration) {
	r.now = start.Add(time.Duration(d)*day + extra)
}

// summary is the record of slot: each CA as its id's first two
// characters, with " signing" and " installed <generation>" where set.
func (r *rig) summary(t *testing.T, slot string) string {
	t.Helper()
	cas, err := r.k.List(t.Context(), slot)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, ca := range cas {
		s := ca.ID[:2]
		if ca.Signing {
			s += " signing"
		}
		if ca.InstallGeneration != 0 {
			s += fmt.Sprintf(" installed %d", ca.InstallGeneration)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func (r *rig) wantRecord(t *testing.T, want string) {
	t.Helper()
	if got := r.summary(t, "work"); got != want {
		t.Errorf("record = %q, want %q", got, want)
	}
}

func (r *rig) wantOrder(t *testing.T, want ...string) {
	t.Helper()
	if got := r.o.take(); !slices.Equal(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// removals is the removal list, each entry as its CA id's first two
// characters and its reason.
func (r *rig) removals(t *testing.T) []string {
	t.Helper()
	var out []string
	err := r.db.CAs(t.Context(), func(tx *state.CATx) error {
		list, err := tx.Removals()
		for _, e := range list {
			out = append(out, e.Item[strings.LastIndexByte(e.Item, '/')+1:][:2]+" "+e.Reason)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// changes is the 5019 records written since the last call, each as its
// message, its rule and the first two characters of each CA id.
func (r *rig) changes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, rec := range r.audit.Records() {
		m := audittest.AsMap(rec.Event)
		if m["class_uid"] != float64(5019) {
			continue
		}
		s := rec.Message() + " " + fmt.Sprint(lookup(m, "unmapped", "rule"))
		cas, _ := lookup(m, "unmapped", "cas").([]any)
		for _, ca := range cas {
			s += " " + fmt.Sprint(lookup(ca, "id"))[:2]
		}
		out = append(out, s)
	}
	return out
}

func (r *rig) wantChanges(t *testing.T, want ...string) {
	t.Helper()
	got := r.changes(t)[r.seen:]
	r.seen += len(got)
	if !slices.Equal(got, want) {
		t.Errorf("5019 records = %q, want %q", got, want)
	}
}

// boot runs a boot of generation and checks it passed.
func (r *rig) boot(t *testing.T, generation uint64, reason Reason) {
	t.Helper()
	if err := r.k.Boot(t.Context(), "work", generation, reason, r.guest); err != nil {
		t.Fatalf("%s: %v", reason, err)
	}
}

func (r *rig) tick(t *testing.T, generation uint64) {
	t.Helper()
	if err := r.k.Tick(t.Context(), "work", generation, r.guest); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// lookup walks v down keys through JSON objects, and returns nil where
// one is missing.
func lookup(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}
