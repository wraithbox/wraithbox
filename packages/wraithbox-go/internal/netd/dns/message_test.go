package dns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// setU16 returns msg with the 16-bit field at off set to v.
func setU16(msg []byte, off int, v uint16) []byte {
	out := append([]byte(nil), msg...)
	binary.BigEndian.PutUint16(out[off:], v)
	return out
}

// TestParseQueryRefuses checks every way a message fails the form
// (S07-03-dns, "Messages"): each is dropped with dns-message-form and
// gets no response, and the fault is in the log record.
func TestParseQueryRefuses(t *testing.T) {
	good := query(1, "api.example.com", TypeA, true)
	plain := query(1, "api.example.com", TypeA, false)
	pointer := append(append([]byte(nil), plain[:headerLen]...), labelTypes, headerLen)
	pointer = binary.BigEndian.AppendUint16(pointer, TypeA)
	pointer = binary.BigEndian.AppendUint16(pointer, ClassIN)
	optName := append(append([]byte(nil), good[:len(good)-11]...), 1, 'x', 0, 41, 0x04, 0xd0, 0, 0, 0, 0, 0, 0)
	optType := setU16(good, len(good)-10, TypeA)
	optLen := setU16(good, len(good)-2, 4)
	cases := []struct {
		name string
		msg  []byte
		want error
	}{
		{"empty", nil, errShort},
		{"header cut", good[:11], errShort},
		{"over 512 bytes", append(append([]byte(nil), plain...), make([]byte, MaxQuery)...), errLong},
		{"a response", setU16(good, 2, flagQR|flagRD), errResponse},
		{"opcode NOTIFY", setU16(good, 2, 4<<11), errOpcode},
		{"no question", setU16(good, 4, 0), errQuestions},
		{"two questions", setU16(good, 4, 2), errQuestions},
		{"an answer record", setU16(good, 6, 1), errRecords},
		{"an authority record", setU16(good, 8, 1), errRecords},
		{"two additional records", setU16(good, 10, 2), errAdditional},
		{"name cut", plain[:headerLen+5], errTruncated},
		{"no root label", plain[:headerLen+17], errTruncated},
		{"type cut", plain[:len(plain)-3], errTruncated},
		{"compression pointer", pointer, errPointer},
		{"extended label type", append(append([]byte(nil), plain[:headerLen]...), 0x41, 0), errLabelType},
		{"class CH", setU16(plain, len(plain)-2, 3), errClass},
		{"OPT cut", good[:len(good)-3], errTruncated},
		{"OPT for a name", optName, errNotOPT},
		{"additional A record", optType, errNotOPT},
		{"OPT data cut", optLen, errTruncated},
		{"OPT counted but missing", setU16(plain, 10, 1), errTruncated},
		{"trailing byte", append(append([]byte(nil), good...), 0), errTrailing},
		{"OPT not counted", setU16(good, 10, 0), errTrailing},
	}
	f := newFixture(t, nil)
	f.names.set("api.example.com", 1)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseQuery(tc.msg); !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			out := &outbox{}
			o := f.ask(tc.msg, out)
			if o.When != SendNever || o.Rule != netd.RuleDNSMessageForm || len(out.list()) != 0 {
				t.Fatalf("outcome %+v, %d responses", o, len(out.list()))
			}
		})
	}
	if n := f.r.Recorder().Count(netd.RuleDNSMessageForm); n != uint64(len(cases)) {
		t.Errorf("%d drops counted, want %d", n, len(cases))
	}
	if len(f.calls.List()) != 0 {
		t.Errorf("calls %+v", f.calls.List())
	}
}

// TestParseQuery checks the queries that pass: with and without an OPT
// record, with OPT options, and with a label of any byte.
func TestParseQuery(t *testing.T) {
	withOptions := append(setU16(query(1, "a.example", TypeA, true), len(query(1, "a.example", TypeA, true))-2, 4), 0, 10, 0, 0)
	for name, msg := range map[string][]byte{
		"plain":        query(1, "api.example.com", TypeHTTPS, false),
		"OPT":          query(1, "api.example.com", TypeHTTPS, true),
		"OPT options":  withOptions,
		"dot in label": wireQuery(1, [][]byte{[]byte("a.b"), []byte("com")}, TypeA, false),
		"the root":     query(1, "", TypeA, false),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseQuery(msg); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestLogName checks that a name that failed the form is logged through
// guesttext.LogField: control bytes escaped and the length cut.
func TestLogName(t *testing.T) {
	q := mustParse(t, wireQuery(1, [][]byte{[]byte("a\nb"), []byte("x.y"), []byte("com")}, TypeA, false))
	if got := q.LogName(); strings.ContainsRune(got, '\n') || !strings.Contains(got, "x.y.com") {
		t.Errorf("LogName %q", got)
	}
	long := make([][]byte, 8)
	for i := range long {
		long[i] = bytes.Repeat([]byte{'a'}, 60)
	}
	if got := mustParse(t, wireQuery(1, long, TypeA, false)).LogName(); strings.Count(got, "a")+strings.Count(got, ".") > maxLogBytes {
		t.Errorf("LogName %q keeps more than %d bytes", got, maxLogBytes)
	}
}

// TestTypeName checks the names of the types in the log.
func TestTypeName(t *testing.T) {
	for typ, want := range map[uint16]string{TypeA: "A", TypeHTTPS: "HTTPS", 12: "TYPE12"} {
		if got := TypeName(typ); got != want {
			t.Errorf("TypeName(%d) = %q, want %q", typ, got, want)
		}
	}
}
