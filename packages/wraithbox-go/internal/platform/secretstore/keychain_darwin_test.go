//go:build darwin && cgo

package secretstore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestC05AdhocShim opens the real darwin backend from the test binary,
// which the Go linker signs ad hoc, and sees the dialogs-off call made
// and every call refused with keychain-needs-team before any Keychain
// item call (C05-credentials-and-store, "Builds without a team";
// S15-02-secret-store, "Ad-hoc signed builds").
func TestC05AdhocShim(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store, err := Open(storeitem.ProgramProxyd)
	var e *Error
	if !errors.As(err, &e) || e.Rule != storeitem.RuleNeedsTeam {
		t.Fatalf("Open: %v, want %s", err, storeitem.RuleNeedsTeam)
	}
	allowed, err := dialogsAllowed()
	if err != nil || allowed {
		t.Errorf("after Open, dialogs allowed = %v, %v; want false", allowed, err)
	}

	ctx := context.Background()
	binding, err := storeitem.Parse("wraithbox/binding/github")
	if err != nil {
		t.Fatal(err)
	}
	ca, err := storeitem.Parse("wraithbox/ca/vm-1/ca-1")
	if err != nil {
		t.Fatal(err)
	}
	_, fetchErr := store.Fetch(ctx, binding, storeitem.RoleBinding)
	_, listErr := store.List(ctx, storeitem.RoleCA)
	for call, err := range map[string]error{
		"write":  store.Write(ctx, ca, storeitem.RoleCA, []byte("value")),
		"fetch":  fetchErr,
		"delete": store.Delete(ctx, ca, storeitem.RoleCA),
		"list":   listErr,
	} {
		if !errors.As(err, &e) || e.Rule != storeitem.RuleNeedsTeam {
			t.Errorf("%s: %v, want %s", call, err, storeitem.RuleNeedsTeam)
		}
	}
	// The shim read the signature, and found no team in it, rather than
	// failing to read it.
	if strings.Contains(logs.String(), "can't be read") {
		t.Errorf("the shim couldn't read the signature:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "not built yet") {
		t.Errorf("a call reached the Keychain item calls:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "rule="+string(storeitem.RuleNeedsTeam)) {
		t.Errorf("the log doesn't name %s:\n%s", storeitem.RuleNeedsTeam, logs.String())
	}
}
