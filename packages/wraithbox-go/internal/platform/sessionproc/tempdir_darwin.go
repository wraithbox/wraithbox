package sessionproc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// getconf is the program UserTempDir runs, at its fixed path.
const getconf = "/usr/bin/getconf"

// The bounds of the getconf run.
const (
	getconfTimeout = 5 * time.Second
	maxTempDir     = 1024
)

// UserTempDir returns the calling user's own temporary directory,
// confstr(_CS_DARWIN_USER_TEMP_DIR), which confstr makes when it is
// missing. Go has no confstr without cgo, so it runs `getconf
// DARWIN_USER_TEMP_DIR`, as the caller, with an empty environment. The
// session helper calls it as the project user, so temporary files stay
// out of the shared /tmp.
func UserTempDir(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, getconfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, getconf, "DARWIN_USER_TEMP_DIR")
	cmd.Env = []string{}
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("sessionproc: %s DARWIN_USER_TEMP_DIR: %w", getconf, err)
	}
	dir := string(bytes.TrimSuffix(out, []byte{'\n'}))
	if len(dir) == 0 || len(dir) > maxTempDir || !filepath.IsAbs(dir) || !printable(dir) {
		return "", fmt.Errorf("sessionproc: %s printed no absolute path", getconf)
	}
	return dir, nil
}

// printable reports whether s is printable ASCII only.
func printable(s string) bool {
	for _, c := range []byte(s) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
