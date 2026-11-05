package dns

import (
	"bytes"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// fuzzAllowed is the one name the fuzz target's policy allows.
const fuzzAllowed = "init.push.apple.com"

// FuzzDNSQuery is the DNS server's fuzz target (C01-network-and-dns,
// "Fuzzing"). It feeds messages through the parser, the decision and
// the hold, with seeds of the queries macOS sends (X03-network-path) and
// of names that fail the form. A message that fails the form gets no
// response. Every response echoes the query's question bytes, an A
// record is only for the allowed name and in 198.18.0.0/15, every
// NXDOMAIN carries the zero SOA, and a held refusal holds the same bytes
// whatever its rule.
func FuzzDNSQuery(f *testing.F) {
	for _, s := range fuzzSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, msg []byte) {
		synctest.Test(t, func(t *testing.T) {
			checkQuery(t, msg)
		})
	})
}

func fuzzSeeds() [][]byte {
	label := func(n int) []byte { return bytes.Repeat([]byte{'a'}, n) }
	long := [][]byte{label(63), label(63), label(63), label(61)} // 254 bytes
	pointer := query(1, "a.example", TypeA, false)
	pointer = append(pointer[:headerLen], labelTypes, headerLen, 0, 1, 0, 1)
	seeds := [][]byte{
		wireQuery(1, [][]byte{[]byte("a.b"), []byte("com")}, TypeA, true),
		wireQuery(2, [][]byte{{}, []byte("com")}, TypeA, false),
		wireQuery(3, [][]byte{label(64), []byte("com")}, TypeA, false),
		wireQuery(4, long, TypeA, true),
		pointer,
		query(5, "15.2.0.10.in-addr.arpa", 12, false),
		query(6, "_dns.resolver.arpa", TypeSVCB, true),
		query(7, "TXT.push.apple.com", TypeTXT, true),
		query(8, "time.apple.com", TypeA, false),
	}
	for _, typ := range []uint16{TypeA, TypeAAAA, TypeHTTPS} {
		for _, opt := range []bool{false, true} {
			seeds = append(seeds, query(9, fuzzAllowed, typ, opt))
		}
	}
	return seeds
}

// checkQuery sends msg to a resolver whose policy allows fuzzAllowed and
// lists push.apple.com as background, and checks what it sends.
func checkQuery(t *testing.T, msg []byte) {
	f := newFixture(t, nil)
	f.names.set(fuzzAllowed, netd.NameExact)
	f.r.background[nt.Name("push.apple.com")] = 0
	out := &outbox{}
	o := f.ask(msg, out)
	q, err := ParseQuery(msg)
	if err != nil {
		if o.When != SendNever || o.Rule != netd.RuleDNSMessageForm {
			t.Fatalf("a message that fails the form: outcome %+v", o)
		}
		time.Sleep(2 * Hold)
		synctest.Wait()
		if len(out.list()) != 0 {
			t.Fatal("a response to a message that fails the form")
		}
		return
	}
	time.Sleep(Hold)
	synctest.Wait()
	sent := out.list()
	if o.When == SendNever {
		if len(sent) != 0 {
			t.Fatalf("outcome %+v, and a response", o)
		}
		return
	}
	if len(sent) != 1 {
		t.Fatalf("%d responses", len(sent))
	}
	checkResponse(t, q, o, sent[0].msg)
}

// checkResponse checks one response to q.
func checkResponse(t *testing.T, q Query, o Outcome, resp []byte) {
	if len(resp) < headerLen+len(q.question) || !bytes.Equal(resp[headerLen:headerLen+len(q.question)], q.question) {
		t.Fatalf("response %x doesn't echo the question %x", resp, q.question)
	}
	refusal := o.When == SendHeld || o.Rule == netd.RuleDNSLocalAnswer
	if refusal && !bytes.Equal(resp, q.Refusal()) {
		t.Fatalf("%s: response %x, want the refusal %x", o.Rule, resp, q.Refusal())
	}
	if wire := q.question[:len(q.question)-4]; len(wire) > 255 || bytes.IndexByte(wire, '.') >= 0 {
		// dnsmessage can't read a name over 255 bytes or a label that
		// holds a dot. Such a name fails the form, or is a local
		// answer, and its bytes were compared above.
		if !refusal {
			t.Fatalf("%s: answered a name that fails the form", o.Rule)
		}
		return
	}
	var m dnsmessage.Message
	if err := m.Unpack(resp); err != nil {
		t.Fatalf("response %x: %v", resp, err)
	}
	for _, rr := range m.Answers {
		a, ok := rr.Body.(*dnsmessage.AResource)
		if !ok || !inPool(a.A) || !strings.EqualFold(rr.Header.Name.String(), fuzzAllowed+".") {
			t.Fatalf("answer %s", rr.GoString())
		}
	}
	if m.RCode == dnsmessage.RCodeNameError {
		checkSOA(t, parsed{header: m.Header, answers: m.Answers, authority: m.Authorities}, 0)
	}
}
