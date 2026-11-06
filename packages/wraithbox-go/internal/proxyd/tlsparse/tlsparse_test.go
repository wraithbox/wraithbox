package tlsparse_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse/tlstest"
)

// sni is the raw data of a server_name extension with the given entries.
func sni(entries ...[]byte) []byte {
	var list []byte
	for _, e := range entries {
		list = append(list, e...)
	}
	return append([]byte{byte(len(list) >> 8), byte(len(list))}, list...)
}

// sniEntry is one server_name entry of the given name type.
func sniEntry(typ byte, host string) []byte {
	return append([]byte{typ, byte(len(host) >> 8), byte(len(host))}, host...)
}

// withTrailing appends extra to a handshake message's body and fixes its
// length.
func withTrailing(msg, extra []byte) []byte {
	out := append(slices.Clone(msg), extra...)
	n := len(out) - 4
	out[1], out[2], out[3] = byte(n>>16), byte(n>>8), byte(n)
	return out
}

func TestReadClientHelloAccepts(t *testing.T) {
	good := tlstest.Hello{ServerName: "api.example.com", ALPN: []string{"h2", "http/1.1"}}
	msg := good.Message()
	tests := []struct {
		name  string
		bytes []byte
		want  string
	}{
		{"one record", good.Records(), "api.example.com"},
		{"split over 3 records", tlstest.Split(tlstest.TypeHandshake, msg, 3, 40), "api.example.com"},
		{"upper case folds", tlstest.Hello{ServerName: "API.Example.COM"}.Records(), "api.example.com"},
		{"GREASE extension", tlstest.Hello{
			ServerName: "a.example.com",
			Extensions: []tlstest.Extension{{Type: 0x1a1a}},
		}.Records(), "a.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := append(slices.Clone(tt.bytes), 0x17, 0x03, 0x03)
			ch, n, err := tlsparse.ReadClientHello(b, tlsparse.MaxPlaintext)
			if err != nil {
				t.Fatalf("ReadClientHello: %v", err)
			}
			if n != len(tt.bytes) {
				t.Errorf("n = %d, want %d", n, len(tt.bytes))
			}
			name, ok := ch.Name()
			if !ok || name.String() != tt.want {
				t.Errorf("Name() = %q, %v; want %q", name, ok, tt.want)
			}
		})
	}
}

func TestReadClientHelloALPN(t *testing.T) {
	ch, _, err := tlsparse.ReadClientHello(tlstest.Hello{ServerName: "a.example.com", ALPN: []string{"ftp", "h2"}}.Records(), 1<<14)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ch.ALPN, []string{"ftp", "h2"}) || !ch.Has(tlsparse.ExtALPN) {
		t.Errorf("ALPN = %q, Has = %v", ch.ALPN, ch.Has(tlsparse.ExtALPN))
	}
}

func TestReadClientHelloShort(t *testing.T) {
	b := tlstest.Split(tlstest.TypeHandshake, tlstest.Hello{ServerName: "a.example.com"}.Message(), 10)
	for _, n := range []int{0, 3, 5, 15, 16, len(b) - 1} {
		if _, _, err := tlsparse.ReadClientHello(b[:n], 1<<14); !errors.Is(err, tlsparse.ErrShort) {
			t.Errorf("%d bytes: err = %v, want ErrShort", n, err)
		}
	}
}

