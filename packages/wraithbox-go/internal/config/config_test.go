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
		{"cpu", "vm_cpu_count = 2\n", Config{CPUCount: 2, MemoryMiB: 8192, DataDiskGiB: 128}},
		{"both", "vm_cpu_count = 6\nvm_memory_mib = 16384\n", Config{CPUCount: 6, MemoryMiB: 16384, DataDiskGiB: 128}},
		{"data disk", "vm_data_disk_gib = 8\n", Config{CPUCount: 4, MemoryMiB: 8192, DataDiskGiB: 8}},
		{"data disk at the top", "vm_data_disk_gib = 1024\n", Config{CPUCount: 4, MemoryMiB: 8192, DataDiskGiB: 1024}},
		{"git", "git = \"/opt/homebrew/bin/git\"\n", Config{CPUCount: 4, MemoryMiB: 8192, DataDiskGiB: 128, Git: "/opt/homebrew/bin/git"}},
		// A relative path is kept: hostgit.Resolve refuses it, as a
		// failed git check (S08-08-host-git, "Which git").
		{"git relative", "git = \"git\"\n", Config{CPUCount: 4, MemoryMiB: 8192, DataDiskGiB: 128, Git: "git"}},
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
		{"unknown table", "\n[audit]\nretention = 30\n", RuleKeyUnknown, 2, "audit"},
		{"git as a table", "\n[git]\npath = \"/usr/bin/git\"\n", RuleValueInvalid, 2, "git"},
		{"git as a number", "git = 2\n", RuleValueInvalid, 1, "git"},
		{"unknown dotted key", "vm.cpu = 2\n", RuleKeyUnknown, 1, "vm.cpu"},
		{"first of two unknown keys", "a = 1\nvm_cpu_count = 2\nb = 2\n", RuleKeyUnknown, 1, "a"},
		{"zero", "vm_cpu_count = 0\n", RuleValueInvalid, 0, "vm_cpu_count"},
		{"negative", "vm_memory_mib = -1\n", RuleValueInvalid, 0, "vm_memory_mib"},
		{"string", "vm_cpu_count = \"4\"\n", RuleValueInvalid, 1, "vm_cpu_count"},
		{"float", "\nvm_memory_mib = 8192.0\n", RuleValueInvalid, 2, "vm_memory_mib"},
		{"over uint32", "vm_cpu_count = 4294967296\n", RuleValueInvalid, 0, "vm_cpu_count"},
		{"over int64", "vm_memory_mib = 9223372036854775808\n", RuleValueInvalid, 1, "vm_memory_mib"},
		{"data disk too small", "vm_data_disk_gib = 7\n", RuleValueInvalid, 0, "vm_data_disk_gib"},
		{"data disk too large", "vm_data_disk_gib = 1025\n", RuleValueInvalid, 0, "vm_data_disk_gib"},
		{"upper-case key", "VM_CPU_COUNT = 2\n", RuleKeyUnknown, 1, "VM_CPU_COUNT"},
		{"mixed-case key", "\nVm_Cpu_Count = 2\n", RuleKeyUnknown, 2, "Vm_Cpu_Count"},
		{"case-variant duplicate", "vm_cpu_count = 2\nVM_CPU_COUNT = 3\n", RuleKeyUnknown, 2, "VM_CPU_COUNT"},
		{"quoted upper-case key", "\"VM_MEMORY_MIB\" = 2\n", RuleKeyUnknown, 1, "VM_MEMORY_MIB"},
		{"upper-case key in an inline table", "x = { VM_CPU_COUNT = 2 }\n", RuleKeyUnknown, 1, "x"},
		{"duplicate key", "vm_cpu_count = 2\nvm_cpu_count = 3\n", RuleSyntaxInvalid, 2, "vm_cpu_count"},
		{"key then table", "vm_cpu_count = 2\n[vm_cpu_count]\n", RuleSyntaxInvalid, 2, "vm_cpu_count"},
		{"table for a number", "[vm_cpu_count]\n", RuleValueInvalid, 1, "vm_cpu_count"},
		{"two keys below a number", "vm_cpu_count.a = 1\nvm_cpu_count.b = 2\n", RuleValueInvalid, 1, "vm_cpu_count.a"},
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

// auditTarget has a nested table, which V1's settings don't.
type auditTarget struct {
	Audit struct {
		RetentionDays int `toml:"retention_days"`
	} `toml:"audit"`
}

