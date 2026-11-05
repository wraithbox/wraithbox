package policyfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticNoFile(t *testing.T) {
	p, err := Static(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Generation != 1 || p.WildcardBudget != DefaultWildcardBudget || len(p.Endpoints) != 0 {
		t.Errorf("Static = %+v", p)
	}
}

func TestStaticGlobalFile(t *testing.T) {
	dir := t.TempDir()
	text := withRuleAndExt("  wildcard_budget: 7\n", "{host: example.com, port: 443}")
	if err := os.WriteFile(filepath.Join(dir, GlobalFileName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Static(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.WildcardBudget != 7 || len(p.Endpoints) != 1 || p.Endpoints[0].Source != global {
		t.Errorf("Static = %+v", p)
	}
}

// TestStaticRefusedFile checks that a refused global file refuses the
// start, with the refusal as the error.
func TestStaticRefusedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, GlobalFileName), []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Static(t.Context(), dir)
	rs, ok := errors.AsType[*Refusals](err)
	if !ok || !rs.Has(RuleVersion) {
		t.Fatalf("Static error %v, want a refusal with %s", err, RuleVersion)
	}
}