// TestReadClientHelloRefuses feeds the parser each violation and sees it
// fail closed with its check (S07-15-non-http-streams, "First bytes").
func TestReadClientHelloRefuses(t *testing.T) {
	hello := tlstest.Hello{ServerName: "a.example.com"}
	msg := hello.Message()
	big := tlstest.Hello{ServerName: "a.example.com", Extensions: []tlstest.Extension{{Type: 21, Data: make([]byte, 200)}}}
	bigMsg := big.Message()
	tests := []struct {
		name  string
		bytes []byte
		max   int
		check string
	}{
		{"not a handshake record", tlstest.Record(tlstest.TypeApplicationData, msg), 1 << 14, tlsparse.CheckRecordType},
		{"SSH", []byte("SSH-2.0-OpenSSH_9.6\r\n"), 1 << 14, tlsparse.CheckRecordVersion},
		{"record version 3.0", append([]byte{22, 3, 0}, tlstest.Record(22, msg)[3:]...), 1 << 14, tlsparse.CheckRecordVersion},
		{"empty record", tlstest.Record(tlstest.TypeHandshake, nil), 1 << 14, tlsparse.CheckRecordLength},
		{"not a ClientHello", tlstest.Split(tlstest.TypeHandshake, tlstest.ServerHello(false)), 1 << 14, tlsparse.CheckHandshakeType},
		{"handshake bytes after it", tlstest.Record(tlstest.TypeHandshake, append(slices.Clone(msg), 1)), 1 << 14, tlsparse.CheckHandshakeAfterHello},
		{"bytes after the extensions", tlstest.Record(tlstest.TypeHandshake, withTrailing(msg, []byte{0})), 1 << 14, tlsparse.CheckHelloForm},
		{"version 0x0300", tlstest.Hello{ServerName: "a.example.com", Version: 0x0300}.Records(), 1 << 14, tlsparse.CheckHelloForm},
		{"two host names", tlstest.Hello{Extensions: []tlstest.Extension{{
			Type: 0, Data: sni(sniEntry(0, "a.example.com"), sniEntry(0, "b.example.com")),
		}}}.Records(), 1 << 14, tlsparse.CheckServerName},
		{"another name type", tlstest.Hello{Extensions: []tlstest.Extension{{
			Type: 0, Data: sni(sniEntry(0, "a.example.com"), sniEntry(1, "x")),
		}}}.Records(), 1 << 14, tlsparse.CheckServerName},
		{"empty server_name list", tlstest.Hello{Extensions: []tlstest.Extension{{Type: 0, Data: sni()}}}.Records(), 1 << 14, tlsparse.CheckServerName},
		{"duplicate extension", tlstest.Hello{ServerName: "a.example.com", Extensions: []tlstest.Extension{{Type: 0}}}.Records(), 1 << 14, tlsparse.CheckExtensionDuplicate},
		{"empty ALPN name", tlstest.Hello{ServerName: "a.example.com", ALPN: []string{""}}.Records(), 1 << 14, tlsparse.CheckALPN},
		{"one byte past the cap", tlstest.Split(tlstest.TypeHandshake, bigMsg, 100, 100), 15 + len(bigMsg) - 1, tlsparse.CheckCap},
		{"message past the cap", big.Records(), 100, tlsparse.CheckCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, n, err := tlsparse.ReadClientHello(tt.bytes, tt.max)
			if err == nil || errors.Is(err, tlsparse.ErrShort) {
				t.Fatalf("ReadClientHello = %+v, %d, %v; want a refusal", ch, n, err)
			}
			if got := tlsparse.CheckOf(err); got != tt.check {
				t.Errorf("check = %q (%v), want %q", got, err, tt.check)
			}
		})
	}
}

func TestReadClientHelloCapExact(t *testing.T) {
	b := tlstest.Split(tlstest.TypeHandshake, tlstest.Hello{ServerName: "a.example.com"}.Message(), 20, 20)
	if _, n, err := tlsparse.ReadClientHello(b, len(b)); err != nil || n != len(b) {
		t.Fatalf("at the cap: n = %d, err = %v", n, err)
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		server string
		ok     bool
	}{
		{"a.example.com", true},
		{"a.example.com.", false},
		{"192.0.2.1", false},
		{"under_score.example.com", false},
		{"-bad.example.com", false},
	}
	for _, tt := range tests {
		ch, _, err := tlsparse.ReadClientHello(tlstest.Hello{ServerName: tt.server}.Records(), 1<<14)
		if err != nil {
			t.Fatalf("%q: %v", tt.server, err)
		}
		if _, ok := ch.Name(); ok != tt.ok {
			t.Errorf("%q: Name ok = %v, want %v", tt.server, ok, tt.ok)
		}
	}
	ch, _, err := tlsparse.ReadClientHello(tlstest.Hello{}.Records(), 1<<14)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ch.Name(); ok || ch.HasServerName {
		t.Errorf("no server_name: Name ok = %v, HasServerName = %v", ok, ch.HasServerName)
	}
}