// TestNestedKeys: inside a known table, a key must match exactly too,
// and a key or table defined twice is refused. V1 has no table, so the
// test decodes into one of its own.
func TestNestedKeys(t *testing.T) {
	tests := []struct {
		name string
		body string
		rule Rule
		line int
		key  string
	}{
		{"unknown nested key", "[audit]\nretention_days = 30\nretention_weeks = 4\n", RuleKeyUnknown, 3, "audit.retention_weeks"},
		{"upper-case nested key", "[audit]\nRETENTION_DAYS = 30\n", RuleKeyUnknown, 2, "audit.RETENTION_DAYS"},
		{"upper-case table", "[AUDIT]\nretention_days = 30\n", RuleKeyUnknown, 1, "AUDIT"},
		{"dotted upper-case key", "audit.Retention_Days = 30\n", RuleKeyUnknown, 1, "audit.Retention_Days"},
		{"inline table", "audit = { retention_days = 1, x = 2 }\n", RuleKeyUnknown, 1, "audit.x"},
		{"table twice", "[audit]\n[audit]\n", RuleSyntaxInvalid, 2, "audit"},
		{"nested key twice", "[audit]\nretention_days = 1\nretention_days = 2\n", RuleSyntaxInvalid, 3, "audit.retention_days"},
		{"dotted and table key twice", "audit.retention_days = 1\n[audit]\nretention_days = 2\n", RuleSyntaxInvalid, 3, "audit.retention_days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target auditTarget
			r := decode("config.toml", []byte(tt.body), &target, nil)
			if r == nil || r.Rule != tt.rule || r.Line != tt.line || r.Key != tt.key {
				t.Fatalf("refusal = %+v, want %s at line %d for %s", r, tt.rule, tt.line, tt.key)
			}
		})
	}
	t.Run("accepted", func(t *testing.T) {
		var target auditTarget
		for _, body := range []string{"[audit]\nretention_days = 3\n", "audit.retention_days = 3\n", "audit = { retention_days = 3 }\n"} {
			target.Audit.RetentionDays = 0
			if r := decode("config.toml", []byte(body), &target, nil); r != nil || target.Audit.RetentionDays != 3 {
				t.Errorf("%q: %v, %d", body, r, target.Audit.RetentionDays)
			}
		}
	})
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

// A renamed dotted key matches on the whole key, and a renamed table on
// its prefix.
func TestRenamedDottedKey(t *testing.T) {
	renamed := map[string]string{"vm.cores": "vm_cpu_count", "old": "audit"}
	tests := []struct{ body, key, newKey string }{
		{"vm.cores = 2\n", "vm.cores", "vm_cpu_count"},
		{"old.retention_days = 2\n", "old", "audit"},
		{"[old]\nretention_days = 2\n", "old", "audit"},
	}
	for _, tt := range tests {
		var target auditTarget
		r := decode("config.toml", []byte(tt.body), &target, renamed)
		if r == nil || r.Rule != RuleKeyRenamed || r.Key != tt.key || r.NewKey != tt.newKey {
			t.Errorf("%q: refusal = %+v, want %s for %s naming %s", tt.body, r, RuleKeyRenamed, tt.key, tt.newKey)
		}
	}
}

// gitTarget has an array of tables, which V1's settings don't.
type gitTarget struct {
	Git []struct {
		Path string `toml:"path"`
	} `toml:"git"`
}

// TestArrayOfTables: keys below an array of tables match exactly, and a
// key defined twice in one element is refused, while each element has
// its own keys.
func TestArrayOfTables(t *testing.T) {
	tests := []struct {
		name string
		body string
		rule Rule
		line int
		key  string
	}{
		{"upper-case key", "[[git]]\nPATH = \"a\"\n", RuleKeyUnknown, 2, "git.PATH"},
		{"upper-case header", "[[GIT]]\npath = \"a\"\n", RuleKeyUnknown, 1, "GIT"},
		{"case-variant in a later element", "[[git]]\npath = \"a\"\n[[git]]\nPath = \"b\"\n", RuleKeyUnknown, 4, "git.Path"},
		{"upper-case key inline", "git = [{ PATH = \"b\" }]\n", RuleKeyUnknown, 1, "git.PATH"},
		{"key twice in an element", "[[git]]\npath = \"a\"\npath = \"b\"\n", RuleSyntaxInvalid, 3, "git.path"},
		{"key twice inline", "git = [{ path = \"a\", path = \"b\" }]\n", RuleSyntaxInvalid, 1, "git.path"},
		{"table after array", "[[git]]\npath = \"a\"\n[git]\n", RuleSyntaxInvalid, 3, "git"},
		{"array after key", "git = []\n[[git]]\n", RuleSyntaxInvalid, 2, "git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target gitTarget
			r := decode("config.toml", []byte(tt.body), &target, nil)
			if r == nil || r.Rule != tt.rule || r.Line != tt.line || r.Key != tt.key {
				t.Fatalf("refusal = %+v, want %s at line %d for %s", r, tt.rule, tt.line, tt.key)
			}
		})
	}
	t.Run("accepted", func(t *testing.T) {
		for _, body := range []string{
			"[[git]]\npath = \"a\"\n[[git]]\npath = \"b\"\n",
			"git = [{ path = \"a\" }, { path = \"b\" }]\n",
		} {
			var target gitTarget
			if r := decode("config.toml", []byte(body), &target, nil); r != nil || len(target.Git) != 2 || target.Git[1].Path != "b" {
				t.Errorf("%q: %v, %+v", body, r, target.Git)
			}
		}
	})
}

// A map's keys are data, kept as written, so they aren't checked
// against tags.
func TestMapKeysOpen(t *testing.T) {
	var target struct {
		Hosts map[string]int `toml:"hosts"`
	}
	if r := decode("config.toml", []byte("[hosts]\nExample_Host = 1\n"), &target, nil); r != nil || target.Hosts["Example_Host"] != 1 {
		t.Errorf("map: %v, %v", r, target.Hosts)
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
