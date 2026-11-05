package match

import (
	"strconv"
	"strings"
)

// MaxPattern is the longest path pattern Compile accepts, in bytes
// (S09-01-policy-files, "Strings").
const MaxPattern = 1024

// Pattern is a compiled path pattern. The zero Pattern matches nothing.
//
// The glob semantics are OpenShell's path_matches: the pattern `**`
// alone matches every path; otherwise `*` matches any run of bytes
// within one segment, so never a `/`, and `**` any run of bytes across
// segments. Both match the empty run. A canonical path's `%2F` is three
// bytes inside its segment, so `*` matches across it
// (S07-08-requests-and-credentials, "Encoded slashes").
type Pattern struct {
	src  string
	all  bool
	toks []token
	// atoms is the pattern one byte or wildcard at a time, for
	// CanMatchPrefix.
	atoms []atom
}

type tokenKind uint8

const (
	tokLit tokenKind = iota
	tokStar
	tokDoubleStar
)

type token struct {
	kind tokenKind
	lit  string
}

// atom is a literal byte (0 to 255), atomStar or atomDoubleStar.
type atom int16

const (
	atomStar       atom = -1
	atomDoubleStar atom = -2
)

// String returns the pattern as written.
func (p Pattern) String() string { return p.src }

// Compile checks a path pattern and puts its literal parts in the
// canonical form of CanonicalPath, so `/%67raphql` compiles to what
// `/graphql` does. It refuses, with RulePathPattern:
//   - a pattern that is neither `**` nor starts with `/`;
//   - one longer than MaxPattern;
//   - a byte that isn't printable ASCII, and a backslash, `?`, `#`, `[`,
//     `]`, `{` or `}`. OpenShell's glob gives the last five a meaning
//     its prover doesn't model (X24-openshell-artifacts), and a request
//     path holds none of them unescaped;
//   - a malformed percent-escape, or an escape of a control byte or a
//     backslash;
//   - an empty segment other than the last, or a segment without `*`
//     that is `.` or `..`, before or after decoding. No canonical path
//     has one, so the pattern could never match there.
func Compile(src string) (Pattern, *Refusal) {
	if src == "**" {
		return Pattern{src: src, all: true}, nil
	}
	if r := checkPattern(src); r != nil {
		return Pattern{}, r
	}
	p := Pattern{src: src}
	segs := strings.Split(src[1:], "/")
	for i, seg := range segs {
		p.addLit("/")
		if r := p.addSegment(seg, i, i == len(segs)-1); r != nil {
			return Pattern{}, r
		}
	}
	for _, t := range p.toks {
		p.atoms = appendAtoms(p.atoms, t)
	}
	return p, nil
}

