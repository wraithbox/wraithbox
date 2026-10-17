package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// newData makes a <data> with both slots' bundles, and points dataDir
// at it for the test.
func newData(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	for _, s := range vmbundle.Slots() {
		if err := os.MkdirAll(vmbundle.Path(data, s), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := dataDir
	dataDir = func(*slog.Logger) (string, error) { return data, nil }
	t.Cleanup(func() { dataDir = old })
	return data
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// The setups of TestStartChecks, each on a <data> with both bundles.

func removeBundle(t *testing.T, data string) {
	if err := os.Remove(vmbundle.Path(data, "work")); err != nil {
		t.Fatal(err)
	}
}

func bundleAsFile(t *testing.T, data string) {
	p := vmbundle.Path(data, "isolated")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func bundleLinkedToWork(t *testing.T, data string) {
	p := vmbundle.Path(data, "isolated")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(vmbundle.Path(data, "work"), p); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// TestStartChecks: each start rule of S04-04-vm-processes, "`wb-diskfmt`",
// with the slot from the environment only.
func TestStartChecks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		slot  string
		setup func(t *testing.T, data string)
		rule  string
	}{
		{name: "argument", args: []string{"--bundle=/x"}, slot: "work", rule: ruleArguments},
		{name: "slot missing", rule: ruleSlotMissing},
		{name: "slot unknown", slot: "build", rule: ruleSlotUnknown},
		{name: "slot path", slot: "../work", rule: ruleSlotUnknown},
		{name: "bundle missing", slot: "work", setup: removeBundle, rule: ruleBundleMissing},
		{name: "bundle a file", slot: "isolated", setup: bundleAsFile, rule: ruleBundleMissing},
		{name: "bundle linked to the other slot", slot: "isolated", setup: bundleLinkedToWork, rule: ruleSlotBundleMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := newData(t)
			if tc.setup != nil {
				tc.setup(t, data)
			}
			var logs bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&logs, nil))
			_, r := checkStart(log, tc.args, env(map[string]string{SlotEnv: tc.slot}))
			if r == nil || r.Rule != tc.rule {
				t.Fatalf("refusal = %+v, want %s", r, tc.rule)
			}
		})
	}
}

func TestStartAccepts(t *testing.T) {
	data := newData(t)
	for _, slot := range vmbundle.Slots() {
		bundle, r := checkStart(slog.Default(), nil, env(map[string]string{SlotEnv: slot}))
		if r != nil {
			t.Fatalf("%s: %+v", slot, r)
		}
		realData, err := filepath.EvalSymlinks(data)
		if err != nil {
			t.Fatal(err)
		}
		if want := vmbundle.Path(realData, slot); bundle != want {
			t.Errorf("%s: bundle = %s, want %s", slot, bundle, want)
		}
	}
}
