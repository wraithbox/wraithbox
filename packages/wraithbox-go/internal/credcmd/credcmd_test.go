package credcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// A test runs the test binary again as wb-credset: each test puts a copy
// at the helper's place in a bundle in its own directory, and TestMain
// acts as the helper when the program's name is wb-credset. wb starts
// the helper with an empty environment, so the fake helper reads what to
// do from controlFile next to itself, and writes what it got to
// reportFile there.
const (
	controlFile = "fake-control.json"
	reportFile  = "fake-report.json"
)

const (
	team = "ABCDE12345"
	// secret is the value the set tests store. Nothing wb or the helper
	// prints or logs may hold it.
	secret = "secret-VALUE-9f2c"
)

// control is what the fake helper does.
type control struct {
	// Mode is "script", which prints Stdout and Stderr and exits with
	// Exit; "kill", which ends on SIGKILL; or "real", which runs
	// credset.Run over a fake store that holds the bindings in Seed.
	Mode   string
	Stdout string
	Stderr string
	Exit   int
	Seed   []string
}

// fakeReport is what the fake helper got.
type fakeReport struct {
	Args    []string
	Environ []string
	Stdin   []byte
}

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == helperName {
		os.Exit(fakeHelper())
	}
	os.Exit(m.Run())
}

// fakeHelper is the helper's side of a test.
func fakeHelper() int {
	dir := filepath.Dir(os.Args[0])
	raw, err := os.ReadFile(filepath.Join(dir, controlFile))
	if err != nil {
		return 99
	}
	var c control
	if json.Unmarshal(raw, &c) != nil {
		return 99
	}
	stdin, _ := io.ReadAll(os.Stdin)
	rep, _ := json.Marshal(fakeReport{Args: os.Args[1:], Environ: os.Environ(), Stdin: stdin})
	if os.WriteFile(filepath.Join(dir, reportFile), rep, 0o600) != nil {
		return 99
	}
	switch c.Mode {
	case "kill":
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Kill()
		time.Sleep(time.Minute)
		return 98
	case "real":
		return realHelper(c, stdin)
	}
	os.Stdout.WriteString(c.Stdout)
	os.Stderr.WriteString(c.Stderr)
	return c.Exit
}

