package tty_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

const childEnv = "WB_TTY_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		if _, err := tty.Open(); err != nil {
			fmt.Println("REFUSED", err)
			os.Exit(0)
		}
		fmt.Println("OPENED")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestNoTerminal: a process in a new session has no controlling
// terminal, as under launchd, and Open fails, so a confirmation is
// refused (SEC14-no-fake-approvals).
func TestNoTerminal(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "REFUSED") {
		t.Errorf("a process without a controlling terminal: %s", out)
	}
}
