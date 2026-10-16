//go:build unix

package config

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO at config.toml is refused without blocking: opening one for
// reading waits for a writer. Windows has no FIFO in the file system.
func TestFIFORefused(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, FileName), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Load(t.Context(), dir)
		done <- err
	}()
	select {
	case err := <-done:
		if r := refusal(t, err); r.Rule != RuleFileUnreadable {
			t.Errorf("rule = %s, want %s", r.Rule, RuleFileUnreadable)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO")
	}
}
