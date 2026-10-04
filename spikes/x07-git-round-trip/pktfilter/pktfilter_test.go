package pktfilter

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const (
	z = "0000000000000000000000000000000000000000"
	a = "1111111111111111111111111111111111111111"
	p = "refs/heads/wb/s1/"
)

func req(lines ...string) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		b.Write(Pkt(l))
	}
	b.WriteString("0000")
	return b.Bytes()
}

func TestReadRequest(t *testing.T) {
	cases := []struct {
		name    string
		in      []byte
		refused bool // policy refusal
		framing bool // framing error
	}{
		{"ok", req(z + " " + a + " refs/heads/wb/s1/main\x00report-status side-band-64k\n"), false, false},
		{"empty", []byte("0000"), false, false},
		{"main", req(z + " " + a + " refs/heads/main\x00report-status\n"), true, false},
		{"tag", req(z + " " + a + " refs/tags/v1\x00report-status\n"), true, false},
		{"other session", req(z + " " + a + " refs/heads/wb/s2/main\x00report-status\n"), true, false},
		{"prefix only", req(z + " " + a + " refs/heads/wb/s1/\x00report-status\n"), true, false},
		{"session no slash", req(z + " " + a + " refs/heads/wb/s1\x00\n"), true, false},
		{"prefix lookalike", req(z + " " + a + " refs/heads/wb/s10/x\x00\n"), true, false},
		{"dotdot", req(z + " " + a + " refs/heads/wb/s1/../../main\x00\n"), true, false},
		{"lock", req(z + " " + a + " refs/heads/wb/s1/x.lock\x00\n"), true, false},
		{"delete", req(a + " " + z + " refs/heads/wb/s1/x\x00\n"), true, false},
		{"good then bad", req(z+" "+a+" refs/heads/wb/s1/x\x00report-status\n", z+" "+a+" refs/heads/main\n"), true, false},
		{"push options", req(z + " " + a + " refs/heads/wb/s1/x\x00report-status push-options\n"), true, false},
		{"nul in later", req(z+" "+a+" refs/heads/wb/s1/x\x00\n", z+" "+a+" refs/heads/wb/s1/y\x00x\n"), false, true},
		{"bad hex len", []byte("00zz"), false, true},
		{"too long", []byte("ffff"), false, true},
		{"delim", []byte("0001"), false, true},
		{"truncated", []byte("0040abc"), false, true},
		{"no flush", Pkt(z + " " + a + " refs/heads/wb/s1/x\n"), false, true},
		{"upper hex oid", req(z + " " + strings.ToUpper("abcdef1111111111111111111111111111111111") + " refs/heads/wb/s1/x\n"), false, true},
		{"mixed oid len", req(z + " " + a + "11111111111111111111111" + " refs/heads/wb/s1/x\n"), false, true},
		{"push-cert", req("push-cert\x00report-status\n"), false, true},
		{"space in ref", req(z + " " + a + " refs/heads/wb/s1/a b\n"), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ReadRequest(bytes.NewReader(c.in), p)
			var r *Refusal
			isRef := errors.As(err, &r)
			if isRef != c.refused || (err != nil && !isRef) != c.framing {
				t.Fatalf("err=%v refused=%v", err, isRef)
			}
		})
	}
}

func FuzzReadRequest(f *testing.F) {
	f.Add(req(z + " " + a + " refs/heads/wb/s1/main\x00report-status side-band-64k\n"))
	f.Add(req("shallow "+a, z+" "+a+" refs/heads/wb/s1/x\x00\n"))
	f.Add([]byte("0000"))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := ReadRequest(bytes.NewReader(b), p)
		if err == nil {
			// Invariant: everything accepted is under the prefix, not a
			// delete, and Raw is exactly the consumed prefix of the input.
			for _, c := range r.Commands {
				if !strings.HasPrefix(c.Ref, p) || len(c.Ref) == len(p) || ZeroOID(c.New) {
					t.Fatalf("accepted %q", c.Ref)
				}
			}
			if !bytes.HasPrefix(b, r.Raw) {
				t.Fatal("raw is not a prefix of the input")
			}
		}
		_ = RefusalResponse(r, nil, "x")
	})
}
