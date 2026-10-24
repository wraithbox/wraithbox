package logpipe

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each process gets one file, the same one on each call, and Close
// closes them all.
func TestFiles(t *testing.T) {
	dir := t.TempDir()
	fs := NewFiles(dir, nil)
	a := fs.Writer("wb-vmd-work")
	if a != fs.Writer("wb-vmd-work") {
		t.Fatal("a second Writer opened another file")
	}
	if err := Copy(strings.NewReader("one\n"), a, "wb-vmd-work"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "wb-vmd-work.log"))
	if err != nil || !bytes.Contains(b, []byte(`"line":"one"`)) {
		t.Fatalf("log %q, %v", b, err)
	}
	if fs.Writer("wb-vmd-work") != io.Discard {
		t.Error("Writer after Close isn't io.Discard")
	}
}

// A file that can't be opened, here for a name outside the process
// form, drops its lines, and the failure is logged with its rule.
func TestFilesOpenFailedDrops(t *testing.T) {
	var logs bytes.Buffer
	fs := NewFiles(t.TempDir(), slog.New(slog.NewJSONHandler(&logs, nil)))
	if fs.Writer("../escape") != io.Discard {
		t.Fatal("Writer for a bad name isn't io.Discard")
	}
	if !strings.Contains(logs.String(), `"rule":"`+RuleOpenFailed+`"`) {
		t.Errorf("log %s", logs.String())
	}
}
