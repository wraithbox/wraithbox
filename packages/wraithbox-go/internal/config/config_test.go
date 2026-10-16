package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func refusal(t *testing.T, err error) *Refusal {
	t.Helper()
	r, ok := errors.AsType[*Refusal](err)
	if !ok {
		t.Fatalf("error %v (%T) is not a *Refusal", err, err)
	}
	return r
}

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Defaults() || cfg.CPUCount != 4 || cfg.MemoryMiB != 8192 {
		t.Errorf("cfg = %+v, want 4 vCPU and 8192 MiB", cfg)
	}
}

func TestAccepted(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Config
	}{
		{"empty", "", Defaults()},
		{"comment only", "# nothing\n", Defaults()},
		{"cpu", "vm_cpu_count = 2\n", Config{CPUCount: 2, MemoryMiB: 8192}},
		{"both", "vm_cpu_count = 6\nvm_memory_mib = 16384\n", Config{CPUCount: 6, MemoryMiB: 16384}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(t.Context(), writeConfig(t, tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tt.want {
				t.Errorf("cfg = %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

// TestC12Refused feeds each rule a violation and sees the file refused
// with its rule, the line and the key (S17-05-upgrades-and-migrations,
// "Configuration"). The unknown keys are the C12-install-and-downloads,
// "Migrations" case.
func TestC12Refused(t *testing.T) {
	tests := []struct {
		name string
		body string
		rule Rule
		line int
		key  string
	}{
		{"unknown top-level key", "vm_cpu_count = 2\nvm_disk_gib = 64\n", RuleKeyUnknown, 2, "vm_disk_gib"},
		{"unknown table", "\n[git]\npath = \"/usr/bin/git\"\n", RuleKeyUnknown, 2, "git"},
		{"unknown dotted key", "vm.cpu = 2\n", RuleKeyUnknown, 1, "vm.cpu"},
		{"first of two unknown keys", "a = 1\nvm_cpu_count = 2\nb = 2\n", RuleKeyUnknown, 1, "a"},
		{"zero", "vm_cpu_count = 0\n", RuleValueInvalid, 0, "vm_cpu_count"},
		{"negative", "vm_memory_mib = -1\n", RuleValueInvalid, 0, "vm_memory_mib"},
		{"string", "vm_cpu_count = \"4\"\n", RuleValueInvalid, 1, "vm_cpu_count"},
		{"float", "\nvm_memory_mib = 8192.0\n", RuleValueInvalid, 2, "vm_memory_mib"},
		{"over uint32", "vm_cpu_count = 4294967296\n", RuleValueInvalid, 0, "vm_cpu_count"},
		{"over int64", "vm_memory_mib = 9223372036854775808\n", RuleValueInvalid, 1, "vm_memory_mib"},
		{"duplicate key", "vm_cpu_count = 2\nvm_cpu_count = 3\n", RuleValueInvalid, 2, "vm_cpu_count"},
		{"table for a number", "[vm_cpu_count]\n", RuleValueInvalid, 1, "vm_cpu_count"},
		{"syntax", "vm_cpu_count =\n", RuleSyntaxInvalid, 1, ""},
		{"not toml", "\x00\x01", RuleSyntaxInvalid, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, tt.body)
			cfg, err := Load(t.Context(), dir)
			r := refusal(t, err)
			if cfg != (Config{}) {
				t.Errorf("a refusal returned settings %+v", cfg)
			}
			if r.Rule != tt.rule || r.Line != tt.line || r.Key != tt.key {
				t.Errorf("refusal = %s line %d key %q, want %s line %d key %q (%v)",
					r.Rule, r.Line, r.Key, tt.rule, tt.line, tt.key, r)
			}
			if r.File != filepath.Join(dir, FileName) || !strings.Contains(r.Error(), r.File) {
				t.Errorf("refusal %q doesn't name the file", r)
			}
		})
	}
}

// TestUnknownNestedKey: a key unknown inside a known table is refused
// as well. V1 has no table, so the test decodes into one of its own.
func TestUnknownNestedKey(t *testing.T) {
	var target struct {
		Audit struct {
			RetentionDays int `toml:"retention_days"`
		} `toml:"audit"`
	}
	body := "[audit]\nretention_days = 30\nretention_weeks = 4\n"
	r := decode("config.toml", []byte(body), &target, nil)
	if r == nil || r.Rule != RuleKeyUnknown || r.Line != 3 || r.Key != "audit.retention_weeks" {
		t.Fatalf("refusal = %+v, want %s at line 3 for audit.retention_weeks", r, RuleKeyUnknown)
	}
}

// C12-install-and-downloads, "Migrations": a renamed key, from a table
// the test injects, is refused with config-key-renamed and the new key.
func TestC12RenamedKey(t *testing.T) {
	dir := writeConfig(t, "# settings\nvm_cores = 2\n")
	path := filepath.Join(dir, FileName)
	renamed := map[string]string{"vm_cores": "vm_cpu_count"}
	_, err := load(t.Context(), path, renamed)
	r := refusal(t, err)
	if r.Rule != RuleKeyRenamed || r.Line != 2 || r.Key != "vm_cores" || r.NewKey != "vm_cpu_count" {
		t.Fatalf("refusal = %+v, want %s at line 2 naming vm_cpu_count", r, RuleKeyRenamed)
	}
	if !strings.Contains(r.Error(), "vm_cpu_count") {
		t.Errorf("message %q doesn't name the new key", r)
	}
}

func TestRenamedTableEmptyInV1(t *testing.T) {
	if len(renamedKeys) != 0 {
		t.Errorf("renamedKeys = %v, want empty in V1", renamedKeys)
	}
}

func TestUnreadable(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Load(t.Context(), dir)
		if r := refusal(t, err); r.Rule != RuleFileUnreadable {
			t.Errorf("rule = %s, want %s", r.Rule, RuleFileUnreadable)
		}
	})
	t.Run("too large", func(t *testing.T) {
		body := "# " + strings.Repeat("x", maxFileSize) + "\n"
		_, err := Load(t.Context(), writeConfig(t, body))
		if r := refusal(t, err); r.Rule != RuleFileUnreadable {
			t.Errorf("rule = %s, want %s", r.Rule, RuleFileUnreadable)
		}
	})
}
