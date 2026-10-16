package startup

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

func TestPrepareReady(t *testing.T) {
	host, r := Prepare(t.Context(), t.TempDir(), t.TempDir())
	if r != nil {
		t.Fatal(r)
	}
	defer func() { _ = host.State.Close() }()
	if host.Config != config.Defaults() {
		t.Errorf("config = %+v, want the defaults", host.Config)
	}
	if g, err := host.State.NextGeneration(t.Context(), "macos-work"); err != nil || g != 1 {
		t.Errorf("NextGeneration = %d, %v", g, err)
	}
}

// C12-install-and-downloads, "Migrations": an unknown configuration key
// puts wb-hostd in refusing mode with its rule, naming the file and
// line. The database isn't touched.
func TestC12UnknownKeyRefuses(t *testing.T) {
	configDir, dataDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, config.FileName), []byte("vm_cpu_count = 2\nwarm_start = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, r := Prepare(t.Context(), configDir, dataDir)
	if host != nil || r == nil || r.Rule != string(config.RuleKeyUnknown) {
		t.Fatalf("Prepare = %v, %v; want refusing with %s", host, r, config.RuleKeyUnknown)
	}
	if !strings.Contains(r.Detail, "line 2") || !strings.Contains(r.Detail, "warm_start") {
		t.Errorf("detail %q doesn't name the line and key", r.Detail)
	}
	if _, err := os.Stat(filepath.Join(dataDir, state.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refusing mode created the database: %v", err)
	}
}

// C12-install-and-downloads, "Migrations": a newer database puts
// wb-hostd in refusing mode with its rule and the versions.
func TestC12NewerDatabaseRefuses(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, state.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	host, r := Prepare(t.Context(), t.TempDir(), dataDir)
	if host != nil || r == nil || r.Rule != string(state.RuleNewer) {
		t.Fatalf("Prepare = %v, %v; want refusing with %s", host, r, state.RuleNewer)
	}
	if !strings.Contains(r.Detail, "version 99") {
		t.Errorf("detail %q doesn't name the version", r.Detail)
	}
}

// Each package's refusal keeps its rule in refusing mode, the backup
// and migration failures and the renamed key included, which the
// packages' own tests produce with injected failures.
func TestRefusingKeepsTheRule(t *testing.T) {
	tests := []struct {
		err  error
		rule string
	}{
		{&state.Refusal{Rule: state.RuleBackupFailed}, "state-db-backup-failed"},
		{&state.Refusal{Rule: state.RuleMigrationFailed}, "state-db-migration-failed"},
		{&state.Refusal{Rule: state.RuleNewer}, "state-db-newer"},
		{&config.Refusal{Rule: config.RuleKeyRenamed}, "config-key-renamed"},
		{&config.Refusal{Rule: config.RuleKeyUnknown}, "config-key-unknown"},
		{&config.Refusal{Rule: config.RuleValueInvalid}, "config-value-invalid"},
		{errors.New("other"), ruleStartFailed},
	}
	for _, tt := range tests {
		if r := refusing(tt.err); r.Rule != tt.rule || !errors.Is(r, tt.err) {
			t.Errorf("refusing(%T %v) = %s, want %s", tt.err, tt.err, r.Rule, tt.rule)
		}
	}
}