func checkPattern(src string) *Refusal {
	switch {
	case src == "" || src[0] != '/':
		return refuse(RulePathPattern, "the path pattern is neither ** nor starts with /")
	case len(src) > MaxPattern:
		return refuse(RulePathPattern, "the path pattern is "+strconv.Itoa(len(src))+" bytes, longer than "+strconv.Itoa(MaxPattern))
	}
	for i := range len(src) {
		c := src[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(`\?#[]{}`, c) >= 0 {
			return refuse(RulePathPattern, "the path pattern has a byte that is not allowed at offset "+strconv.Itoa(i)+
				": only printable ASCII without space, backslash, ?, #, [, ], { and }")
		}
	}
	return nil
}

// addSegment adds one segment of the pattern, the i-th, split on its
// stars.
func (p *Pattern) addSegment(seg string, i int, last bool) *Refusal {
	if seg == "" && !last {
		return refuse(RulePathPattern, "segment "+strconv.Itoa(i+1)+" of the path pattern is empty")
	}
	if !strings.Contains(seg, "*") {
		d, r := decodeSegment(seg, 0)
		if r != nil {
			return refuse(RulePathPattern, "segment "+strconv.Itoa(i+1)+" of the path pattern: "+r.msg)
		}
		if seg == "." || seg == ".." || hasHiddenDot(d) {
			return refuse(RulePathPattern, "segment "+strconv.Itoa(i+1)+" of the path pattern is a . or .. segment, which no canonical path has")
		}
		p.addLit(encodeSegment(d))
		return nil
	}
	for rest := seg; rest != ""; {
		lit, after, star := strings.Cut(rest, "*")
		d, r := decodeSegment(lit, 0)
		if r != nil {
			return refuse(RulePathPattern, "segment "+strconv.Itoa(i+1)+" of the path pattern: "+r.msg)
		}
		p.addLit(encodeSegment(d))
		rest = after
		switch {
		case !star:
		case strings.HasPrefix(rest, "*"):
			p.toks = append(p.toks, token{kind: tokDoubleStar})
			rest = rest[1:]
		default:
			p.toks = append(p.toks, token{kind: tokStar})
		}
	}
	return nil
}

func (p *Pattern) addLit(s string) {
	if s == "" {
		return
	}
	if n := len(p.toks); n > 0 && p.toks[n-1].kind == tokLit {
		p.toks[n-1].lit += s
		return
	}
	p.toks = append(p.toks, token{kind: tokLit, lit: s})
}

func encodeSegment(decoded string) string {
	var b strings.Builder
	writeSegment(&b, decoded)
	return b.String()
}

func appendAtoms(atoms []atom, t token) []atom {
	switch t.kind {
	case tokStar:
		return append(atoms, atomStar)
	case tokDoubleStar:
		return append(atoms, atomDoubleStar)
	}
	for i := range len(t.lit) {
		atoms = append(atoms, atom(t.lit[i]))
	}
	return atoms
}

// Match reports whether the pattern matches a path in canonical form
// (CanonicalPath). It runs in time proportional to the path's length
// times the number of the pattern's parts, never exponential.
func (p Pattern) Match(path string) bool {
	if p.all {
		return true
	}
	if len(p.toks) == 0 {
		return false
	}
	n := len(path)
	cur, next := make([]bool, n+1), make([]bool, n+1)
	cur[0] = true
	for _, t := range p.toks {
		clear(next)
		if !step(t, path, cur, next) {
			return false
		}
		cur, next = next, cur
	}
	return cur[n]
}

// step moves the set of positions a token can start at, cur, to the set
// it can end at, next. It reports whether next has any position.
func step(t token, path string, cur, next []bool) bool {
	live := false
	on := false
	for i := range cur {
		switch t.kind {
		case tokLit:
			on = cur[i] && strings.HasPrefix(path[i:], t.lit)
			if on {
				next[i+len(t.lit)] = true
			}
		case tokStar:
			// A run within one segment: a `/` before i ends it.
			if i > 0 && path[i-1] == '/' {
				on = false
			}
			on = on || cur[i]
			next[i] = next[i] || on
		case tokDoubleStar:
			on = on || cur[i]
			next[i] = next[i] || on
		}
		live = live || on
	}
	return live
}

// CanMatchPrefix reports whether the pattern matches some path that
// starts with prefix. It decides on the pattern, not by sampling
// paths: it runs the pattern over the prefix and answers yes when any
// state of the pattern is still alive at its end, because what remains
// of a pattern can always be completed. The answer may be yes for a
// pattern whose only completions aren't canonical paths. That errs
// towards a match, which fails closed for the load checks of
// S09-02-precedence.
//
// prefix must be in canonical form: the caller passes a path such as
// `/graphql` or `/v1/oauth/token`.
func (p Pattern) CanMatchPrefix(prefix string) bool {
	if p.all {
		return true
	}
	if len(p.atoms) == 0 {
		return false
	}
	states := make([]bool, len(p.atoms)+1)
	states[0] = true
	closeStates(p.atoms, states)
	for i := range len(prefix) {
		var live bool
		states, live = advance(p.atoms, states, prefix[i])
		if !live {
			return false
		}
		closeStates(p.atoms, states)
	}
	return true
}

// closeStates adds, for each live state at a wildcard, the state after
// it, since a wildcard matches the empty run.
func closeStates(atoms []atom, states []bool) {
	for i, a := range atoms {
		if states[i] && a < 0 {
			states[i+1] = true
		}
	}
}

// advance moves the live states over one byte, and reports whether any
// is still live. A wildcard state stays on the byte it consumes, a
// literal state moves past a byte equal to it, and the end state
// consumes nothing.
func advance(atoms []atom, states []bool, c byte) ([]bool, bool) {
	next := make([]bool, len(states))
	live := false
	for i, a := range atoms {
		if !states[i] {
			continue
		}
		switch {
		case a == atomDoubleStar, a == atomStar && c != '/':
			next[i] = true
			live = true
		case a >= 0 && byte(a) == c:
			next[i+1] = true
			live = true
		}
	}
	return next, live
}
