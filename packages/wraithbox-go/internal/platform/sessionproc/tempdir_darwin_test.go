package sessionproc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

func TestUserTempDir(t *testing.T) {
	dir, err := sessionproc.UserTempDir(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("%q isn't absolute", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("%q isn't a directory: %v", dir, err)
	}
}
