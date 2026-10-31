package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// The host terminal variables the shells get, which the guest's
// environment must show unchanged.
const (
	hostTerm = "xterm-256color"
	hostLang = "en_US.UTF-8"
)

// TestC09ShellVM boots the work VM as TestC11HostdVM does and runs
// `wb shell --project P` through wb-hostd, piped, for projects it adds
// to state.db (C09-guest-and-sessions, "Debug shells in V1-M2"). The
// checks that need a terminal are attended, and C09 lists them. It is
// needs-vm and needs-host, like TestC11HostdVM, and it claims and
// removes <data> the same way. It runs with the variables and the `go
// test` command of `mise run go:test-vm`, with `-run '^TestC09ShellVM$'`
// in place of that task's own.
func TestC09ShellVM(t *testing.T) {
	r := newVMRun(t)
	r.cloneFixture()
	r.startChild(filepath.Join(r.app, "Contents", "MacOS", "wb-hostd"))

	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	r.waitRecord("guest data mount accepted", began, 2*time.Minute)
	id := r.addProject("c09shell")
	other := r.addProject("c09other")

	user := r.checkUserAndEnv(id)
	r.checkExits(id)
	r.checkIsolation(id, other, user)
	r.checkPipes(id)
	r.checkStopRefused(id)
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// checkUserAndEnv runs a shell as the project user, sees that the user
// can run the installed wb-guestd, that the environment is exactly the
// one of S13-01-process-hardening ("Limits and environment") with the
// host's TERM unchanged, and that wb exits with the shell's status. It
// returns the user's name.
func (r *vmRun) checkUserAndEnv(id string) string {
	t := r.t
	script := "id -un\n" +
		"test -x " + installedGuestd + " && echo guestd-executable\n" +
		"echo \"term=$TERM\"\n" +
		"echo --env--; env; echo --end--\n" +
		pathScript +
		"exit 7\n"
	res := r.runShell(id, shellIO{stdin: strings.NewReader(script), lookup: hostVars})
	if res.code != 7 {
		t.Errorf("wb shell: exit %d, want 7", res.code)
	}
	user, _, _ := strings.Cut(res.stdout, "\n")
	if !strings.HasPrefix(user, "wbp") {
		t.Errorf("id -un = %q, want a project user", user)
	}
	for _, want := range []string{"guestd-executable", "term=" + hostTerm + "\n"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q misses %q", res.stdout, want)
		}
	}
	if !strings.Contains(res.stderr, "wb: debug shell ") {
		t.Errorf("stderr %q misses the label line", res.stderr)
	}
	checkEnv(t, res.stdout, user)
	checkPath(t, res.stdout)
	return user
}

// hostVars are the host's terminal variables of the shells.
func hostVars(name string) (string, bool) {
	switch name {
	case "TERM":
		return hostTerm, true
	case "LANG":
		return hostLang, true
	}
	return "", false
}

// shellOwnVars are the variables zsh sets itself, which the guest's
// environment may show besides the ones wb-guestd sets.
var shellOwnVars = []string{"OLDPWD", "PWD", "SHLVL", "_"}

// checkEnv checks the `env` output between the markers: the host's
// terminal variables unchanged, the fixed ones of
// internal/guestd/shell, TMPDIR from the helper, and nothing else but
// zsh's own. PATH is checked by checkPath, because the login shell's
// /etc/zprofile rebuilds it with path_helper.
func checkEnv(t *testing.T, stdout, user string) {
	t.Helper()
	_, rest, _ := strings.Cut(stdout, "--env--\n")
	block, _, _ := strings.Cut(rest, "--end--")
	got := map[string]string{}
	for line := range strings.Lines(block) {
		name, value, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		got[name] = value
	}
	want := map[string]string{
		"TERM": hostTerm, "LANG": hostLang, "USER": user, "LOGNAME": user,
		"SHELL": "/bin/zsh",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("env %s = %q, want %q", name, got[name], value)
		}
	}
	for _, name := range []string{"HOME", "TMPDIR", "PATH"} {
		if got[name] == "" {
			t.Errorf("env has no %s", name)
		}
	}
	for name := range got {
		_, wanted := want[name]
		if !wanted && name != "HOME" && name != "TMPDIR" && name != "PATH" && !slices.Contains(shellOwnVars, name) {
			t.Errorf("env has %s, which the shell must not get", name)
		}
	}
}

// pathScript prints one line for each entry of the shell's PATH: the
// entry, the directory checked, its owner, its permission bits in
// octal, and whether the project user can write it. The directory
// checked is the entry, or, for an entry that doesn't exist, its
// nearest ancestor that does, where a project user could create it.
// stat -L follows a link to the directory the shell would search.
const pathScript = "echo --path--\n" +
	"for d in $path; do\n" +
	"  a=$d; while [ ! -e \"$a\" ]; do a=${a:h}; done\n" +
	"  w=no; [ -w \"$a\" ] && w=yes\n" +
	"  echo \"entry $d $a $(stat -L -f '%Su %Lp' \"$a\") $w\"\n" +
	"done\n" +
	"echo --end-path--\n"

