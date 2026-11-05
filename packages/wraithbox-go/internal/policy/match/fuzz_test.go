package match

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzPathMatch feeds an arbitrary pattern, method and path to the
// matcher (C03-policy-and-approvals, "Fuzzing": policy matching). The
// path comes from the guest and the pattern from a policy file, so both
// are adversarial. It checks that nothing panics, that canonical form
// is a fixed point, that other spellings of the same path have the same
// canonical form, so every pattern matches them alike, and that a match implies CanMatchPrefix for
// every prefix of the path, the property the load checks of
// S09-02-precedence rely on.
func FuzzPathMatch(f *testing.F) {
	for _, seed := range []struct{ pattern, method, path string }{
		{"/**", "*", "/graphql"},
		{"**", "GET", "/"},
		{"/graph*", "POST", "/%67raphql"},
		{"/api/*/x", "get", "/api/a%2Fb/x"},
		{"/v1/oauth/%74oken", "POST", "/v1/oauth/token/revoke"},
		{"/a**b", "HEAD", "//a/./x/../b"},
		{"/@scope%2fname", "GET", "/@scope%2Fname"},
		{"/a/*", "GET", "/a/..;/b"},
		{"/%2A", "PUT", "/*"},
		{"/a", "GET", "/a%zz"},
	} {
		f.Add(seed.pattern, seed.method, seed.path)
	}
	f.Fuzz(func(t *testing.T, pattern, method, path string) {
		p, pr := Compile(pattern)
		m, mr := CompileMethod(method)
		canon, cr := CanonicalPath(path)
		if pr != nil || cr != nil {
			return
		}
		again, ar := CanonicalPath(canon)
		if ar != nil || again != canon {
			t.Fatalf("CanonicalPath(%q) = %q, then %q, %v", path, canon, again, ar)
		}
		checkSpellings(t, canon)
		if p.Match(canon) {
			checkPrefixes(t, p, canon)
		}
		if mr == nil && !m.Match(method) {
			t.Fatalf("method %q doesn't match itself", method)
		}
	})
}

// spellings returns other spellings of a canonical path that
// CanonicalPath must bring back to it: a byte that may appear literally
// escaped with lowercase hex digits, a doubled slash, an inserted `/.`,
// and an inserted `/x/..`.
func spellings(canon string) []string {
	out := []string{
		"/" + canon,
		"/." + canon,
		"/x/.." + canon,
	}
	// A byte whose escape has a hex letter: `j` to `o`, `z`, their
	// uppercase forms, `:`, `=` and `~`.
	hexLetter := func(r rune) bool {
		l := r | 0x20
		return 'j' <= l && l <= 'o' || l == 'z' || strings.ContainsRune(":=~", r)
	}
	if i := strings.IndexFunc(canon, hexLetter); i >= 0 {
		out = append(out, canon[:i]+fmt.Sprintf("%%%02x", canon[i])+canon[i+1:])
	}
	return out
}

// checkSpellings checks that each spelling of a canonical path has that
// canonical form. Patterns match the canonical form only, so the
// spellings then match every pattern as canon does.
func checkSpellings(t *testing.T, canon string) {
	t.Helper()
	for _, v := range spellings(canon) {
		if len(v) > MaxPath {
			continue
		}
		got, r := CanonicalPath(v)
		if r != nil || got != canon {
			t.Fatalf("CanonicalPath(%q) = %q, %v, want %q", v, got, r, canon)
		}
	}
}

// checkPrefixes checks that a pattern that matches a path can match
// every prefix of it.
func checkPrefixes(t *testing.T, p Pattern, path string) {
	t.Helper()
	for i := range len(path) + 1 {
		if !p.CanMatchPrefix(path[:i]) {
			t.Fatalf("pattern %q matches %q but not CanMatchPrefix(%q)", p, path, path[:i])
		}
	}
}
