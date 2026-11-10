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
	// testVersion is the build version of wb and of its bundle.
	testVersion = "0.1.0-dev+g1234abc"
)

// control is what the fake helper does.
type control struct {
	// Mode is "script", which prints Stdout and Stderr and exits with
	// Exit; "no-read", which does the same without reading its standard
	// input; "kill", which ends on SIGKILL; or "real", which runs
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
	var stdin []byte
	if c.Mode != "no-read" {
		stdin, _ = io.ReadAll(os.Stdin)
	}
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
	fake := storetest.New(storeitem.ProgramCredset, team, group)
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
	writePlist(t, root, testVersion)
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

// writePlist writes the bundle's Info.plist with the build version v.
func writePlist(t *testing.T, root, v string) {
	t.Helper()
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>` + appbundle.BuildVersionKey + `</key>
	<string>` + v + `</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(appbundle.InfoPlistPath)), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
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

// harness is the Env of one test, and what wb printed and logged.
type harness struct {
	env                 Env
	stdout, stderr, log bytes.Buffer
	// noCore counts the calls of env.NoCore.
	noCore int
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
		Version:   testVersion,
		NoCore: func() error {
			h.noCore++
			return nil
		},
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) out {
	t.Helper()
	code := Run(t.Context(), h.env, args)
	return out{code: code, stdout: h.stdout.String(), stderr: h.stderr.String(), log: h.log.String()}
}

// errReader fails the test when it is read.
type errReader struct{ t *testing.T }

func (r errReader) Read([]byte) (int, error) {
	r.t.Error("wb read its standard input")
	return 0, io.ErrUnexpectedEOF
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