// realHelper runs credset.Run as wb-credset does, over a fake store.
func realHelper(c control, stdin []byte) int {
	ctx := context.Background()
	group := storeitem.Group(team, storeitem.RoleBinding)
	fake := storetest.New(team, group)
	for _, b := range c.Seed {
		n, _ := credset.BindingItem(b)
		fake.Write(ctx, group, n.String(), []byte("seeded"))
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	store, err := secretstore.New(storeitem.ProgramCredset, fake, log)
	if err != nil {
		return 97
	}
	return credset.Run(ctx, credset.Process{Args: os.Args[1:], Environ: os.Environ(), Stdin: bytes.NewReader(stdin), Stdout: os.Stdout, Stderr: os.Stderr}, store)
}

// bundle is a test's Wraith Box.app with the fake helper in it.
type bundle struct {
	wb, macos string
}

// newBundle makes a bundle in a test directory, with a regular file as
// wb and a copy of the test binary as wb-credset, which does c.
func newBundle(t *testing.T, c control) bundle {
	t.Helper()
	root := filepath.Join(t.TempDir(), appbundle.AppName)
	wb := filepath.Join(root, "Contents", "MacOS", "wb")
	i := slices.IndexFunc(appbundle.Programs, func(p appbundle.Program) bool { return p.Name == helperName })
	helper := filepath.Join(root, filepath.FromSlash(appbundle.Programs[i].Path()))
	for _, d := range []string{filepath.Dir(wb), filepath.Dir(helper)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(wb, []byte("wb"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, helper)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(helper), controlFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return bundle{wb: wb, macos: filepath.Dir(helper)}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if os.Link(from, to) == nil {
		return
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o755); err != nil {
		t.Fatal(err)
	}
}

// report returns what the fake helper got, and false when it didn't
// run.
func (b bundle) report(t *testing.T) (fakeReport, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.macos, reportFile))
	if errors.Is(err, os.ErrNotExist) {
		return fakeReport{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var r fakeReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r, true
}

// out is what one `wb cred` run printed and logged.
type out struct {
	code                int
	stdout, stderr, log string
}

// lastLine is the last line on standard error.
func (o out) lastLine() string {
	lines := strings.Split(strings.TrimRight(o.stderr, "\n"), "\n")
	return lines[len(lines)-1]
}

// noSecret fails the test when the value is in what wb printed or
// logged.
func (o out) noSecret(t *testing.T) {
	t.Helper()
	if strings.Contains(o.stdout+o.stderr+o.log, secret) {
		t.Errorf("the value is in wb's output:\nstdout: %s\nstderr: %s\nlog: %s", o.stdout, o.stderr, o.log)
	}
}

// harness is the Env of one test, and what wb printed and logged.
type harness struct {
	env                 Env
	stdout, stderr, log bytes.Buffer
}

// testEnv is an Env for a macOS host, with wb in b and stdin as its
// standard input, which isn't a terminal.
func testEnv(t *testing.T, b bundle, stdin io.Reader) *harness {
	t.Helper()
	h := &harness{}
	h.env = Env{
		Host:       platform.Host{OS: platform.MacOS},
		Executable: b.wb,
		Stdin:      stdin,
		ReadHidden: func() ([]byte, error) {
			t.Error("wb read from the terminal")
			return nil, errors.New("no terminal")
		},
		Stdout:    &h.stdout,
		Stderr:    &h.stderr,
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadTeam:  func(context.Context, string) (string, error) { return team, nil },
		Log:       slog.New(slog.NewJSONHandler(&h.log, nil)),
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) out {
	t.Helper()
	code := Run(t.Context(), h.env, args)
	return out{code: code, stdout: h.stdout.String(), stderr: h.stderr.String(), log: h.log.String()}
}

// accept is the decision line of a run that wb-credset accepted.
func accept(op, binding, rule string) string {
	d, _ := json.Marshal(map[string]any{
		"msg": credset.DecisionMsg, "decision": credset.DecisionAccept,
		"rule": rule, "op": op, "binding": binding,
	})
	return string(d) + "\n"
}

// refuse is the decision line of a run that wb-credset refused.
func refuse(op, binding, rule, reason string) string {
	d, _ := json.Marshal(map[string]any{
		"msg": credset.DecisionMsg, "decision": credset.DecisionRefuse,
		"rule": rule, "op": op, "binding": binding, "reason": reason,
	})
	return string(d) + "\n"
}

// errReader fails the test when it is read.
type errReader struct{ t *testing.T }

func (r errReader) Read([]byte) (int, error) {
	r.t.Error("wb read its standard input")
	return 0, io.ErrUnexpectedEOF
}

// TestC05CredSetFromPipe sets a binding from a pipe through the real
// wb-credset logic: the value reaches the helper on its standard input
// only, without the trailing newline, with two arguments and an empty
// environment, and nothing wb or the helper prints or logs holds it
// (C05-credentials-and-store, "wb cred"; SEC04-no-guest-secrets).
func TestC05CredSetFromPipe(t *testing.T) {
	b := newBundle(t, control{Mode: "real"})
	h := testEnv(t, b, strings.NewReader(secret+"\n"))
	h.env.LookupEnv = func(string) (string, bool) { return "set", true }
	o := h.run(t, "set", "github")
	if o.code != exitOK || o.stdout != "Set the binding github.\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", o.code, o.stdout, o.stderr)
	}
	o.noSecret(t)
	r, ok := b.report(t)
	if !ok {
		t.Fatal("the helper didn't run")
	}
	if string(r.Stdin) != secret {
		t.Errorf("the helper got %q on standard input, want the value without its newline", r.Stdin)
	}
	if !slices.Equal(r.Args, []string{credset.OpWrite, "github"}) {
		t.Errorf("the helper got the arguments %q", r.Args)
	}
	if len(r.Environ) != 0 {
		t.Errorf("the helper got the environment %q, want none", r.Environ)
	}
	if strings.Count(o.log, "wb cred access") != 2 {
		t.Errorf("the log has no start and end record of the access:\n%s", o.log)
	}
}

// TestC05CredSetFromPrompt reads the value from the terminal, and the
// prompt doesn't echo it.
func TestC05CredSetFromPrompt(t *testing.T) {
	b := newBundle(t, control{Mode: "real"})
	h := testEnv(t, b, errReader{t})
	h.env.StdinTerminal = true
	h.env.ReadHidden = func() ([]byte, error) { return []byte(secret), nil }
	o := h.run(t, "set", "github")
	if o.code != exitOK {
		t.Fatalf("exit %d, stderr %q", o.code, o.stderr)
	}
	if !strings.Contains(o.stderr, "Secret for binding github (not shown): ") {
		t.Errorf("no prompt on standard error: %q", o.stderr)
	}
	o.noSecret(t)
	if r, _ := b.report(t); string(r.Stdin) != secret {
		t.Errorf("the helper got %q on standard input", r.Stdin)
	}
}

// TestC05CredSetNewline removes one trailing newline, and keeps a
// second.
func TestC05CredSetNewline(t *testing.T) {
	for in, want := range map[string]string{
		"v":       "v",
		"v\n":     "v",
		"v\r\n":   "v",
		"v\n\n":   "v\n",
		"\nv\n":   "\nv",
		"v\r":     "v\r",
		"v\r\n\n": "v\r\n",
	} {
		b := newBundle(t, control{Mode: "script", Stderr: accept(credset.OpWrite, "x", credset.RuleDone)})
		h := testEnv(t, b, strings.NewReader(in))
		if o := h.run(t, "set", "x"); o.code != exitOK {
			t.Errorf("%q: exit %d, stderr %q", in, o.code, o.stderr)
		}
		if r, _ := b.report(t); string(r.Stdin) != want {
			t.Errorf("%q: the helper got %q, want %q", in, r.Stdin, want)
		}
	}
}

// TestC05CredSetValueRefused refuses an empty value and one over the
// cap before the helper runs.
func TestC05CredSetValueRefused(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"only newline": "\n",
		"over the cap": strings.Repeat("a", credset.MaxValue+1),
		"far over":     strings.Repeat("a", 3*credset.MaxValue),
	} {
		b := newBundle(t, control{Mode: "real"})
		h := testEnv(t, b, strings.NewReader(in))
		o := h.run(t, "set", "x")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleValueRefused+")") {
			t.Errorf("%s: exit %d, last line %q", name, o.code, o.lastLine())
		}
		if _, ran := b.report(t); ran {
			t.Errorf("%s: the helper ran", name)
		}
	}
	// A value of exactly the cap is accepted.
	b := newBundle(t, control{Mode: "real"})
	h := testEnv(t, b, strings.NewReader(strings.Repeat("a", credset.MaxValue)+"\n"))
	if o := h.run(t, "set", "x"); o.code != exitOK {
		t.Errorf("a value of %d bytes: exit %d, stderr %q", credset.MaxValue, o.code, o.stderr)
	}
}

