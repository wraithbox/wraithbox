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

// A test that needs wb-diskfmt's process runs the test binary again as
// wb-diskfmt: TestMain runs run instead of the tests when childEnv is
// set. Confinement can't be undone, so it never runs in the test
// process itself. Only the child replaces dataDir; the shipped binary
// has no override of the bundle path.
const (
	childEnv = "WB_DISKFMT_TEST_CHILD"
	// childData is <data> for the child.
	childData = "WB_DISKFMT_TEST_DATA"
	// childArgs, when set, are the child's arguments, split on spaces.
	childArgs = "WB_DISKFMT_TEST_ARGS"
	// childOutside names a file outside the bundle that the child reads
	// after run, confined, and reports on standard error.
	childOutside = "WB_DISKFMT_TEST_OUTSIDE"
	// childVersion is the child's build version: a test binary is
	// unstamped, which no peer accepts.
	childVersion = "WB_DISKFMT_TEST_VERSION"
)

// The lines the child prints after run about its read outside the
// bundle.
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
	data := os.Getenv(childData)
	dataDir = func(*slog.Logger) (string, error) { return data, nil }
	var args []string
	if a := os.Getenv(childArgs); a != "" {
		args = strings.Fields(a)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	code := run(context.Background(), log, args, os.Getenv, os.Getenv(childVersion))
	if outside := os.Getenv(childOutside); outside != "" {
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
