package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
)

// A test that needs wb-netd's process runs the test binary again as
// wb-netd: TestMain runs run instead of the tests when childEnv is set.
// Confinement can't be undone, so it never runs in the test process
// itself.
const (
	childEnv = "WB_NETD_TEST_CHILD"
	// childArgs, when set, are the child's arguments, split on spaces.
	childArgs = "WB_NETD_TEST_ARGS"
	// childOutside names a file that the child reads after run, confined,
	// and reports on standard error.
	childOutside = "WB_NETD_TEST_OUTSIDE"
	// childVersion is the child's build version: a test binary is
	// unstamped, which no peer accepts.
	childVersion = "WB_NETD_TEST_VERSION"
)

// The lines the child prints after run about its read of childOutside.
const (
	outsideDenied  = "OUTSIDE denied"
	outsideAllowed = "OUTSIDE allowed"
)

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		os.Exit(runChild())
	}
	os.Exit(m.Run())
}

func runChild() int {
	var args []string
	if a := os.Getenv(childArgs); a != "" {
		args = strings.Fields(a)
	}
	outside := os.Getenv(childOutside)
	own := os.Getenv(childVersion)
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	code := run(context.Background(), log, args, own)
	if outside != "" {
		_, err := os.ReadFile(outside)
		switch {
		case errors.Is(err, syscall.EPERM):
			_, _ = os.Stderr.WriteString(outsideDenied + "\n")
		default:
			_, _ = os.Stderr.WriteString(outsideAllowed + "\n")
		}
	}
	return code
}
