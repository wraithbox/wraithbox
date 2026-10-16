package selfsandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// Confinement can't be undone and covers every thread, so each case runs
// in a child: the test binary again, with childCase naming the case.
// TestMain runs the case instead of the tests, and the child prints
// resultOK when every check passed.
const (
	childCase  = "WB_SELFSANDBOX_TEST_CASE"
	childHome  = "WB_SELFSANDBOX_TEST_HOME"
	childTemp  = "WB_SELFSANDBOX_TEST_TEMP"
	childAddr  = "WB_SELFSANDBOX_TEST_ADDR"
	childAllow = "WB_SELFSANDBOX_TEST_ALLOW"
	resultOK   = "RESULT ok"

	denyAll = "(version 1)\n(deny default)\n"
)

// childCases are the cases a child runs. Each returns the checks that
// failed.
var childCases = map[string]func() []string{
	"deny-default":   caseDenyDefault,
	"param":          caseParam,
	"allow-default":  caseAllowDefault,
	"syntax-error":   caseSyntaxError,
	"second-confine": caseSecondConfine,
}

func TestMain(m *testing.M) {
	if name := os.Getenv(childCase); name != "" {
		os.Exit(runCase(name))
	}
	os.Exit(m.Run())
}

func runCase(name string) int {
	run, ok := childCases[name]
	if refused, found := strings.CutPrefix(name, "refuse-"); found {
		run, ok = func() []string { return caseRefused(refused) }, true
	}
	if !ok {
		fmt.Println("unknown case", name)
		return 2
	}
	failed := run()
	for _, f := range failed {
		fmt.Println("FAIL", f)
	}
	if len(failed) > 0 {
		return 1
	}
	fmt.Println(resultOK)
	return 0
}

// runChild runs a case in a child and checks that every check passed.
// It returns the child's output.
func runChild(t *testing.T, name string, env []string, files ...*os.File) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), childCase+"="+name), env...)
	cmd.ExtraFiles = files
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte(resultOK)) {
		t.Fatalf("case %s: %v\n%s", name, err, out)
	}
	return string(out)
}

// denied checks that err is a Seatbelt denial.
func denied(what string, err error) []string {
	if errors.Is(err, syscall.EPERM) {
		return nil
	}
	return []string{fmt.Sprintf("%s: %v, want EPERM", what, err)}
}

// wantRule checks that err is a refusal with rule.
func wantRule(what string, err error, rule Rule) []string {
	var r *Refusal
	if errors.As(err, &r) && r.Rule == rule {
		return nil
	}
	return []string{fmt.Sprintf("%s: %v, want %s", what, err, rule)}
}

// unconfined checks that the child can still read the probe file.
func unconfined() []string {
	if _, err := os.ReadFile(probePath); err != nil {
		return []string{fmt.Sprintf("read %s: %v, want the child unconfined", probePath, err)}
	}
	return nil
}

