// Package match holds the method, path and host matcher of the network
// policy (S09-01-policy-files, "Network policy schema"). wb-proxyd
// decides each request with it, and wb-hostd checks policy with it at
// load, so a rule means the same in both (S09-02-precedence, "Network
// rules merge as a union").
//
// The semantics follow OpenShell v0.1.2, commit 6648bd0c
// (X24-openshell-artifacts): `crates/openshell-supervisor-network/data/sandbox-policy.rego`
// (method_matches, path_matches, endpoint_matches_request) and
// `crates/openshell-supervisor-network/src/l7/path.rs` (the canonical
// path). Where they differ from S07-08-requests-and-credentials, the spec
// wins, and the test that covers the case names the difference:
//
//   - A percent-encoded `/` is data inside its segment (S07-08,
//     "Encoded slashes"). OpenShell refuses it unless an endpoint opts in.
//   - A segment that is `.` or `..` only after percent-decoding is
//     refused (S07-08, "Request form"). OpenShell resolves it.
//   - `;params` stay in the path. OpenShell strips them. A segment whose
//     part before its first `;` is `.` or `..` is refused, so `..;`
//     can't reach a server that strips them as a dot segment.
//   - A pattern is put in canonical form before it matches, so
//     `/%67raphql` matches what `/graphql` matches. In OpenShell such a
//     pattern matches no canonical path at all.
//
// Every input is treated as adversarial: request paths come from the
// guest, and patterns from policy files that repository configuration
// can supply. Nothing here logs. Each caller logs a refusal with its
// rule.
package match

import (
	"strconv"
	"strings"
)

// The rules of the refusals in this package.
const (
	// RulePathForm refuses a request path that isn't in a form
	// S07-08-requests-and-credentials, "Request form", accepts.
	RulePathForm = "http-path-form"
	// RulePathPattern refuses a path pattern in policy.
	RulePathPattern = "policy-path-pattern"
	// RuleHostPattern refuses a host pattern in policy that isn't an
	// exact host or one leading `*.` label.
	RuleHostPattern = "policy-host-pattern"
)

// MaxPath is the longest request path CanonicalPath accepts, in bytes.
// It is below the 64 KiB request head cap of S07-08-requests-and-credentials,
// "Header cap", and twice OpenShell's 4 KiB, because git hosting paths
// carry long ref names (SEC13-bounded-resources).
const MaxPath = 8 << 10

// Refusal is the error of every refusal in this package. Its message
// holds only printable ASCII: it never repeats the refused bytes.
type Refusal struct {
	// Rule is one of the rules above, or dnsname.RuleForm for a host.
	Rule string

	msg string
}

// Error returns the rule and the message. A nil Refusal gives "".
func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	return r.Rule + ": " + r.msg
}

func refuse(rule, msg string) *Refusal { return &Refusal{Rule: rule, msg: msg} }

// encodedSlash stands for a decoded `%2F` while a path is split into
// segments, so it never becomes a separator. Raw and decoded control
// bytes are refused before it is used, so no other byte equals it.
const encodedSlash = 0x01

// CanonicalPath returns the canonical form of a request path, the form
// rules match and wb-proxyd sends upstream
// (S07-08-requests-and-credentials, "Request form"). raw is the path
// alone: the caller splits off the query first.
//
// The canonical form:
//   - decodes every percent-escape of a byte that a path may hold
//     literally (RFC 3986 pchar: unreserved, sub-delims, `:` and `@`),
//     and writes every other escape with uppercase hexadecimal digits;
//   - keeps `%2F` as `%2F`, inside its segment ("Encoded slashes");
//   - removes literal `.` and `..` segments (RFC 3986, section 5.2.4);
//   - collapses repeated slashes, except a trailing one, as OpenShell
//     does.
//
// It refuses, with RulePathForm, a path that is empty or doesn't start
// with `/`, is longer than MaxPath before or after canonicalization
// (escaping a byte makes it three, and a fixed point must stay one), or has a raw or encoded control
// byte, a backslash, a raw non-ASCII byte, a `?` or `#`, a malformed
// escape, a `..` above the root, or a segment that is `.` or `..` only
// after decoding, alone, between encoded slashes or before a `;`.
func CanonicalPath(raw string) (string, *Refusal) {
	if r := checkRawPath(raw); r != nil {
		return "", r
	}
	segs, r := resolveSegments(raw)
	if r != nil {
		return "", r
	}
	canon := buildPath(segs)
	if len(canon) > MaxPath {
		return "", refuse(RulePathForm, "the canonical path is "+strconv.Itoa(len(canon))+" bytes, longer than "+strconv.Itoa(MaxPath))
	}
	return canon, nil
}

