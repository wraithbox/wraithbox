package dnsname

import (
	"bytes"
	"strings"
	"testing"
)

// label63 and label64 are labels of 63 and 64 bytes.
var (
	label63 = strings.Repeat("a", 63)
	label64 = strings.Repeat("a", 64)
)

// name253 and name254 are names of 253 and 254 bytes written with dots:
// three labels of 63 bytes and one of 61 or 62.
var (
	name253 = label63 + "." + label63 + "." + label63 + "." + strings.Repeat("b", 61)
	name254 = label63 + "." + label63 + "." + label63 + "." + strings.Repeat("b", 62)
)

// wire splits a host on its dots, the way it is on the wire.
func wire(host string) [][]byte {
	if host == "" {
		return nil
	}
	var labels [][]byte
	for l := range strings.SplitSeq(host, ".") {
		labels = append(labels, []byte(l))
	}
	return labels
}

func TestAccepted(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"example.com", "example.com"},
		{"Example.COM", "example.com"},
		{"EXAMPLE.com.", "example.com"},
		{"localhost", "localhost"},
		{"a-b.c-d", "a-b.c-d"},
		{"xn--80ak6aa92e.example", "xn--80ak6aa92e.example"},
		{"ab--cd.example", "ab--cd.example"},
		{"123.example", "123.example"},
		{"example.0xg", "example.0xg"},
		{"example.x0", "example.x0"},
		{"example.1a", "example.1a"},
		{label63 + ".example", label63 + ".example"},
		{name253, name253},
		{name253 + ".", name253},
		{strings.ToUpper(name253), name253},
	} {
		n, r := FromHost(tc.host)
		if r != nil || n.String() != tc.want {
			t.Errorf("FromHost(%q) = %q, %v, want %q", tc.host, n, r, tc.want)
		}
		if strings.HasSuffix(tc.host, ".") {
			continue
		}
		w, r := FromWire(wire(tc.host))
		if r != nil || w != n {
			t.Errorf("FromWire(%q) = %q, %v, want %q", tc.host, w, r, tc.want)
		}
	}
}

func TestMixedCaseIsOneName(t *testing.T) {
	a, _ := FromHost("api.example.com")
	b, _ := FromHost("API.Example.Com.")
	c, _ := FromWire([][]byte{[]byte("Api"), []byte("EXAMPLE"), []byte("com")})
	if a != b || a != c || a == (Name{}) {
		t.Errorf("mixed case gave %q, %q, %q", a, b, c)
	}
}

