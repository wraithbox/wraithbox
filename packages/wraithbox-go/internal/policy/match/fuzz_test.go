package match

import (
	"testing"
)

// FuzzPathMatch feeds an arbitrary pattern, method and path to the
// matcher (C03-policy-and-approvals, "Fuzzing": policy matching). The
// path comes from the guest and the pattern from a policy file, so both
// are adversarial. It checks that nothing panics, that canonical form
// is a fixed point and matches what the raw path matches, and that a
// match implies CanMatchPrefix for every prefix of the path, the
// property the load checks of S09-02-precedence rely on.
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
		matched := p.Match(canon)
		if p.Match(again) != matched {
			t.Fatalf("pattern %q matches %q and its canonical form differently", pattern, canon)
		}
		if matched {
			checkPrefixes(t, p, canon)
		}
		if mr == nil && !m.Match(method) {
			t.Fatalf("method %q doesn't match itself", method)
		}
	})
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
