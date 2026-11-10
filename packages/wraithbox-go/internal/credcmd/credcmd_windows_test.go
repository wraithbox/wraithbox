package credcmd

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// TestC05CredWindowsLater sees wb's own Env on a Windows host refuse
// `wb cred` as later in the platform matrix, before it reads or starts
// anything.
func TestC05CredWindowsLater(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := SystemEnv(slog.New(slog.DiscardHandler))
	if env.Host.OS != platform.Windows {
		t.Fatalf("SystemEnv has the host %+v on Windows", env.Host)
	}
	env.Stdin, env.Stdout, env.Stderr = errReader{t}, &stdout, &stderr
	code := Run(t.Context(), env, []string{"set", "x"})
	if code != exitFailure || !strings.HasSuffix(strings.TrimRight(stderr.String(), "\n"), "("+RuleHostLater+")") || stdout.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