func checkRawPath(raw string) *Refusal {
	switch {
	case raw == "" || raw[0] != '/':
		return refuse(RulePathForm, "the path does not start with /")
	case len(raw) > MaxPath:
		return refuse(RulePathForm, "the path is "+strconv.Itoa(len(raw))+" bytes, longer than "+strconv.Itoa(MaxPath))
	}
	for i := range len(raw) {
		if r := checkPathByte(raw[i], i, false); r != nil {
			return r
		}
	}
	return nil
}

// checkPathByte refuses a byte no path may hold, raw or after decoding.
func checkPathByte(b byte, at int, decoded bool) *Refusal {
	where := " at offset " + strconv.Itoa(at)
	if decoded {
		where = " encoded" + where
	}
	switch {
	case b < 0x20 || b == 0x7f:
		return refuse(RulePathForm, "the path has a control byte"+where)
	case b == '\\':
		return refuse(RulePathForm, "the path has a backslash"+where)
	case !decoded && b >= 0x80:
		return refuse(RulePathForm, "the path has a non-ASCII byte"+where)
	case !decoded && (b == '?' || b == '#'):
		return refuse(RulePathForm, "the path has a query or fragment separator"+where)
	}
	return nil
}

// decodeSegment percent-decodes one checked segment, which starts at
// offset at of the path. A decoded `/` becomes encodedSlash.
func decodeSegment(seg string, at int) (string, *Refusal) {
	if !strings.Contains(seg, "%") {
		return seg, nil
	}
	out := make([]byte, 0, len(seg))
	for i := 0; i < len(seg); i++ {
		if seg[i] != '%' {
			out = append(out, seg[i])
			continue
		}
		b, ok := unhex(seg, i)
		if !ok {
			return "", refuse(RulePathForm, "the path has a malformed percent-escape at offset "+strconv.Itoa(at+i))
		}
		if b == '/' {
			b = encodedSlash
		} else if r := checkPathByte(b, at+i, true); r != nil {
			return "", r
		}
		out = append(out, b)
		i += 2
	}
	return string(out), nil
}

// unhex decodes the escape that starts at s[i], a `%`.
func unhex(s string, i int) (byte, bool) {
	if i+2 >= len(s) {
		return 0, false
	}
	hi, ok1 := hexValue(s[i+1])
	lo, ok2 := hexValue(s[i+2])
	return hi<<4 | lo, ok1 && ok2
}

func hexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// resolveSegments splits a checked path on its literal slashes, removes
// literal dot segments and empty segments, and decodes the others. A
// trailing slash is kept as an empty last segment. A literal dot
// segment is one that is `.` or `..` before decoding. One that is so
// only after decoding is refused.
func resolveSegments(raw string) ([]string, *Refusal) {
	parts := strings.Split(raw[1:], "/")
	stack := make([]string, 0, len(parts))
	last := len(parts) - 1
	offset := 1
	for i, seg := range parts {
		at := offset
		offset += len(seg) + 1
		switch {
		case seg == "..":
			if len(stack) == 0 {
				return nil, refuse(RulePathForm, "the path has a .. segment above the root")
			}
			stack = stack[:len(stack)-1]
			if i == last {
				stack = append(stack, "")
			}
		case seg == ".":
			if i == last {
				stack = append(stack, "")
			}
		case seg == "" && i != last:
		default:
			d, r := decodeSegment(seg, at)
			if r != nil {
				return nil, r
			}
			if hasHiddenDot(d) {
				return nil, refuse(RulePathForm, "segment "+strconv.Itoa(i+1)+" of the path is a . or .. segment after decoding")
			}
			stack = append(stack, d)
		}
	}
	return stack, nil
}

// hasHiddenDot reports whether a decoded segment that isn't a literal
// dot segment has a `.` or `..` part: between encoded slashes, or before
// a `;`.
func hasHiddenDot(seg string) bool {
	for part := range strings.SplitSeq(seg, string(rune(encodedSlash))) {
		before, _, _ := strings.Cut(part, ";")
		if before == "." || before == ".." {
			return true
		}
	}
	return false
}

func buildPath(segs []string) string {
	var b strings.Builder
	b.WriteByte('/')
	for i, seg := range segs {
		if i > 0 {
			b.WriteByte('/')
		}
		writeSegment(&b, seg)
	}
	return b.String()
}

// writeSegment writes decoded bytes in their canonical encoding.
func writeSegment(b *strings.Builder, seg string) {
	const hexDigits = "0123456789ABCDEF"
	for i := range len(seg) {
		c := seg[i]
		switch {
		case c == encodedSlash:
			b.WriteString("%2F")
		case isPchar(c):
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
}

// isPchar reports whether a byte is written literally in a canonical
// path: RFC 3986 pchar without its percent-encoded form, as OpenShell's
// is_pchar_unreserved.
func isPchar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("-._~!$&'()*+,;=:@", c) >= 0
}
