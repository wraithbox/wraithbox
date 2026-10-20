package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestUsage: a bad command line exits 2 before anything starts.
func TestUsage(t *testing.T) {
	cases := [][]string{
		nil,
		{"start"},
		{"boot", "extra"},
		{"boot", "-cpu-count", "0"},
		{"hello", "-port", "4294967296"},
		{"hello", "-port", "-1"},
		{"connect", "-listen", "x.sock"},
		{"connect", "-port", "900"},
		{"connect", "-port", "900", "-listen", "socket"},
		{"connect", "-port", "900", "-listen", ".sock"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Errorf("%q: exit %d, want 2; stderr %s", args, code, stderr.String())
		}
	}
}

// TestUnstamped: an unstamped wb-dev-vm refuses to start wb-vmd, which
// would refuse it anyway, and says how to build it.
func TestUnstamped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"boot"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unstamped") {
		t.Fatalf("exit %d, stderr %s; want 1 and the unstamped refusal", code, stderr.String())
	}
}

// TestEndpointOf: -listen names <directory>/<name>.sock, and a relative
// path is made absolute.
func TestEndpointOf(t *testing.T) {
	runDir := t.TempDir()
	dir, name, err := endpointOf(filepath.Join(runDir, "spike.sock"))
	if err != nil || dir != runDir || name != "spike" {
		t.Fatalf("endpointOf = %q, %q, %v", dir, name, err)
	}
	dir, _, err = endpointOf("spike.sock")
	if err != nil || !filepath.IsAbs(dir) {
		t.Fatalf("relative: dir %q, %v", dir, err)
	}
}
