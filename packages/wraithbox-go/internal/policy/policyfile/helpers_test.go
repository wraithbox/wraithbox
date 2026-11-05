package policyfile

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

const testProject = "0123456789abcdef0123456789abcdef"

var (
	global  = policy.Source{Kind: policy.SourceGlobal}
	project = policy.Source{Kind: policy.SourceProject, Project: testProject}
	repo    = policy.Source{
		Kind: policy.SourceRepository, Project: testProject, Repository: "https://github.com/example/project",
	}
)

// testFile is the name every test file is loaded under, so a test can
// see that a refusal names it.
const testFile = "test-policy.yaml"

// withRule returns a file with one rule, r, whose endpoints are the
// given flow mappings, and whose binaries are the universal entry.
func withRule(endpoints ...string) string {
	var b strings.Builder
	b.WriteString("version: 1\nnetwork_policies:\n  r:\n    endpoints:\n")
	for _, e := range endpoints {
		b.WriteString("      - " + e + "\n")
	}
	b.WriteString("    binaries:\n      - path: \"/**\"\n")
	return b.String()
}

// withRuleAndExt returns withRule's file and a `wraithbox` key holding
// ext, indented two spaces per line already.
func withRuleAndExt(ext string, endpoints ...string) string {
	return withRule(endpoints...) + "wraithbox:\n" + ext
}

// loadString loads text as testFile. It fails the test on an error that
// isn't a *Refusals.
func loadString(t *testing.T, src policy.Source, text string) (*File, *Refusals) {
	t.Helper()
	f, err := Load(t.Context(), testFile, []byte(text), src)
	if err == nil {
		return f, nil
	}
	rs, ok := errors.AsType[*Refusals](err)
	if !ok {
		t.Fatalf("Load error %T %v, want *Refusals", err, err)
	}
	return nil, rs
}

// wantRefused checks that rs holds a refusal with rule, that its
// message names the file, and that it shows value, escaped, when value
// isn't "". The value may be part of what the message shows.
func wantRefused(t *testing.T, rs *Refusals, rule, value string) *Refusal {
	t.Helper()
	if rs == nil {
		t.Fatalf("the file loaded, want a refusal with %s", rule)
	}
	for _, r := range rs.List {
		if r.Rule != rule {
			continue
		}
		msg := r.Error()
		if r.File != testFile || !strings.Contains(msg, testFile) {
			t.Errorf("refusal %q doesn't name the file %s", msg, testFile)
		}
		shown := strings.Trim(strconv.QuoteToASCII(value), `"`)
		if value != "" && !strings.Contains(msg, shown) {
			t.Errorf("refusal %q doesn't show the value %s", msg, shown)
		}
		return r
	}
	t.Fatalf("refusals:\n%v\nwant one with rule %s", rs, rule)
	return nil
}

// mustLoad loads text as testFile and fails the test when it is refused.
func mustLoad(t *testing.T, src policy.Source, text string) *File {
	t.Helper()
	f, rs := loadString(t, src, text)
	if rs != nil {
		t.Fatalf("refused, want loaded:\n%v", rs)
	}
	return f
}
