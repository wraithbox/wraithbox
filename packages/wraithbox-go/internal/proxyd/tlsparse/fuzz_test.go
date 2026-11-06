package tlsparse_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse/tlstest"
)

// fuzzCap is the byte cap of the fuzz targets: small, so that inputs
// reach it.
const fuzzCap = 1 << 10

// splitRecords puts msg into handshake records, with each byte of cuts
// giving the next record's fragment length, plus one.
func splitRecords(msg, cuts []byte) []byte {
	var sizes []int
	left := len(msg)
	for _, c := range cuts {
		n := int(c) + 1
		if n >= left {
			break
		}
		sizes = append(sizes, n)
		left -= n
	}
	return tlstest.Split(tlstest.TypeHandshake, msg, sizes...)
}

// FuzzClientHello feeds ReadClientHello arbitrary bytes, as they are and
// as a handshake message split over records at arbitrary points
// (C02-streams-and-tls, "Fuzzing"). It checks that the parser doesn't
// panic, decides the same on the bytes cut at the cap, so it reads
// nothing past it, takes no more than the cap, and that a server name
// it accepts is in the name form of S07-03-dns.
func FuzzClientHello(f *testing.F) {
	f.Add(tlstest.Hello{ServerName: "api.example.com", ALPN: []string{"h2"}}.Message(), []byte{2, 30})
	f.Add(tlstest.Hello{ServerName: "API.example.com."}.Message(), []byte{})
	f.Add(tlstest.Hello{ServerName: "192.0.2.1"}.Message(), []byte{0, 0, 0})
	f.Add(tlstest.Hello{Extensions: []tlstest.Extension{{Type: 0, Data: sni(sniEntry(0, "a.b"), sniEntry(0, "c.d"))}}}.Message(), []byte{9})
	f.Add([]byte("SSH-2.0-OpenSSH_9.6\r\n"), []byte{})
	f.Fuzz(func(t *testing.T, msg, cuts []byte) {
		for _, b := range [][]byte{msg, splitRecords(msg, cuts)} {
			checkClientHello(t, b)
		}
	})
}

func checkClientHello(t *testing.T, b []byte) {
	t.Helper()
	ch, n, err := tlsparse.ReadClientHello(b, fuzzCap)
	cut := b[:min(len(b), fuzzCap)]
	_, n2, err2 := tlsparse.ReadClientHello(cut, fuzzCap)
	if n != n2 || tlsparse.CheckOf(err) != tlsparse.CheckOf(err2) || errors.Is(err, tlsparse.ErrShort) != errors.Is(err2, tlsparse.ErrShort) {
		t.Fatalf("bytes past the cap changed the result: %v, %d / %v, %d", err, n, err2, n2)
	}
	switch {
	case errors.Is(err, tlsparse.ErrShort) && len(b) >= fuzzCap:
		t.Fatalf("ErrShort at the cap")
	case errors.Is(err, tlsparse.ErrShort):
	case err != nil && tlsparse.CheckOf(err) == "":
		t.Fatalf("refusal without a check: %v", err)
	case err != nil:
	case n > fuzzCap || n > len(b) || n < tlsparse.HeaderLen:
		t.Fatalf("n = %d of %d bytes", n, len(b))
	default:
		checkNameForm(t, ch)
	}
}

// checkNameForm checks that a server name the ClientHello gives is in
// the name form: lower case, no trailing dot, and the same after
// dnsname.FromHost again.
func checkNameForm(t *testing.T, ch *tlsparse.ClientHello) {
	t.Helper()
	name, ok := ch.Name()
	if !ok {
		return
	}
	s := name.String()
	again, ref := dnsname.FromHost(s)
	if ref != nil || again != name || s != strings.ToLower(s) || strings.HasSuffix(s, ".") {
		t.Fatalf("accepted name %q isn't in the name form", name)
	}
}

// FuzzServerFlight feeds ServerFlight arbitrary records and checks that
// it reads no more than its cap and reports at most one retry before a
// ServerHello.
func FuzzServerFlight(f *testing.F) {
	f.Add(append(tlstest.Record(22, tlstest.ServerHello(true)), tlstest.Record(22, tlstest.ServerHello(false))...))
	f.Add(tlstest.Record(21, []byte{2, 40}))
	f.Fuzz(func(t *testing.T, b []byte) {
		fl := tlsparse.NewServerFlight(fuzzCap)
		read, retries := 0, 0
		ev := tlsparse.ServerNothing
		for ev != tlsparse.ServerHello {
			r, n, err := tlsparse.ParseRecord(b)
			if err != nil {
				return
			}
			b = b[n:]
			read += len(r.Fragment)
			if ev, err = fl.Record(r); err != nil {
				return
			}
			if ev == tlsparse.ServerRetry {
				retries++
			}
			if read > fuzzCap || retries > 1 {
				t.Fatalf("read %d bytes, %d retries", read, retries)
			}
		}
	})
}