func caseDenyDefault() []string {
	home, temp, addr := os.Getenv(childHome), os.Getenv(childTemp), os.Getenv(childAddr)
	pipe, sock := os.NewFile(3, "pipe"), os.NewFile(4, "socket")
	if err := Confine(Profile{Name: "deny-all", Text: denyAll}, nil); err != nil {
		return []string{fmt.Sprintf("Confine: %v", err)}
	}
	var failed []string
	_, err := os.Open(home)
	failed = append(failed, denied("open the home directory", err)...)
	_, err = os.Open(temp)
	failed = append(failed, denied("open a file in the temporary directory", err)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var d net.Dialer
	_, err = d.DialContext(ctx, "tcp", addr)
	failed = append(failed, denied("connect to the parent's loopback listener", err)...)
	if err := exec.CommandContext(ctx, "/bin/echo", "confined").Run(); err == nil {
		failed = append(failed, "running /bin/echo succeeded")
	}
	if _, err := pipe.Write([]byte("through the pipe")); err != nil {
		failed = append(failed, fmt.Sprintf("write to the inherited pipe: %v", err))
	}
	failed = append(failed, sendOnInherited(sock, pipe)...)
	failed = append(failed, channelInside(pipe)...)
	failed = append(failed, deniedOnEveryThread(temp)...)
	time.Sleep(2 * time.Second)
	if got, want := runtime.GOMAXPROCS(0), runtime.NumCPU(); got != want {
		failed = append(failed, fmt.Sprintf("GOMAXPROCS = %d after 2 s, want %d", got, want))
	}
	return failed
}

// sendOnInherited sends the pipe's descriptor on the inherited socket.
func sendOnInherited(sock, pipe *os.File) []string {
	ch, err := fdchan.New(sock, 4096)
	if err != nil {
		return []string{fmt.Sprintf("fdchan.New on the inherited socket: %v", err)}
	}
	defer ch.Close()
	if err := ch.Send([]byte("descriptor"), pipe); err != nil {
		return []string{fmt.Sprintf("send a descriptor on the inherited socket: %v", err)}
	}
	return nil
}

// channelInside makes a descriptor channel in the confined process, as
// wb-netd and wb-proxyd will use theirs: a descriptor round-trips, and a
// Recv that waits sees the peer close, which takes a getpeername.
func channelInside(pipe *os.File) []string {
	fa, fb, err := fdchan.Socketpair(4096)
	if err != nil {
		return []string{fmt.Sprintf("fdchan.Socketpair: %v", err)}
	}
	a, errA := fdchan.New(fa, 4096)
	b, errB := fdchan.New(fb, 4096)
	if err := errors.Join(errA, errB); err != nil {
		return []string{fmt.Sprintf("fdchan.New: %v", err)}
	}
	defer b.Close()
	if err := a.Send([]byte("inside"), pipe); err != nil {
		return []string{fmt.Sprintf("send inside: %v", err)}
	}
	m, err := b.Recv()
	if err != nil || string(m.Record) != "inside" || m.File == nil {
		return []string{fmt.Sprintf("receive inside: %v, %v", m, err)}
	}
	_ = m.Close()
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = a.Close()
	}()
	if _, err := b.Recv(); !errors.Is(err, io.EOF) {
		return []string{fmt.Sprintf("receive after the peer closed: %v, want io.EOF", err)}
	}
	return nil
}

