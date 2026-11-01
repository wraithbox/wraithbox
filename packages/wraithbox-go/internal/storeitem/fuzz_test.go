package storeitem

import (
	"slices"
	"testing"
)

// FuzzItemName feeds any bytes to Parse. The CA removal list holds
// names that a compromised wb-hostd may have chosen (T16-hostd-landing),
// so Parse is the gate. A name that parses formats back to the same
// bytes, has one of the forms, and has only valid parts.
func FuzzItemName(f *testing.F) {
	for _, s := range []string{
		"wraithbox/binding/github-token",
		"wraithbox/ca/vm-1/ca-2",
		"wraithbox/wbadmin/build/b1",
		"wraithbox/wbadmin/image/base",
		"wraithbox/wbadmin/vm/vm-1/disk-0",
		"wraithbox/binding/..",
		"wraithbox/wbadmin/vm/a/b/c",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := Parse(s)
		if err != nil {
			if n != (Name{}) {
				t.Fatalf("Parse(%q) refused it and returned %q", s, n)
			}
			return
		}
		if n.String() != s {
			t.Fatalf("Parse(%q) formats as %q", s, n)
		}
		if !slices.Contains(Forms(), n.Form()) || !n.Role().Valid() {
			t.Fatalf("Parse(%q) has form %q role %q", s, n.Form(), n.Role())
		}
		again, err := Parse(n.String())
		if err != nil || again != n {
			t.Fatalf("Parse(%q) again = %q, %v", s, again, err)
		}
	})
}
