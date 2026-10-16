//go:build unix

package logpipe

import (
	"os"
	"path/filepath"
	"testing"
)

// Log files are mode 600, the rotated ones too. Windows has no mode
// bits, so this runs on Unix hosts only.
func TestLogFileMode(t *testing.T) {
	dir := t.TempDir()
	lf, err := OpenFile(dir, "wb-netd")
	if err != nil {
		t.Fatal(err)
	}
	lf.maxSize = 8
	for range 3 {
		if _, err := lf.Write([]byte("0123456\n")); err != nil {
			t.Fatal(err)
		}
	}
	_ = lf.Close()
	for _, name := range []string{"wb-netd.log", "wb-netd.log.1", "wb-netd.log.2"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if m := info.Mode().Perm(); m != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, m)
		}
	}
}
