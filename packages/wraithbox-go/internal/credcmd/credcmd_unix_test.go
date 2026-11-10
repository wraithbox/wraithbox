//go:build unix

package credcmd

// The tests that start the fake helper. wb cred refuses on Windows
// hosts before it starts anything (TestC05CredHostLater), and the test
// binary copied without .exe can't run there.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// secret is the value the set tests store. Nothing wb or the helper
// prints or logs may hold it.
const secret = "secret-VALUE-9f2c"

// noSecret fails the test when the value is in what wb printed or
// logged.
func (o out) noSecret(t *testing.T) {
	t.Helper()
	if strings.Contains(o.stdout+o.stderr+o.log, secret) {
		t.Errorf("the value is in wb's output:\nstdout: %s\nstderr: %s\nlog: %s", o.stdout, o.stderr, o.log)
	}
}

// accept is the decision line of a run that wb-credset accepted.
func accept(op, binding string) string {
	d, _ := json.Marshal(map[string]any{
		"msg": credset.DecisionMsg, "decision": credset.DecisionAccept,
		"rule": credset.RuleDone, "op": op, "binding": binding,
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
		b := newBundle(t, control{Mode: "script", Stderr: accept(credset.OpWrite, "x")})
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
	b := newBundle(t, control{Mode: "script", Stdout: "good\n\x1b[2J-bad\n", Stderr: accept(credset.OpList, "")})
	h := testEnv(t, b, nil)
	o := h.run(t, "list")
	if o.code != exitFailure || o.stdout != "" || !strings.HasSuffix(o.lastLine(), "("+RuleHelperFailed+")") {
		t.Errorf("exit %d, stdout %q, last line %q", o.code, o.stdout, o.lastLine())
	}
	if strings.Contains(o.stderr, "\x1b") {
		t.Errorf("wb printed the helper's escape: %q", o.stderr)
	}
}

// TestC05CredRm removes a binding. For a binding that isn't there it
// exits 0 with a note on standard error, and prints nothing on standard
// output.
func TestC05CredRm(t *testing.T) {
	for _, tc := range []struct {
		seed   []string
		stdout string
		note   bool
	}{{[]string{"x"}, "Removed the binding x.\n", false}, {nil, "", true}} {
		b := newBundle(t, control{Mode: "real", Seed: tc.seed})
		h := testEnv(t, b, errReader{t})
		o := h.run(t, "rm", "x")
		if o.code != exitOK || o.stdout != tc.stdout {
			t.Errorf("seed %q: exit %d, stdout %q, stderr %q", tc.seed, o.code, o.stdout, o.stderr)
		}
		if got := strings.Contains(o.stderr, "wasn't in the store"); got != tc.note {
			t.Errorf("seed %q: note %v, stderr %q", tc.seed, got, o.stderr)
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

// TestC05CredHelperOutputCut fails a run whose standard output or error
// was longer than wb keeps, even with a decision that accepts, and
// prints none of the names.
func TestC05CredHelperOutputCut(t *testing.T) {
	names := strings.Repeat("a\n", maxStdout/2+1)
	for name, c := range map[string]control{
		"stdout": {Mode: "script", Stdout: names, Stderr: accept(credset.OpList, "")},
		"stderr": {Mode: "script", Stdout: "a\n", Stderr: accept(credset.OpList, "") + strings.Repeat("x", maxStderr)},
	} {
		b := newBundle(t, c)
		h := testEnv(t, b, nil)
		o := h.run(t, "list")
		if o.code != exitFailure || o.stdout != "" || !strings.HasSuffix(o.lastLine(), "("+RuleHelperFailed+")") {
			t.Errorf("%s: exit %d, stdout of %d bytes, last line %q", name, o.code, len(o.stdout), o.lastLine())
		}
	}
}

// TestC05CredBundleCheck refuses a bundle whose build version isn't
// wb's, an unstamped wb, a bundle without a build version, and a helper
// whose path goes through a link, before it reads anything.
func TestC05CredBundleCheck(t *testing.T) {
	cases := map[string]func(t *testing.T, b bundle, h *harness){
		"other version": func(t *testing.T, b bundle, _ *harness) {
			writePlist(t, bundleRoot(b), "0.2.0")
		},
		"unstamped wb": func(_ *testing.T, _ bundle, h *harness) {
			h.env.Version = ""
		},
		"no plist": func(t *testing.T, b bundle, _ *harness) {
			if err := os.Remove(filepath.Join(bundleRoot(b), filepath.FromSlash(appbundle.InfoPlistPath))); err != nil {
				t.Fatal(err)
			}
		},
		"linked helpers": func(t *testing.T, b bundle, _ *harness) {
			helpers := filepath.Join(bundleRoot(b), "Contents", "Helpers")
			moved := filepath.Join(t.TempDir(), "Helpers")
			if err := os.Rename(helpers, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, helpers); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		b := newBundle(t, control{Mode: "real"})
		h := testEnv(t, b, errReader{t})
		change(t, b, h)
		o := h.run(t, "set", "x")
		if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleNoBundle+")") {
			t.Errorf("%s: exit %d, stderr %q", name, o.code, o.stderr)
		}
		if _, ran := b.report(t); ran {
			t.Errorf("%s: the helper ran", name)
		}
	}
}

// bundleRoot is the Wraith Box.app of b.
func bundleRoot(b bundle) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(b.wb)))
}

// TestC05CredNoCore turns core files off before set reads the value,
// and refuses before it reads or starts the helper when that fails.
func TestC05CredNoCore(t *testing.T) {
	b := newBundle(t, control{Mode: "real"})
	h := testEnv(t, b, strings.NewReader(secret))
	if o := h.run(t, "set", "x"); o.code != exitOK || h.noCore != 1 {
		t.Errorf("exit %d, NoCore called %d times", o.code, h.noCore)
	}
	b = newBundle(t, control{Mode: "real"})
	h = testEnv(t, b, errReader{t})
	h.env.NoCore = func() error { return errors.New("setrlimit: not allowed") }
	o := h.run(t, "set", "x")
	if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleNoCore+")") {
		t.Errorf("exit %d, last line %q", o.code, o.lastLine())
	}
	if _, ran := b.report(t); ran {
		t.Error("the helper ran")
	}
}

// TestC05CredHelperNoRead reports the helper's refusal when it exits
// without reading the value, and doesn't hang on the write.
func TestC05CredHelperNoRead(t *testing.T) {
	b := newBundle(t, control{Mode: "no-read", Exit: 1, Stderr: refuse(credset.OpWrite, "x", credset.RuleRefused, "no")})
	h := testEnv(t, b, strings.NewReader(strings.Repeat("v", credset.MaxValue)))
	o := h.run(t, "set", "x")
	if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+credset.RuleRefused+")") {
		t.Errorf("exit %d, last line %q", o.code, o.lastLine())
	}
}

// TestC05CredPromptLimit refuses a line at the prompt that the terminal
// may have cut, before the helper runs, and names the pipe instead.
func TestC05CredPromptLimit(t *testing.T) {
	b := newBundle(t, control{Mode: "real"})
	h := testEnv(t, b, errReader{t})
	h.env.StdinTerminal = true
	h.env.ReadHidden = func() ([]byte, error) { return []byte(strings.Repeat("v", maxPromptLine)), nil }
	o := h.run(t, "set", "x")
	if o.code != exitFailure || !strings.HasSuffix(o.lastLine(), "("+RuleValueRefused+")") || !strings.Contains(o.stderr, "wb cred set x < file") {
		t.Errorf("exit %d, stderr %q", o.code, o.stderr)
	}
	if _, ran := b.report(t); ran {
		t.Error("the helper ran")
	}
}
