package credcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
)

// helperName is the program wb cred runs.
const helperName = "wb-credset"

// noBundleFix is the fix of RuleNoBundle.
const noBundleFix = `Install Wraith Box with "mise run app:install" or put "` + appbundle.AppName + `" in ~/Applications, run "wb setup" from it, and use the wb inside it.`

// helperPath returns the absolute path of wb-credset in the bundle wb
// runs from: wb's real path is <bundle>/Contents/MacOS/wb, as wb setup
// finds its bundle, and the helper is a regular file at its place in the
// layout (internal/appbundle). wb runs it by that path only, never from
// PATH.
func helperPath(env Env) (string, error) {
	if env.Unavailable != nil {
		return "", fmt.Errorf("wb can't find its own path: %w", env.Unavailable)
	}
	exe, err := filepath.EvalSymlinks(env.Executable)
	if err != nil {
		return "", fmt.Errorf("wb's own path %q can't be resolved: %w", env.Executable, err)
	}
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	if filepath.Base(exe) != "wb" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" {
		return "", fmt.Errorf("wb runs from %q, which is not Contents/MacOS/wb of an installed bundle, so it has no %s to start", exe, helperName)
	}
	i := slices.IndexFunc(appbundle.Programs, func(p appbundle.Program) bool { return p.Name == helperName })
	helper := filepath.Join(filepath.Dir(contents), filepath.FromSlash(appbundle.Programs[i].Path()))
	if fi, err := os.Lstat(helper); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("the bundle %q has no program %s", filepath.Dir(contents), appbundle.Programs[i].Path())
	}
	return helper, nil
}

// readValue reads the value of `wb cred set`: from a prompt with echo off
// when standard input is a terminal, and otherwise from standard input
// to its end, with one trailing "\n" or "\r\n" removed. It returns the
// buffer the value is in, which the caller zeroes, the value, and the
// reason of a refusal: a value that is empty, longer than
// credset.MaxValue, or can't be read.
func readValue(env Env, binding string) (buf, value []byte, reason string) {
	if env.StdinTerminal {
		fmt.Fprintf(env.Stderr, "Secret for binding %s (not shown): ", binding)
		line, err := env.ReadHidden()
		fmt.Fprintln(env.Stderr)
		if err != nil {
			return line, nil, "reading the secret from the terminal failed: " + err.Error()
		}
		return line, line, checkValue(line)
	}
	// Room for the value, a "\r\n", and one byte more, which shows that
	// the value is too long.
	buf = make([]byte, credset.MaxValue+3)
	n, err := io.ReadFull(env.Stdin, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return buf, nil, "reading the secret from standard input failed: " + err.Error()
	}
	value = trimNewline(buf[:n])
	return buf, value, checkValue(value)
}

// trimNewline removes one trailing "\r\n" or "\n", and keeps a second.
func trimNewline(v []byte) []byte {
	if l := len(v); l >= 2 && v[l-2] == '\r' && v[l-1] == '\n' {
		return v[:l-2]
	}
	if l := len(v); l >= 1 && v[l-1] == '\n' {
		return v[:l-1]
	}
	return v
}

// checkValue returns the reason to refuse value, or "".
func checkValue(value []byte) string {
	switch {
	case len(value) == 0:
		return "the secret is empty"
	case len(value) > credset.MaxValue:
		return fmt.Sprintf("the secret is longer than %d bytes", credset.MaxValue)
	}
	return ""
}

// The bounds of what wb keeps of wb-credset's output: a list of names,
// and its log, which has a few lines.
const (
	maxStdout = 1 << 20
	maxStderr = 64 << 10
)

// capped is a writer that keeps the first max bytes written to it, and
// drops the rest.
type capped struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if len(p) > room {
		c.cut = true
		c.buf.Write(p[:max(room, 0)])
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// result is how a run of wb-credset ended.
type result struct {
	// startErr is the error of starting it.
	startErr error
	// exit is its exit status, -1 when it ended on a signal.
	exit int
	// signal is the signal it ended on.
	signal   syscall.Signal
	signaled bool
	stdout   []byte
	stderr   []byte
	// decision is its decision line, when found.
	decision    credset.Decision
	hasDecision bool
}

// rule returns the rule the run ended with, for the record.
func (r result) rule() string {
	switch {
	case r.startErr != nil:
		return RuleHelperFailed
	case r.signaled:
		return RuleHelperSignaled
	case r.hasDecision:
		return r.decision.Rule
	}
	return RuleHelperFailed
}

// runHelper starts wb-credset by its absolute path with the two
// arguments, the environment of credset.Environ, and pipes for its
// streams, and waits for it to exit. For set it writes value to the
// helper's standard input, closes it, and zeroes value
// (S15-03-access-rules, rule 5). The value is in no argument, no
// variable and no log line (SEC04-no-guest-secrets).
func runHelper(ctx context.Context, env Env, helper string, c command, value []byte) result {
	arg := c.binding
	if c.op == credset.OpList {
		arg = credset.ListArg
	}
	cmd := exec.CommandContext(ctx, helper, c.op, arg) //nolint:gosec // G204: helper is the absolute path in wb's own bundle from helperPath, the operation is a constant, the binding passed the name form, and no shell parses the arguments
	cmd.Env = credset.Environ(env.LookupEnv)
	stdout, stderr := &capped{max: maxStdout}, &capped{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	var in io.WriteCloser
	if c.op == credset.OpWrite {
		var err error
		if in, err = cmd.StdinPipe(); err != nil {
			return result{startErr: err}
		}
	}
	if err := cmd.Start(); err != nil {
		return result{startErr: err}
	}
	if in != nil {
		writeValue(ctx, env, in, value)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return result{startErr: err}
	}
	r := result{exit: cmd.ProcessState.ExitCode(), stdout: stdout.buf.Bytes(), stderr: stderr.buf.Bytes()}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		r.signaled, r.signal = true, ws.Signal()
	}
	r.decision, r.hasDecision = credset.ParseDecision(r.stderr)
	if stdout.cut || stderr.cut {
		env.Log.LogAttrs(ctx, slog.LevelWarn, "wb cred: wb-credset's output was cut", slog.Bool("stdout_cut", stdout.cut), slog.Bool("stderr_cut", stderr.cut))
	}
	return r
}

// writeValue writes value to the helper's standard input, closes it,
// and zeroes value when the write ends. A failed write, as when the
// helper exited first, is logged without the value, and the helper's
// exit decides the outcome.
func writeValue(ctx context.Context, env Env, in io.WriteCloser, value []byte) {
	_, err := in.Write(value)
	clear(value)
	if cerr := in.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		env.Log.LogAttrs(ctx, slog.LevelWarn, "wb cred: writing the secret to wb-credset failed", slog.String("error", err.Error()))
	}
}
