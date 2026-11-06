package stream

import (
	"bytes"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse/tlstest"
)

// fuzzCap is the first-bytes cap of the fuzz targets: small, so that
// inputs reach it.
const fuzzCap = 1 << 10

// FuzzFirstBytes feeds the first-byte checks arbitrary bytes, whole and
// at each prefix, as readFirst does (C02-streams-and-tls, "Fuzzing"). On
// a TLS port it checks that nothing is accepted that the ClientHello
// parser refuses, on port 80 that the HTTP/2 preface is never accepted
// and an accepted request line is HTTP/1.1 or HTTP/1.0, and that a
// decision taken on a prefix holds for the whole.
func FuzzFirstBytes(f *testing.F) {
	f.Add(tlstest.Hello{ServerName: "api.example.com"}.Records())
	f.Add([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	f.Add([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	f.Add([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
	f.Add([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f})
	f.Fuzz(func(t *testing.T, b []byte) {
		checkDecide(t, b, decideTLS)
		checkDecide(t, b, decideHTTP)
		if d := decideTLS(b, fuzzCap); d.verdict == verdictAccept {
			if _, n, err := tlsparse.ReadClientHello(b, fuzzCap); err != nil || n != d.n {
				t.Fatalf("accepted bytes the parser refuses: %v", err)
			}
		}
		d := decideHTTP(b, fuzzCap)
		if d.verdict != verdictAccept {
			return
		}
		line := b[:d.n]
		if bytes.HasPrefix(b, http2Preface) || !bytes.HasSuffix(line, []byte(" HTTP/1.1\r\n")) && !bytes.HasSuffix(line, []byte(" HTTP/1.0\r\n")) {
			t.Fatalf("accepted %q on port 80", line)
		}
	})
}

// checkDecide checks that decide reads no more than the cap, and that a
// prefix's decision holds for the whole.
func checkDecide(t *testing.T, b []byte, decide func([]byte, int) decision) {
	t.Helper()
	whole := decide(b, fuzzCap)
	if whole.n > fuzzCap || whole.n > len(b) {
		t.Fatalf("took %d bytes of %d", whole.n, len(b))
	}
	if whole.verdict == verdictMore && len(b) >= fuzzCap {
		t.Fatal("undecided at the cap")
	}
	for i := range min(len(b), fuzzCap+1) {
		d := decide(b[:i], fuzzCap)
		if d.verdict == verdictMore {
			continue
		}
		if d.verdict != whole.verdict || d.n != whole.n {
			t.Fatalf("prefix of %d decided %v/%d, the whole %v/%d", i, d.verdict, d.n, whole.verdict, whole.n)
		}
		return
	}
}

// FuzzPassClientRecords feeds a pass relay's client direction arbitrary
// records after the first ClientHello, before any ServerHello, with and
// without a HelloRetryRequest (C02-streams-and-tls, "Fuzzing"). It
// checks that what goes upstream is at most one change cipher spec and,
// only after a retry, one ClientHello that passes the pass checks.
func FuzzPassClientRecords(f *testing.F) {
	name := "pass.example.com"
	f.Add(cat(ccs, tlstest.Hello{ServerName: name}.Records()), true)
	f.Add(cat(ccs, ccs), false)
	f.Add(appData, false)
	f.Add(tlstest.Record(tlstest.TypeAlert, []byte{2, 40}), true)
	f.Fuzz(func(t *testing.T, b []byte, retried bool) {
		n, ref := dnsname.FromHost(name)
		if ref != nil {
			t.Fatal(ref)
		}
		p := &passState{name: n, firstBytesCap: fuzzCap, retried: retried}
		var sent []byte
		for {
			s, err := p.nextClient(b)
			if err != nil || s.more {
				break
			}
			if s.through || s.n <= 0 || s.n > len(b) {
				t.Fatalf("step %+v on %d bytes before a ServerHello", s, len(b))
			}
			sent = append(sent, b[:s.n]...)
			b = b[s.n:]
		}
		checkSent(t, p, sent, retried)
	})
}

// checkSent checks the records a pass relay sent upstream before the
// ServerHello.
func checkSent(t *testing.T, p *passState, sent []byte, retried bool) {
	t.Helper()
	changeCipherSpecs, hellos := 0, 0
	for len(sent) > 0 {
		if sent[0] == tlsparse.TypeHandshake {
			ch, n, err := tlsparse.ReadClientHello(sent, fuzzCap)
			if err != nil || !retried || checkPassHello(ch, p.name) != nil {
				t.Fatalf("sent handshake records that aren't a second ClientHello: %v", err)
			}
			hellos++
			sent = sent[n:]
			continue
		}
		r, n, err := tlsparse.ParseRecord(sent)
		if err != nil || r.Type != tlsparse.TypeChangeCipherSpec || !bytes.Equal(r.Fragment, []byte{1}) {
			t.Fatalf("sent a record of type %d: %v", r.Type, err)
		}
		changeCipherSpecs++
		sent = sent[n:]
	}
	if changeCipherSpecs > 1 || hellos > 1 {
		t.Fatalf("sent %d change cipher specs and %d ClientHellos", changeCipherSpecs, hellos)
	}
}