// basePath are the entries wb-guestd's PATH has, which the login
// shell's PATH must keep.
var basePath = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// checkPath checks the pathScript output: the login shell's PATH comes
// from the guest's /etc/zprofile (path_helper), so it isn't wb-guestd's
// value. It must keep basePath. Each entry that exists, and the nearest
// existing ancestor of each entry that doesn't, must be owned by root,
// with no group or other write bit, and not writable by the project
// user. So no project can put a program on another project's PATH
// (SEC08-proj-isolation).
func checkPath(t *testing.T, stdout string) {
	t.Helper()
	_, rest, _ := strings.Cut(stdout, "--path--\n")
	block, _, _ := strings.Cut(rest, "--end-path--")
	var entries []string
	for line := range strings.Lines(block) {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSuffix(line, "\n"), "entry "))
		if len(fields) == 0 {
			continue
		}
		entries = append(entries, fields[0])
		checkPathEntry(t, fields)
	}
	t.Logf("path: %s", strings.Join(entries, ":"))
	for _, want := range basePath {
		if !slices.Contains(entries, want) {
			t.Errorf("PATH %q misses %s", strings.Join(entries, ":"), want)
		}
	}
}

// checkPathEntry checks one pathScript line, split into fields: the
// entry, the directory checked, its owner, its octal mode, and "yes" or
// "no" for the project user's write access.
func checkPathEntry(t *testing.T, fields []string) {
	t.Helper()
	if len(fields) != 5 {
		t.Errorf("path entry line %q, want entry, directory checked, owner, mode and writable", strings.Join(fields, " "))
		return
	}
	entry, checked, owner, writable := fields[0], fields[1], fields[2], fields[4]
	if checked != entry {
		t.Logf("path: %s doesn't exist, checked its ancestor %s", entry, checked)
	}
	mode, err := strconv.ParseUint(fields[3], 8, 32)
	if err != nil {
		t.Errorf("path entry %s: %s mode %q: %v", entry, checked, fields[3], err)
		return
	}
	if owner != "root" || mode&0o022 != 0 || writable != "no" {
		t.Errorf("path entry %s: %s has owner %s, mode %o, writable by the project user %s; want root, no group or other write, and not writable (SEC08-proj-isolation)",
			entry, checked, owner, mode, writable)
	}
}

// checkExits sees `exit 3` give exit 3, `kill -9 $$` give 137, and 8
// MiB piped to a program that never reads it give its status in time.
func (r *vmRun) checkExits(id string) {
	t := r.t
	if res := r.runShell(id, shellIO{stdin: strings.NewReader("exit 3\n")}); res.code != 3 {
		t.Errorf("exit 3: wb exit %d, want 3\n%s", res.code, res.stderr)
	}
	if res := r.runShell(id, shellIO{stdin: strings.NewReader("kill -9 $$\n")}); res.code != 137 {
		t.Errorf("kill -9 $$: wb exit %d, want 137\n%s", res.code, res.stderr)
	}
	piped := io.MultiReader(strings.NewReader("exec /bin/sh -c 'sleep 2; exit 3'\n"),
		bytes.NewReader(bytes.Repeat([]byte("#\n"), pipedInput/2)))
	start := time.Now()
	res := r.runShell(id, shellIO{stdin: piped})
	took := time.Since(start)
	t.Logf("measure: wb shell with %d bytes piped to a program that doesn't read them: exit %d after %v\n%s", pipedInput, res.code, took, res.stderr)
	if res.code != 3 {
		t.Errorf("wb shell piped: exit %d, want 3", res.code)
	}
	if took > time.Minute {
		t.Errorf("wb shell piped took %v, want under a minute", took)
	}
}

// checkIsolation reads the home of other's project user from its own
// shell, and sees the user of id refused there with EACCES, and refused
// by sudo (SEC08-proj-isolation, SEC11-root-gains-nothing).
func (r *vmRun) checkIsolation(id, other, user string) {
	t := r.t
	res := r.runShell(other, shellIO{stdin: strings.NewReader("echo \"home=$HOME\"\nid -un\n")})
	_, home, _ := strings.Cut(res.stdout, "home=")
	home, otherUser, _ := strings.Cut(home, "\n")
	otherUser = strings.TrimSpace(otherUser)
	if res.code != 0 || home == "" || otherUser == user {
		t.Fatalf("the other project's shell: exit %d, home %q, user %q\n%s", res.code, home, otherUser, res.stderr)
	}
	script := "ls '" + home + "' 2>&1; echo \"ls=$?\"\n" +
		"sudo -n true; echo \"sudo=$?\"\n"
	res = r.runShell(id, shellIO{stdin: strings.NewReader(script)})
	t.Logf("isolation: exit %d\nstdout:\n%s", res.code, res.stdout)
	if !strings.Contains(res.stdout, "Permission denied") || strings.Contains(res.stdout, "ls=0\n") {
		t.Errorf("ls of %s as %s: %q, want EACCES", home, user, res.stdout)
	}
	if !strings.Contains(res.stdout, "sudo=") || strings.Contains(res.stdout, "sudo=0\n") {
		t.Errorf("sudo -n true as %s: %q, want a failure", user, res.stdout)
	}
}