// TestC05CredNameInvalid refuses a bad binding name with exit 2 before
// it reads anything or starts the helper.
func TestC05CredNameInvalid(t *testing.T) {
	for _, args := range [][]string{
		{"set", "Bad_Name"},
		{"set", ""},
		{"set", strings.Repeat("a", 65)},
		{"set", "../x"},
		{"rm", "a/b"},
		{"rm", "x\x1b[2J"},
	} {
		b := newBundle(t, control{Mode: "real"})
		h := testEnv(t, b, errReader{t})
		h.env.StdinTerminal = args[0] == "set"
		h.env.ReadHidden = func() ([]byte, error) {
			t.Errorf("%q: wb prompted for a value", args)
			return nil, errors.New("no")
		}
		o := h.run(t, args...)
		if o.code != exitUsage || !strings.HasSuffix(o.lastLine(), "("+RuleNameInvalid+")") {
			t.Errorf("%q: exit %d, last line %q", args, o.code, o.lastLine())
		}
		if strings.Contains(o.stderr, "\x1b") {
			t.Errorf("%q: wb printed an escape: %q", args, o.stderr)
		}
		if _, ran := b.report(t); ran {
			t.Errorf("%q: the helper ran", args)
		}
	}
}

// TestC05CredHelperRule prints the helper's rule as the rule of the
// failure, with its fix before it.
func TestC05CredHelperRule(t *testing.T) {
	for _, tc := range []struct {
		rule string
		fix  string
	}{
		{string(storeitem.RuleLocked), lockedFix},
		{string(storeitem.RuleNeedsTeam), appbundle.TeamSetupSteps},
		{string(storeitem.RuleWriteFailed), setupFix},
		{credset.RuleRefused, refusedFix},
	} {
		b := newBundle(t, control{Mode: "script", Exit: 1, Stderr: "{\"msg\":\"other\"}\nnot json\n" + refuse(credset.OpWrite, "x", tc.rule, "the store said no\x1b[31m")})
		h := testEnv(t, b, strings.NewReader(secret))
		o := h.run(t, "set", "x")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+tc.rule+")") || !strings.HasPrefix(o.lastLine(), "wraith box: ") {
			t.Errorf("%s: exit %d, last line %q", tc.rule, o.code, o.lastLine())
		}
		if !strings.Contains(o.stderr, strings.TrimRight(tc.fix, "\n")) {
			t.Errorf("%s: no fix in %q", tc.rule, o.stderr)
		}
		if strings.Contains(o.stderr, "\x1b") {
			t.Errorf("%s: wb printed the helper's escape: %q", tc.rule, o.stderr)
		}
		o.noSecret(t)
	}
}

