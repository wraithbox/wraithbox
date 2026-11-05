package policyfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// nested returns a file whose root mapping holds depth-1 flow lists
// inside each other, so the deepest list is at depth depth.
func nested(depth int) string {
	return "version: 1\nx: " + strings.Repeat("[", depth-1) + strings.Repeat("]", depth-1) + "\n"
}

// TestBoundDepth checks MaxDepth (I489, decision 2) in the form check,
// before the keys are checked: the deepest file that passes it is
// refused for its unknown key, and one level more for its depth.
func TestBoundDepth(t *testing.T) {
	_, rs := loadString(t, global, nested(MaxDepth))
	if rs.Has(RuleYAMLBound) {
		t.Fatalf("depth %d refused for its depth:\n%v", MaxDepth, rs)
	}
	wantRefused(t, rs, RuleKeyUnknown, "x")
	_, rs = loadString(t, global, nested(MaxDepth+1))
	wantRefused(t, rs, RuleYAMLBound, "")
	if rs.Has(RuleKeyUnknown) {
		t.Errorf("a file past the depth bound had its keys checked:\n%v", rs)
	}
}

// TestBoundNodes checks MaxNodes: a flat list of scalars, within the
// size bound.
func TestBoundNodes(t *testing.T) {
	_, rs := loadString(t, global, "version: 1\nx: ["+strings.Repeat("1,", MaxNodes)+"]\n")
	wantRefused(t, rs, RuleYAMLBound, "")
}

// TestBoundSize checks MaxFileSize through Load and LoadFile: the file
// is refused for its size, before it is parsed, which a syntax error at
// its start shows.
func TestBoundSize(t *testing.T) {
	big := "[" + strings.Repeat(" ", MaxFileSize)
	_, rs := loadString(t, global, big)
	wantRefused(t, rs, RuleYAMLBound, "")
	if rs.Has(RuleYAMLSyntax) {
		t.Errorf("a file past the size bound was parsed:\n%v", rs)
	}

	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(t.Context(), path, global)
	r := wantFileRefusal(t, err, path)
	if r.Rule != RuleYAMLBound {
		t.Errorf("LoadFile of %d bytes: rule %s, want %s", len(big), r.Rule, RuleYAMLBound)
	}

	if err := os.WriteFile(path, []byte(big[:MaxFileSize]), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadFile(t.Context(), path, global)
	if r := wantFileRefusal(t, err, path); r.Rule != RuleYAMLSyntax {
		t.Errorf("LoadFile of %d bytes: rule %s, want %s, so the file was parsed", MaxFileSize, r.Rule, RuleYAMLSyntax)
	}
}

// TestBoundBillionLaughs checks that the alias expansion attack is
// refused for its first alias, without expanding anything.
func TestBoundBillionLaughs(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [lol, lol, lol, lol, lol, lol, lol, lol, lol, lol]\n")
	for i := 1; i < 10; i++ {
		n, p := "a"+string(rune('0'+i)), "a"+string(rune('0'+i-1))
		b.WriteString(n + ": &" + n + " [")
		for j := range 10 {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*" + p)
		}
		b.WriteString("]\n")
	}
	_, rs := loadString(t, global, b.String())
	wantRefused(t, rs, RuleYAMLForm, "a0")
}

func TestLoadFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	f, err := LoadFile(t.Context(), path, global)
	if err != nil || f == nil || len(f.Endpoints) != 0 || f.Name != path {
		t.Fatalf("LoadFile of a missing file = %+v, %v; want an empty policy", f, err)
	}
}

func TestLoadFileDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadFile(t.Context(), dir, global)
	if r := wantFileRefusal(t, err, dir); r.Rule != RuleFileUnreadable {
		t.Errorf("rule %s, want %s", r.Rule, RuleFileUnreadable)
	}
}

func TestLoadFileReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(withRule("{host: example.com, port: 443}")), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFile(t.Context(), path, global)
	if err != nil || len(f.Endpoints) != 1 || f.Name != path {
		t.Fatalf("LoadFile = %+v, %v", f, err)
	}
}

// wantFileRefusal checks that err is a *Refusals of one refusal naming
// path, and returns that refusal.
func wantFileRefusal(t *testing.T, err error, path string) *Refusal {
	t.Helper()
	rs, ok := errors.AsType[*Refusals](err)
	if !ok || len(rs.List) == 0 {
		t.Fatalf("LoadFile error %T %v, want *Refusals", err, err)
	}
	r := rs.List[0]
	if r.File != path || !strings.Contains(r.Error(), path) {
		t.Errorf("refusal %q doesn't name %s", r.Error(), path)
	}
	return r
}