func TestExtensions(t *testing.T) {
	for _, e := range []uint16{0, 16, 43, 51, 0xff01} {
		if !tlsparse.KnownExtension(e) {
			t.Errorf("KnownExtension(%d) = false", e)
		}
	}
	for _, e := range []uint16{42, 0xfe0d, 0xffce, 0x1234, 0x0a1a} {
		if tlsparse.KnownExtension(e) {
			t.Errorf("KnownExtension(%#04x) = true", e)
		}
	}
	for _, e := range []uint16{0x0a0a, 0x1a1a, 0xfafa} {
		if !tlsparse.IsGREASE(e) || !tlsparse.KnownExtension(e) {
			t.Errorf("GREASE %#04x not taken", e)
		}
	}
}

func serverRecords(t *testing.T, f *tlsparse.ServerFlight, records ...[]byte) []tlsparse.ServerEvent {
	t.Helper()
	var out []tlsparse.ServerEvent
	for _, b := range records {
		r, _, err := tlsparse.ParseRecord(b)
		if err != nil {
			t.Fatalf("ParseRecord: %v", err)
		}
		ev, err := f.Record(r)
		if err != nil {
			out = append(out, -1)
			continue
		}
		out = append(out, ev)
	}
	return out
}

func TestServerFlight(t *testing.T) {
	hrr := tlstest.Record(tlstest.TypeHandshake, tlstest.ServerHello(true))
	sh := tlstest.ServerHello(false)
	ccs := tlstest.Record(tlstest.TypeChangeCipherSpec, []byte{1})
	tests := []struct {
		name    string
		records [][]byte
		want    []tlsparse.ServerEvent
	}{
		{"ServerHello", [][]byte{tlstest.Record(22, sh)}, []tlsparse.ServerEvent{tlsparse.ServerHello}},
		{"split ServerHello", [][]byte{tlstest.Record(22, sh[:10]), tlstest.Record(22, sh[10:])}, []tlsparse.ServerEvent{tlsparse.ServerNothing, tlsparse.ServerHello}},
		{"retry then hello", [][]byte{hrr, ccs, tlstest.Record(22, sh)}, []tlsparse.ServerEvent{tlsparse.ServerRetry, tlsparse.ServerNothing, tlsparse.ServerHello}},
		{"second retry", [][]byte{hrr, hrr}, []tlsparse.ServerEvent{tlsparse.ServerRetry, -1}},
		{"application data", [][]byte{tlstest.Record(23, []byte{1})}, []tlsparse.ServerEvent{-1}},
		{"not a ServerHello", [][]byte{tlstest.Record(22, tlstest.Hello{}.Message())}, []tlsparse.ServerEvent{-1}},
		{"bytes after the retry", [][]byte{tlstest.Record(22, append(tlstest.ServerHello(true), 2))}, []tlsparse.ServerEvent{-1}},
		{"short ServerHello", [][]byte{tlstest.Record(22, []byte{2, 0, 0, 2, 3, 3})}, []tlsparse.ServerEvent{-1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverRecords(t, tlsparse.NewServerFlight(1<<16), tt.records...)
			if !slices.Equal(got, tt.want) {
				t.Errorf("events = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerFlightCap(t *testing.T) {
	f := tlsparse.NewServerFlight(100)
	got := serverRecords(t, f, tlstest.Record(21, make([]byte, 60)), tlstest.Record(21, make([]byte, 41)))
	if !slices.Equal(got, []tlsparse.ServerEvent{tlsparse.ServerNothing, -1}) {
		t.Errorf("events = %v", got)
	}
}