func TestRefusedHost(t *testing.T) {
	for _, tc := range []struct {
		host  string
		fault Fault
		label int
		msg   string
	}{
		{"", FaultNoLabels, 0, "dns-name-form: the name has no labels: the root name is not a host"},
		{".", FaultNoLabels, 0, "dns-name-form: the name has no labels: the root name is not a host"},
		{"example.com..", FaultLabelEmpty, 3, "dns-name-form: label 3 is empty"},
		{"a..b", FaultLabelEmpty, 2, "dns-name-form: label 2 is empty"},
		{".example", FaultLabelEmpty, 1, "dns-name-form: label 1 is empty"},
		{label64 + ".example", FaultLabelLong, 1, "dns-name-form: label 1 is 64 bytes, longer than 63"},
		{
			"my_host.com", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "_" at offset 2: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"example.c\x00m", FaultLabelByte, 2,
			`dns-name-form: label 2 has the byte "\x00" at offset 1: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"test\u00e9.example", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "\xc3" at offset 4: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"\x1b[31m.example", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "\x1b" at offset 0: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"*.example.com", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "*" at offset 0: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"[::1]", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "[" at offset 0: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"::1", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte ":" at offset 0: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"a b.example", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte " " at offset 1: only ASCII letters, digits and hyphens are allowed`,
		},
		{"-example.com", FaultLabelHyphen, 1, "dns-name-form: label 1 starts with a hyphen"},
		{"example-.com", FaultLabelHyphen, 1, "dns-name-form: label 1 ends with a hyphen"},
		{"www.example.-", FaultLabelHyphen, 3, "dns-name-form: label 3 starts with a hyphen"},
		{name254, FaultNameLong, 0, "dns-name-form: the name is 254 bytes written with dots, longer than 253"},
		{name254 + ".", FaultNameLong, 0, "dns-name-form: the name is 254 bytes written with dots, longer than 253"},
		{
			"1.2.3.4", FaultIPLiteral, 0,
			`dns-name-form: the last label "4" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		{
			"example.123", FaultIPLiteral, 0,
			`dns-name-form: the last label "123" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		{
			"example.0x1f", FaultIPLiteral, 0,
			`dns-name-form: the last label "0x1f" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		{
			"example.0X1F", FaultIPLiteral, 0,
			`dns-name-form: the last label "0X1F" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		{
			"example.0x", FaultIPLiteral, 0,
			`dns-name-form: the last label "0x" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		{
			"2130706433", FaultIPLiteral, 0,
			`dns-name-form: the last label "2130706433" is a decimal or hexadecimal number, or a bare 0x, so the name is an IPv4 address, not a host`,
		},
		// The label checks run before the length check, label by label.
		{name254 + ".-a", FaultLabelHyphen, 5, "dns-name-form: label 5 starts with a hyphen"},
		{
			"a_.-b", FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "_" at offset 1: only ASCII letters, digits and hyphens are allowed`,
		},
	} {
		n, r := FromHost(tc.host)
		checkRefusal(t, "FromHost", tc.host, n, r, want{tc.fault, tc.label, tc.msg})
		if tc.host == "" || strings.HasSuffix(tc.host, ".") {
			continue
		}
		n, r = FromWire(wire(tc.host))
		checkRefusal(t, "FromWire", tc.host, n, r, want{tc.fault, tc.label, tc.msg})
	}
}

func TestRefusedWire(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels [][]byte
		fault  Fault
		label  int
		msg    string
	}{
		{"root", nil, FaultNoLabels, 0, "dns-name-form: the name has no labels: the root name is not a host"},
		{"root label", [][]byte{{}}, FaultLabelEmpty, 1, "dns-name-form: label 1 is empty"},
		{"empty last label", [][]byte{[]byte("example"), {}}, FaultLabelEmpty, 2, "dns-name-form: label 2 is empty"},
		{
			"dot inside a label",
			[][]byte{[]byte("evil.example"), []byte("com")},
			FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "." at offset 4: only ASCII letters, digits and hyphens are allowed`,
		},
		{
			"label of 64 bytes",
			[][]byte{[]byte(label64)},
			FaultLabelLong, 1,
			"dns-name-form: label 1 is 64 bytes, longer than 63",
		},
		{
			"label of 255 bytes",
			[][]byte{[]byte("a"), bytes.Repeat([]byte{0xff}, 255)},
			FaultLabelLong, 2,
			"dns-name-form: label 2 is 255 bytes, longer than 63",
		},
		{
			"high byte",
			[][]byte{{'a', 0x80}},
			FaultLabelByte, 1,
			`dns-name-form: label 1 has the byte "\x80" at offset 1: only ASCII letters, digits and hyphens are allowed`,
		},
	} {
		n, r := FromWire(tc.labels)
		checkRefusal(t, "FromWire", tc.name, n, r, want{tc.fault, tc.label, tc.msg})
	}
}

// want is the refusal a test expects.
type want struct {
	fault Fault
	label int
	msg   string
}

func checkRefusal(t *testing.T, fn, in string, n Name, r *Refusal, w want) {
	t.Helper()
	if r == nil {
		t.Errorf("%s(%q) = %q, want a refusal", fn, in, n)
		return
	}
	if n != (Name{}) || r.Rule != RuleForm || r.Fault != w.fault || r.Label != w.label || r.Error() != w.msg {
		t.Errorf("%s(%q) = %q, %+v %q, want %+v", fn, in, n, *r, r.Error(), w)
	}
}

// TestNilRefusal checks that a nil Refusal stored in an error doesn't
// panic when the error is printed.
func TestNilRefusal(t *testing.T) {
	var r *Refusal
	var err error = r
	if err.Error() != "" {
		t.Errorf("nil Refusal gave %q", err.Error())
	}
}

// TestManyLabels checks that a long list of labels, or a long host,
// costs one pass and allocates only the refusal.
func TestManyLabels(t *testing.T) {
	labels := make([][]byte, 1<<16)
	for i := range labels {
		labels[i] = []byte("a")
	}
	host := strings.Repeat("a.", 1<<16) + "b"
	allocs := testing.AllocsPerRun(10, func() {
		if _, r := FromWire(labels); r == nil || r.Fault != FaultNameLong {
			t.Fatalf("FromWire = %v", r)
		}
		if _, r := FromHost(host); r == nil || r.Fault != FaultNameLong {
			t.Fatalf("FromHost = %v", r)
		}
	})
	// Each refusal allocates itself and its message.
	if allocs > 8 {
		t.Errorf("%v allocations for two refusals", allocs)
	}
}

// FuzzNameForm is the name form's fuzz target (C01-network-and-dns,
// "Fuzzing"). The input is a list of labels, each a length byte and that
// many bytes, so a label can hold a dot or any other byte, and be 0 to
// 255 bytes long. It checks that an accepted name is in canonical form
// and passes FromHost again unchanged, that FromWire and FromHost agree
// on every list both can express, and that a refusal message holds only
// printable ASCII.
func FuzzNameForm(f *testing.F) {
	for _, seed := range [][][]byte{
		wire("example.com"),
		wire("API.Example.COM"),
		{[]byte("evil.example"), []byte("com")},
		{{}},
		{[]byte("a"), {}},
		{[]byte(label64)},
		wire(name253),
		wire(name254),
		wire("1.2.3.4"),
		wire("example.0x1f"),
		wire("example.0x"),
		wire("-a.b-"),
		{{0x00, 0x1b, 0x80, 0xff}},
	} {
		f.Add(encode(seed))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		labels := decode(in)
		n, r := FromWire(labels)
		checkOutcome(t, n, r)
		// A dot byte inside a wire label must never become a separator.
		if r == nil && strings.Count(n.String(), ".") != len(labels)-1 {
			t.Fatalf("FromWire(%q) = %q, not %d labels", labels, n, len(labels))
		}
		if !hostCanExpress(labels) {
			return
		}
		host := string(bytes.Join(labels, []byte(".")))
		h, hr := FromHost(host)
		if h != n || (r == nil) != (hr == nil) || (r != nil && *r != *hr) {
			t.Fatalf("FromWire(%q) = %q, %v but FromHost(%q) = %q, %v", labels, n, r, host, h, hr)
		}
	})
}

// checkOutcome checks one result of FromWire or FromHost.
func checkOutcome(t *testing.T, n Name, r *Refusal) {
	t.Helper()
	if r != nil {
		if n != (Name{}) || r.Rule != RuleForm || !printable(r.Error()) {
			t.Fatalf("refusal %+v %q with name %q", *r, r.Error(), n)
		}
		return
	}
	if fault := notCanonical(n.String()); fault != "" {
		t.Fatalf("accepted %q: %s", n, fault)
	}
	again, r := FromHost(n.String())
	if r != nil || again != n {
		t.Fatalf("FromHost(%q) = %q, %v, want the same name", n, again, r)
	}
}

// printable reports whether s is all printable ASCII.
func printable(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// notCanonical says what keeps s from being a name in canonical form,
// checked without the package's own helpers, and "" when nothing does.
func notCanonical(s string) string {
	if s == "" || len(s) > maxName {
		return "length"
	}
	for l := range strings.SplitSeq(s, ".") {
		if l == "" || len(l) > maxLabel || l[0] == '-' || l[len(l)-1] == '-' {
			return "label " + l
		}
		if strings.Trim(l, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return "byte in label " + l
		}
	}
	last := s[strings.LastIndexByte(s, '.')+1:]
	if strings.Trim(last, "0123456789") == "" {
		return "decimal last label"
	}
	if hex, ok := strings.CutPrefix(last, "0x"); ok && strings.Trim(hex, "0123456789abcdef") == "" {
		return "hexadecimal last label"
	}
	return ""
}

// hostCanExpress reports whether a dotted host can hold the labels: none
// holds a dot, and the last isn't empty, which FromHost reads as a
// trailing dot.
func hostCanExpress(labels [][]byte) bool {
	if len(labels) == 0 || len(labels[len(labels)-1]) == 0 {
		return false
	}
	for _, l := range labels {
		if bytes.IndexByte(l, '.') >= 0 {
			return false
		}
	}
	return true
}

// decode reads labels, each a length byte and that many bytes. A label
// cut short by the end of the input is kept as it is.
func decode(in []byte) [][]byte {
	var labels [][]byte
	for len(in) > 0 {
		n := min(int(in[0]), len(in)-1)
		labels = append(labels, in[1:1+n])
		in = in[1+n:]
	}
	return labels
}

func encode(labels [][]byte) []byte {
	var out []byte
	for _, l := range labels {
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return out
}