// deniedOnEveryThread opens path from 32 goroutines, each locked to its
// own OS thread, and checks each is denied.
func deniedOnEveryThread(path string) []string {
	const threads = 32
	var ready, done sync.WaitGroup
	ready.Add(threads)
	start := make(chan struct{})
	errs := make([]error, threads)
	for i := range threads {
		done.Go(func() {
			runtime.LockOSThread()
			ready.Done()
			<-start
			_, errs[i] = os.Open(path)
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	var failed []string
	for i, err := range errs {
		failed = append(failed, denied(fmt.Sprintf("open on thread %d", i), err)...)
	}
	return failed
}

func caseParam() []string {
	allowed, err := filepath.EvalSymlinks(os.Getenv(childAllow))
	if err != nil {
		return []string{err.Error()}
	}
	outside := os.Getenv(childTemp)
	profile := denyAll + `(allow file-read* (subpath (param "ALLOWED")))` + "\n"
	if err := Confine(Profile{Name: "param", Text: profile}, map[string]string{"ALLOWED": allowed}); err != nil {
		return []string{fmt.Sprintf("Confine: %v", err)}
	}
	var failed []string
	if b, err := os.ReadFile(filepath.Join(allowed, "inside")); err != nil || string(b) != "inside" {
		failed = append(failed, fmt.Sprintf("read inside the parameter's directory: %q, %v", b, err))
	}
	_, err = os.ReadFile(outside)
	return append(failed, denied("read outside the parameter's directory", err)...)
}

func caseAllowDefault() []string {
	err := Confine(Profile{Name: "allow-all", Text: "(version 1)\n(allow default)\n"}, nil)
	return wantRule("Confine", err, RuleProbeAllowed)
}

func caseSyntaxError() []string {
	err := Confine(Profile{Name: "broken", Text: "(version 1)\n(deny default\n"}, nil)
	return append(wantRule("Confine", err, RuleInitFailed), unconfined()...)
}

func caseSecondConfine() []string {
	if err := Confine(Profile{Name: "deny-all", Text: denyAll}, nil); err != nil {
		return []string{fmt.Sprintf("first Confine: %v", err)}
	}
	var failed []string
	for _, text := range []string{denyAll, "(version 1)\n(allow default)\n"} {
		err := Confine(Profile{Name: "second", Text: text}, nil)
		failed = append(failed, wantRule("second Confine", err, RuleInitFailed)...)
	}
	// The kernel itself refuses a wider profile in a confined process,
	// which is what keeps a compromised daemon from undoing its own.
	if _, ok := sandboxInit("(version 1)\n(allow default)\n", nil); ok {
		failed = append(failed, "sandbox_init_with_parameters applied (allow default) in a confined process")
	}
	return failed
}

// refusedCases are profiles and parameters that Confine refuses before
// the call.
var refusedCases = map[string]struct {
	text   string
	params map[string]string
	rule   Rule
}{
	"missing":   {denyAll + `(allow file-read* (subpath (param "A")))`, nil, RuleParamMissing},
	"extra":     {denyAll, map[string]string{"A": "/tmp"}, RuleParamUnexpected},
	"empty":     {denyAll + `(allow file-read* (subpath (param "A")))`, map[string]string{"A": ""}, RuleParamInvalid},
	"nul":       {denyAll + `(allow file-read* (subpath (param "A")))`, map[string]string{"A": "/tmp\x00/x"}, RuleParamInvalid},
	"lowercase": {denyAll + `(allow file-read* (subpath (param "x")))`, map[string]string{"x": "/tmp"}, RuleProfileParamForm},
}

func caseRefused(name string) []string {
	c, ok := refusedCases[name]
	if !ok {
		return []string{"unknown refused case " + name}
	}
	err := Confine(Profile{Name: name, Text: c.text}, c.params)
	return append(wantRule("Confine", err, c.rule), unconfined()...)
}

// TestConfineDenyDefault is the C11-host-daemons case "Self-sandbox",
// with the tests after it: it confines a child under (deny default).
// Files, the network and running programs are denied on every thread,
// and inherited descriptors still work.
func TestConfineDenyDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(temp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	ours, theirs, err := fdchan.Socketpair(4096)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fdchan.New(ours, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	out := runChild(t, "deny-default",
		[]string{childHome + "=" + home, childTemp + "=" + temp, childAddr + "=" + l.Addr().String()},
		pw, theirs)
	_ = pw.Close()
	_ = theirs.Close()
	if !strings.Contains(out, "rule="+string(RuleApplied)) {
		t.Errorf("no %s in the child's log:\n%s", RuleApplied, out)
	}

	m, err := ch.Recv()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if string(m.Record) != "descriptor" || m.File == nil {
		t.Fatalf("got %q with %v", m.Record, m.File)
	}
	if _, err := m.File.Write([]byte(", and the passed one")); err != nil {
		t.Fatal(err)
	}
	_ = m.File.Close()
	got, err := io.ReadAll(pr)
	if err != nil || string(got) != "through the pipe, and the passed one" {
		t.Fatalf("pipe holds %q, %v", got, err)
	}
}

// TestConfineParam reads through a profile parameter.
func TestConfineParam(t *testing.T) {
	allowed := t.TempDir()
	if err := os.WriteFile(filepath.Join(allowed, "inside"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	runChild(t, "param", []string{childAllow + "=" + allowed, childTemp + "=" + outside})
}

// TestConfineRefusals runs the cases that must fail closed, each in its
// own child.
func TestConfineRefusals(t *testing.T) {
	for _, name := range []string{"allow-default", "syntax-error", "second-confine"} {
		t.Run(name, func(t *testing.T) { runChild(t, name, nil) })
	}
	for name, c := range refusedCases {
		t.Run(name, func(t *testing.T) {
			out := runChild(t, "refuse-"+name, nil)
			if !strings.Contains(out, "rule="+string(c.rule)) {
				t.Errorf("no %s in the child's log:\n%s", c.rule, out)
			}
		})
	}
}
