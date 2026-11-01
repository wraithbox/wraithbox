//go:build unix

package audit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDayFileMode: a day file has mode 0600 (S09-11-audit, "Where").
// Windows has no Unix modes, so the test runs on Unix only.
func TestDayFileMode(t *testing.T) {
	h := newHarness(t, nil)
	h.w.Record(VMRunning("work", 1, "vm-start"))
	h.flush()
	h.mu.Lock()
	day := h.now
	h.mu.Unlock()
	info, err := os.Stat(filepath.Join(h.dir, dayName(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("%s has mode %v, want 0600", info.Name(), info.Mode().Perm())
	}
}