// TestC05CredHelperNoDecision fails a helper that exits without a
// decision line, even with exit 0.
func TestC05CredHelperNoDecision(t *testing.T) {
	for _, exit := range []int{0, 1, 3} {
		b := newBundle(t, control{Mode: "script", Exit: exit, Stderr: "crashed\n"})
		h := testEnv(t, b, nil)
		o := h.run(t, "rm", "x")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleHelperFailed+")") {
			t.Errorf("exit %d: wb exited %d, last line %q", exit, o.code, o.lastLine())
		}
	}
}

// TestC05CredHelperKilled reports a helper that macOS ends with SIGKILL,
// as it does on a signature or profile it refuses.
func TestC05CredHelperKilled(t *testing.T) {
	b := newBundle(t, control{Mode: "kill"})
	h := testEnv(t, b, strings.NewReader(secret))
	o := h.run(t, "set", "x")
	if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleHelperSignaled+")") {
		t.Fatalf("exit %d, last line %q", o.code, o.lastLine())
	}
	if !strings.Contains(o.lastLine(), "SIGKILL") || !strings.Contains(o.stderr, signaledFix) {
		t.Errorf("no SIGKILL and its fix in %q", o.stderr)
	}
	o.noSecret(t)
}

// TestC05CredList lists the binding names on standard output, and the
// signing team on standard error.
func TestC05CredList(t *testing.T) {
	b := newBundle(t, control{Mode: "real", Seed: []string{"zeta", "github", "a-1"}})
	h := testEnv(t, b, errReader{t})
	o := h.run(t, "list")
	if o.code != exitOK || o.stdout != "a-1\ngithub\nzeta\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", o.code, o.stdout, o.stderr)
	}
	if !strings.Contains(o.stderr, "signed by the team "+team) || !strings.Contains(o.stderr, team+".nl.wraithbox.*") {
		t.Errorf("no team note in %q", o.stderr)
	}
	if r, _ := b.report(t); !slices.Equal(r.Args, []string{credset.OpList, credset.ListArg}) {
		t.Errorf("the helper got the arguments %q", r.Args)
	}
}

