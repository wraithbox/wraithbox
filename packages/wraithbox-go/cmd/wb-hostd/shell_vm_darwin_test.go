package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectcmd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/relay"
)

// installedGuestd is wb-guestd in the guest (internal/platform/guesttools,
// InstallDir).
const installedGuestd = "/Library/WraithBox/libexec/wb-guestd"

// pipedInput is the size of the input piped to a shell whose program
// doesn't read it: more than the flow control windows of both streams,
// so a relay that waited for the input to drain would hang.
const pipedInput = 8 << 20

// TestC09ShellVM boots the work VM as TestC11HostdVM does and runs
// `wb shell --project P` through wb-hostd, piped, for a project it adds
// to state.db (C09-guest-and-sessions, "Debug shells in V1-M2"). The
// first shell checks that it runs as the project user, that the user
// can run the installed wb-guestd, and that wb exits with the shell's
// status. The second pipes 8 MiB to a program that never reads it and
// sees wb exit with its status, and in time. It is needs-vm and
// needs-host, like TestC11HostdVM, and it claims and removes <data> the
// same way. It runs with the variables and the `go test` command of
// `mise run go:test-vm`, with `-run '^TestC09ShellVM$'` in place of that
// task's own.
func TestC09ShellVM(t *testing.T) {
	r := newVMRun(t)
	r.cloneFixture()
	r.startChild(filepath.Join(r.app, "Contents", "MacOS", "wb-hostd"))

	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	r.waitRecord("guest data mount accepted", began, 2*time.Minute)
	id := r.addProject()

	script := "id -un\n" +
		"test -x " + installedGuestd + " && echo guestd-executable\n" +
		"exit 7\n"
	code, stdout, stderr := r.shell(id, strings.NewReader(script))
	t.Logf("wb shell: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	if code != 7 {
		t.Errorf("wb shell: exit %d, want 7", code)
	}
	for _, want := range []string{"wbp", "guestd-executable"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q misses %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, "wb: debug shell ") {
		t.Errorf("stderr %q misses the label line", stderr)
	}

	piped := io.MultiReader(strings.NewReader("exec /bin/sh -c 'sleep 2; exit 3'\n"),
		bytes.NewReader(bytes.Repeat([]byte("#\n"), pipedInput/2)))
	start := time.Now()
	code, _, stderr = r.shell(id, piped)
	took := time.Since(start)
	t.Logf("measure: wb shell with %d bytes piped to a program that doesn't read them: exit %d after %v\n%s", pipedInput, code, took, stderr)
	if code != 3 {
		t.Errorf("wb shell piped: exit %d, want 3", code)
	}
	if took > time.Minute {
		t.Errorf("wb shell piped took %v, want under a minute", took)
	}
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// addProject inserts a project of the work VM into state.db, as
// wb project place does, and returns its id.
func (r *vmRun) addProject() string {
	t := r.t
	db, err := state.Open(t.Context(), r.paths.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	err = db.Projects(t.Context(), func(tx *state.ProjectTx) error {
		return tx.Insert(state.Project{
			ID: id, Realm: "native", KeyPath: "/c09shell/0", Name: "c09shell",
			Placement: state.PlacementWork, Created: time.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// shell runs `wb shell --project id` against the child, with stdin piped
// and no terminal, and returns its exit status and output.
func (r *vmRun) shell(id string, stdin io.Reader) (int, string, string) {
	var stdout, stderr bytes.Buffer
	env := projectcmd.SystemEnv(slog.New(slog.DiscardHandler))
	env.Paths = r.paths
	env.Stdout, env.Stderr = &stdout, &stderr
	env.Terminal = func() (relay.Config, func(), error) {
		return relay.Config{
			Charset: tty.Charset{Source: "LANG", Value: "en_US.UTF-8", UTF8: true},
			Stdin:   stdin, Stdout: &stdout, Stderr: &stderr,
		}, func() {}, nil
	}
	env.Lookup = func(string) (string, bool) { return "", false }
	ctx, cancel := context.WithTimeout(r.t.Context(), 3*time.Minute)
	defer cancel()
	code := projectcmd.RunShell(ctx, env, r.scratch, []string{"--project", id})
	return code, stdout.String(), stderr.String()
}
