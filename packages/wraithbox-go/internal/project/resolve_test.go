package project

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestResolveRefusesWithoutOwnDirectories: a Resolver that doesn't know
// <data>, <config> and <logs> can't check a key against them, so it
// refuses every directory (fail closed).
func TestResolveRefusesWithoutOwnDirectories(t *testing.T) {
	var log bytes.Buffer
	r := &Resolver{Git: "/usr/bin/git", Log: slog.New(slog.NewJSONHandler(&log, nil))}
	_, err := r.Resolve(t.Context(), t.TempDir())
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != RuleOwnUnset {
		t.Fatalf("Resolve = %v, want %s", err, RuleOwnUnset)
	}
	if !strings.Contains(log.String(), `"rule":"project-own-dirs-unset"`) {
		t.Errorf("log = %s, want the rule", log.String())
	}
}