// TestC05CredListBadName fails a list with a line that isn't a binding
// name, and prints none of the names.
func TestC05CredListBadName(t *testing.T) {
	b := newBundle(t, control{Mode: "script", Stdout: "good\n\x1b[2J-bad\n", Stderr: accept(credset.OpList, "", credset.RuleDone)})
	h := testEnv(t, b, nil)
	o := h.run(t, "list")
	if o.code != exitFailure || o.stdout != "" || !strings.HasSuffix(o.lastLine(), "("+RuleHelperFailed+")") {
		t.Errorf("exit %d, stdout %q, last line %q", o.code, o.stdout, o.lastLine())
	}
	if strings.Contains(o.stderr, "\x1b") {
		t.Errorf("wb printed the helper's escape: %q", o.stderr)
	}
}

// TestC05CredRm removes a binding, and a binding that isn't there.
func TestC05CredRm(t *testing.T) {
	for _, tc := range []struct {
		seed []string
		note bool
	}{{[]string{"x"}, false}, {nil, true}} {
		b := newBundle(t, control{Mode: "real", Seed: tc.seed})
		h := testEnv(t, b, errReader{t})
		o := h.run(t, "rm", "x")
		if o.code != exitOK || o.stdout != "Removed the binding x.\n" {
			t.Errorf("seed %q: exit %d, stdout %q, stderr %q", tc.seed, o.code, o.stdout, o.stderr)
		}
		if got := strings.Contains(o.stderr, "wasn't in the store"); got != tc.note {
			t.Errorf("seed %q: note %v, stderr %q", tc.seed, got, o.stderr)
		}
	}
}

// TestC05CredHostLater refuses on a host that is later in the platform
// matrix, before it starts anything.
func TestC05CredHostLater(t *testing.T) {
	for _, host := range []platform.Host{{OS: platform.Linux}, {OS: platform.Windows}, {OS: platform.MacOS, WSL: true}} {
		b := newBundle(t, control{Mode: "real"})
		h := testEnv(t, b, errReader{t})
		h.env.Host = host
		o := h.run(t, "set", "x")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleHostLater+")") {
			t.Errorf("%+v: exit %d, last line %q", host, o.code, o.lastLine())
		}
		if _, ran := b.report(t); ran {
			t.Errorf("%+v: the helper ran", host)
		}
	}
}

// TestC05CredNoBundle refuses when wb doesn't run from a bundle, or the
// bundle has no wb-credset, and never looks for one on PATH.
func TestC05CredNoBundle(t *testing.T) {
	loose := filepath.Join(t.TempDir(), "wb")
	if err := os.WriteFile(loose, []byte("wb"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := newBundle(t, control{Mode: "real"})
	helper := filepath.Join(empty.macos, helperName)
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	asDir := newBundle(t, control{Mode: "real"})
	helper = filepath.Join(asDir.macos, helperName)
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, exe := range map[string]string{"loose wb": loose, "no helper": empty.wb, "helper is a directory": asDir.wb} {
		h := testEnv(t, bundle{wb: exe}, errReader{t})
		h.env.LookupEnv = func(string) (string, bool) { return filepath.Dir(helper), true }
		o := h.run(t, "list")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleNoBundle+")") || !strings.Contains(o.stderr, noBundleFix) {
			t.Errorf("%s: exit %d, stderr %q", name, o.code, o.stderr)
		}
	}
}

// TestCredUsage refuses a wrong command with exit 2 and the usage.
func TestCredUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"get", "x"}, {"set"}, {"set", "a", "b"}, {"rm"}, {"list", "x"}} {
		h := testEnv(t, bundle{}, errReader{t})
		o := h.run(t, args...)
		if o.code != exitUsage || !strings.Contains(o.stderr, "Usage: wb cred set <binding>") {
			t.Errorf("%q: exit %d, stderr %q", args, o.code, o.stderr)
		}
	}
	h := testEnv(t, bundle{}, errReader{t})
	if o := h.run(t, "help"); o.code != exitOK || !strings.HasPrefix(o.stdout, "Usage:") {
		t.Errorf("help: exit %d, stdout %q", o.code, o.stdout)
	}
}