// checkPipes sees `echo 'echo hi; exit 4' | wb shell` print hi and exit
// 4, and a shell running `yes` into a reader that closes after one line,
// as `| head -1` does, end promptly.
func (r *vmRun) checkPipes(id string) {
	t := r.t
	res := r.runShell(id, shellIO{stdin: strings.NewReader("echo hi; exit 4\n")})
	if res.code != 4 || res.stdout != "hi\n" {
		t.Errorf("echo 'echo hi; exit 4' | wb shell: exit %d, stdout %q", res.code, res.stdout)
	}
	head := &headWriter{}
	start := time.Now()
	res = r.runShell(id, shellIO{stdin: strings.NewReader("yes\nexit 5\n"), stdout: head})
	took := time.Since(start)
	t.Logf("measure: wb shell running yes into head -1: exit %d after %v\n%s", res.code, took, res.stderr)
	if res.code != 5 || head.String() != "y\n" {
		t.Errorf("yes | head -1: exit %d, stdout %q, want 5 and %q", res.code, head.String(), "y\n")
	}
	if took > 30*time.Second {
		t.Errorf("yes | head -1 took %v, want under 30s", took)
	}
}

// headWriter takes the first line and fails every write after it with
// EPIPE, as the pipe to `head -1` does once head exits.
type headWriter struct {
	mu   sync.Mutex
	b    bytes.Buffer
	done bool
}

func (w *headWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return 0, syscall.EPIPE
	}
	line, _, found := bytes.Cut(p, []byte("\n"))
	if !found {
		return w.b.Write(p)
	}
	w.b.Write(line)
	w.b.WriteByte('\n')
	w.done = true
	return len(line) + 1, syscall.EPIPE
}

func (w *headWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// checkStopRefused keeps a shell open and sees `wb vm stop work`
// refused, naming the shell, then ends the shell.
func (r *vmRun) checkStopRefused(id string) {
	t := r.t
	stdin, feed := io.Pipe()
	out := &markWriter{mark: "open\n", seen: make(chan struct{})}
	done := make(chan shellResult, 1)
	go func() { done <- r.runShell(id, shellIO{stdin: stdin, stdout: out}) }()
	if _, err := feed.Write([]byte("echo open\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.seen:
	case <-time.After(3 * time.Minute):
		t.Fatal("the shell didn't open")
	}
	code, msg := r.wb("stop", "work")
	if code == 0 || !strings.Contains(msg, "c09shell-") {
		t.Errorf("wb vm stop with a shell open: exit %d, want a refusal naming the shell\n%s", code, msg)
	}
	_ = feed.Close()
	if res := <-done; res.code != 0 {
		t.Errorf("the open shell: exit %d, want 0\n%s", res.code, res.stderr)
	}
}

// markWriter closes seen once its output holds mark.
type markWriter struct {
	mu   sync.Mutex
	b    bytes.Buffer
	mark string
	seen chan struct{}
	once sync.Once
}

func (w *markWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.b.Write(p)
	if strings.Contains(w.b.String(), w.mark) {
		w.once.Do(func() { close(w.seen) })
	}
	return n, err
}

// addProject inserts a project of the work VM named name into
// state.db, as wb project place does, and returns its id.
func (r *vmRun) addProject(name string) string {
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
			ID: id, Realm: "native", KeyPath: "/" + name + "/0", Name: name,
			Placement: state.PlacementWork, Created: time.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// shellIO is the standard streams and the host variables of one
// `wb shell`. A nil stdout is a buffer, which shellResult returns, and
// a nil lookup finds no variable.
type shellIO struct {
	stdin  io.Reader
	stdout io.Writer
	lookup func(string) (string, bool)
}

type shellResult struct {
	code           int
	stdout, stderr string
}

// runShell runs `wb shell --project id` against the child, with no
// terminal, and returns its exit status and output.
func (r *vmRun) runShell(id string, sio shellIO) shellResult {
	var stdout, stderr bytes.Buffer
	out := sio.stdout
	if out == nil {
		out = &stdout
	}
	lookup := sio.lookup
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	env := projectcmd.SystemEnv(slog.New(slog.DiscardHandler))
	env.Paths = r.paths
	env.Stdout, env.Stderr = out, &stderr
	env.Terminal = func() (relay.Config, func(), error) {
		return relay.Config{
			Charset: tty.Charset{Source: "LANG", Value: hostLang, UTF8: true},
			Stdin:   sio.stdin, Stdout: out, Stderr: &stderr,
		}, func() {}, nil
	}
	env.Lookup = lookup
	ctx, cancel := context.WithTimeout(r.t.Context(), 3*time.Minute)
	defer cancel()
	code := projectcmd.RunShell(ctx, env, r.scratch, []string{"--project", id})
	res := shellResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	r.t.Logf("wb shell --project %s: exit %d\nstdout:\n%s\nstderr:\n%s", id, code, res.stdout, res.stderr)
	return res
}
