package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoginEnvNotSet asks launchctl for a variable no session sets. The
// test never sets a login variable, because that would change the
// session of the user who runs it.
func TestLoginEnvNotSet(t *testing.T) {
	got, err := loginEnv(t.Context(), launchctlPath, "WRAITHBOX_TEST_NEVER_SET_7F3A")
	if err != nil {
		t.Fatalf("loginEnv: %v", err)
	}
	if got != "" {
		t.Errorf("loginEnv = %q, want not set", got)
	}
}

// TestLoginXDGConfigHome checks only the shape of the answer: the
// maintainer's session may or may not set the variable.
func TestLoginXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/wraithbox-test-from-the-caller")
	got, err := LoginXDGConfigHome(t.Context())
	if err != nil {
		t.Fatalf("LoginXDGConfigHome: %v", err)
	}
	if got == "/wraithbox-test-from-the-caller" {
		t.Errorf("LoginXDGConfigHome = %q, the caller's environment", got)
	}
}

// fakeLaunchctl writes a shell script that stands in for launchctl. It
// uses only shell builtins, because it runs with an empty environment.
func fakeLaunchctl(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launchctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoginEnvFailures: a launchctl that exits nonzero, or prints more
// than loginEnvMaxOutput bytes, is an error, never "not set".
func TestLoginEnvFailures(t *testing.T) {
	tests := []struct {
		name, body string
		want       error
	}{
		{"exit nonzero", "exit 3", nil},
		{"too much output", `i=0; while [ $i -lt 600 ]; do printf '/abcdefg\n'; i=$((i+1)); done`, errLoginEnvTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loginEnv(t.Context(), fakeLaunchctl(t, tt.body), "XDG_CONFIG_HOME")
			if err == nil {
				t.Fatalf("loginEnv = %q, want an error", got)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLoginEnvValue(t *testing.T) {
	got, err := loginEnv(t.Context(), fakeLaunchctl(t, `printf '/Users/me/.config\n'`), "XDG_CONFIG_HOME")
	if err != nil || got != "/Users/me/.config" {
		t.Errorf("loginEnv = %q, %v; want /Users/me/.config", got, err)
	}
	got, err = loginEnv(t.Context(), fakeLaunchctl(t, `printf '/a/../b\n'`), "XDG_CONFIG_HOME")
	if err != nil || got != "" {
		t.Errorf("loginEnv = %q, %v; want not set", got, err)
	}
}

func TestLoginValue(t *testing.T) {
	tests := []struct {
		name, out, want string
		rejected        bool
	}{
		{"not set", "", "", false},
		{"absolute", "/Users/me/.config\n", "/Users/me/.config", false},
		{"no newline", "/Users/me/.config", "/Users/me/.config", false},
		{"trailing slash", "/Users/me/.config/\n", "/Users/me/.config/", false},
		{"dot dot in a name", "/Users/me/..config\n", "/Users/me/..config", false},
		{"relative", "me/.config\n", "", true},
		{"tilde", "~/.config\n", "", true},
		{"two lines", "/a\n/b\n", "", true},
		{"nul", "/a\x00b\n", "", true},
		{"escape", "/a\x1b[2Jb\n", "", true},
		{"tab", "/a\tb\n", "", true},
		{"delete", "/a\x7fb\n", "", true},
		{"not ASCII", "/Users/m\xc3\xa9/.config\n", "", true},
		{"dot dot", "/Users/me/../other\n", "", true},
		{"dot dot at the end", "/Users/me/..\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := loginValue(tt.out)
			if got != tt.want || (reason != "") != tt.rejected {
				t.Errorf("loginValue(%q) = %q, %q; want %q, rejected %v", tt.out, got, reason, tt.want, tt.rejected)
			}
		})
	}
}

func TestCappedBuffer(t *testing.T) {
	c := &cappedBuffer{limit: 4}
	for _, p := range []string{"12", "3456", "78"} {
		n, err := c.Write([]byte(p))
		if n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	if string(c.buf) != "1234" || !c.over {
		t.Errorf("buf = %q, over = %v; want %q, true", c.buf, c.over, "1234")
	}
}
