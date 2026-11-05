//go:build unix

package policyfile

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestLoadFileFIFO checks that a FIFO in the place of the policy file
// is refused without blocking: opening one waits for a writer.
func TestLoadFileFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := LoadFile(t.Context(), path, global)
		done <- err
	}()
	select {
	case err := <-done:
		if r := wantFileRefusal(t, err, path); r.Rule != RuleFileUnreadable {
			t.Errorf("rule %s, want %s", r.Rule, RuleFileUnreadable)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadFile blocked on a FIFO")
	}
}
