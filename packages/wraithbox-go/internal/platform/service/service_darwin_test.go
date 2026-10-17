package service

import (
	"context"
	"encoding/xml"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	testUID     = 501
	testProgram = "/Users/u/Applications/Wraith Box.app/Contents/MacOS/wb-hostd"
)

// recorder is a Runner that records each command, and answers from
// codes: the exit code for each launchctl subcommand, in order of the
// calls to it. The last code of a list repeats.
type recorder struct {
	t     *testing.T
	codes map[string][]int
	calls []Command
}

func (r *recorder) run(_ context.Context, c Command) (Result, error) {
	r.calls = append(r.calls, c)
	if len(c.Args) == 0 {
		r.t.Fatalf("launchctl ran with no arguments")
	}
	codes := r.codes[c.Args[0]]
	if len(codes) == 0 {
		r.t.Fatalf("unexpected launchctl %v", c.Args)
	}
	code := codes[0]
	if len(codes) > 1 {
		r.codes[c.Args[0]] = codes[1:]
	}
	return Result{Code: code, Output: []byte("out\x1b[2J")}, nil
}

// args returns each recorded call's arguments, joined by spaces.
func (r *recorder) args() []string {
	var out []string
	for _, c := range r.calls {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

func newAgent(t *testing.T, codes map[string][]int) (*launchAgent, *recorder, string) {
	t.Helper()
	home := t.TempDir()
	rec := &recorder{t: t, codes: codes}
	m, err := New(Config{Home: home, UID: testUID, Run: rec.run, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := m.(*launchAgent)
	if !ok {
		t.Fatalf("New gave a %T", m)
	}
	a.poll = time.Millisecond
	a.wait = 50 * time.Millisecond
	return a, rec, home
}

// TestC12SetupLaunchctlPathAndEnv sees every launchctl run as
// /bin/launchctl with PATH=/usr/bin:/bin and nothing else
// (S17-02-setup, "The agent").
func TestC12SetupLaunchctlPathAndEnv(t *testing.T) {
	a, rec, _ := newAgent(t, map[string][]int{"print": {0, 113}, "bootout": {0}, "bootstrap": {0}})
	if _, err := a.State(context.Background(), testProgram); err != nil {
		t.Fatal(err)
	}
	if err := a.Replace(context.Background(), testProgram); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) == 0 {
		t.Fatal("no launchctl run")
	}
	for _, c := range rec.calls {
		if c.Path != "/bin/launchctl" || !slices.Equal(c.Env, []string{"PATH=/usr/bin:/bin"}) {
			t.Errorf("ran %q with environment %q", c.Path, c.Env)
		}
	}
}

// TestC12SetupPlist writes the plist and checks its mode, that it came by
// rename, and its keys (S17-02-setup, "The agent"; S04-07-supervision,
// "Who starts whom").
func TestC12SetupPlist(t *testing.T) {
	a, rec, home := newAgent(t, map[string][]int{"print": {0, 113}, "bootout": {0}, "bootstrap": {0}})
	path := filepath.Join(home, "Library", "LaunchAgents", "nl.wraithbox.hostd.plist")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A hard link to the old plist keeps the old bytes only when the new
	// plist came by rename, not by a write into the old file.
	keep := filepath.Join(home, "old-link")
	if err := os.Link(path, keep); err != nil {
		t.Fatal(err)
	}
	if err := a.Replace(context.Background(), testProgram); err != nil {
		t.Fatal(err)
	}
	if old, _ := os.ReadFile(keep); string(old) != "old" {
		t.Errorf("the old plist's file now holds %q: the plist wasn't renamed in", old)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %04o, want 0644", fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := plistKeys(t, data)
	want := map[string]string{
		"Label":            "string:nl.wraithbox.hostd",
		"ProgramArguments": "array:" + testProgram,
		"RunAtLoad":        "true:",
		"KeepAlive":        "true:",
	}
	if !mapsEqual(keys, want) {
		t.Errorf("plist keys %v, want %v", keys, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("LaunchAgents holds %d files, want only the plist", len(entries))
	}
	wantCalls := []string{"bootout gui/501/nl.wraithbox.hostd", "print gui/501/nl.wraithbox.hostd", "print gui/501/nl.wraithbox.hostd", "bootstrap gui/501 " + path}
	if got := rec.args(); !slices.Equal(got, wantCalls) {
		t.Errorf("launchctl runs %q, want %q", got, wantCalls)
	}
}

// plistKeys decodes the top-level dict of a plist into key -> kind:text,
// where text is the string, or the array's one string.
func plistKeys(t *testing.T, data []byte) map[string]string {
	t.Helper()
	var doc struct {
		Dict struct {
			Items []struct {
				XMLName xml.Name
				Text    string   `xml:",chardata"`
				Strings []string `xml:"string"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	items := doc.Dict.Items
	for i := 0; i+1 < len(items); i += 2 {
		v := items[i+1]
		text := strings.TrimSpace(v.Text)
		if v.XMLName.Local == "array" {
			text = strings.Join(v.Strings, ",")
		}
		out[items[i].Text] = v.XMLName.Local + ":" + text
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestPlistEscapesAndRefuses(t *testing.T) {
	data, err := Plist("/Users/a&b/<x>/wb-hostd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>/Users/a&amp;b/&lt;x&gt;/wb-hostd</string>") {
		t.Errorf("the path isn't escaped:\n%s", data)
	}
	for _, bad := range []string{"wb-hostd", "/a/\nb", "/a/\xff", ""} {
		if _, err := Plist(bad); err == nil {
			t.Errorf("Plist(%q) took it", bad)
		}
	}
}

func TestStateNoPlist(t *testing.T) {
	a, _, _ := newAgent(t, map[string][]int{"print": {113}})
	st, err := a.State(context.Background(), testProgram)
	if err != nil || st != (State{}) {
		t.Fatalf("State = %+v, %v; want neither", st, err)
	}
}

func TestStateSameBytesLoaded(t *testing.T) {
	a, rec, _ := newAgent(t, map[string][]int{"print": {0}})
	writeCurrent(t, a)
	st, err := a.State(context.Background(), testProgram)
	if err != nil || st != (State{Current: true, Loaded: true}) {
		t.Fatalf("State = %+v, %v; want current and loaded", st, err)
	}
	if got := rec.args(); !slices.Equal(got, []string{"print gui/501/nl.wraithbox.hostd"}) {
		t.Errorf("launchctl runs %q, want only print", got)
	}
}

func TestStateOtherProgram(t *testing.T) {
	a, _, _ := newAgent(t, map[string][]int{"print": {0}})
	writeCurrent(t, a)
	st, err := a.State(context.Background(), "/Applications/Wraith Box.app/Contents/MacOS/wb-hostd")
	if err != nil || st.Current {
		t.Fatalf("State = %+v, %v; want not current", st, err)
	}
}

// TestStateLink counts a plist that is a symbolic link to the same bytes
// as not current, so Replace puts a plain file there.
func TestStateLink(t *testing.T) {
	a, _, home := newAgent(t, map[string][]int{"print": {0}})
	writeCurrent(t, a)
	moved := filepath.Join(home, "elsewhere.plist")
	if err := os.Rename(a.plist, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, a.plist); err != nil {
		t.Fatal(err)
	}
	if st, err := a.State(context.Background(), testProgram); err != nil || st.Current {
		t.Fatalf("State = %+v, %v; want a link not current", st, err)
	}
}

func TestStatePrintFails(t *testing.T) {
	a, _, _ := newAgent(t, map[string][]int{"print": {5}})
	_, err := a.State(context.Background(), testProgram)
	if err == nil || !strings.Contains(err.Error(), `exited with 5: "out\x1b[2J"`) {
		t.Fatalf("err = %v, want the failed print with its output quoted", err)
	}
}

func writeCurrent(t *testing.T, a *launchAgent) {
	t.Helper()
	data, err := Plist(testProgram)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.writePlist(data); err != nil {
		t.Fatal(err)
	}
}

// TestBootoutOfAnAgentNotLoaded sees a failed bootout of an agent that
// isn't loaded count as done, and one of a loaded agent fail.
func TestBootoutOfAnAgentNotLoaded(t *testing.T) {
	ctx := context.Background()
	a, rec, _ := newAgent(t, map[string][]int{"bootout": {3}, "print": {113}, "bootstrap": {0}})
	if err := a.Replace(ctx, testProgram); err != nil {
		t.Fatal(err)
	}
	if got := rec.args(); len(got) != 3 || !strings.HasPrefix(got[2], "bootstrap ") {
		t.Errorf("launchctl runs %q, want bootout, print, bootstrap", got)
	}

	// A nonzero bootout of an agent that launchd drops within the wait
	// counts as done too.
	a, _, _ = newAgent(t, map[string][]int{"bootout": {5}, "print": {0, 113}, "bootstrap": {0}})
	if err := a.Replace(ctx, testProgram); err != nil {
		t.Fatal(err)
	}
}

// TestFailedBootoutKeepsTheOldPlist sees a bootout that fails while the
// agent stays loaded end Replace before the write: the old plist stays
// on disk, the next State isn't Current, and nothing is bootstrapped.
func TestFailedBootoutKeepsTheOldPlist(t *testing.T) {
	ctx := context.Background()
	a, rec, _ := newAgent(t, map[string][]int{"bootout": {5}, "print": {0}})
	old, err := Plist("/Applications/Wraith Box.app/Contents/MacOS/wb-hostd")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.writePlist(old); err != nil {
		t.Fatal(err)
	}
	err = a.Replace(ctx, testProgram)
	if err == nil || !strings.Contains(err.Error(), "bootout gui/501/nl.wraithbox.hostd exited with 5") {
		t.Fatalf("Replace = %v, want the failed bootout", err)
	}
	if slices.ContainsFunc(rec.args(), func(s string) bool { return strings.HasPrefix(s, "bootstrap") }) {
		t.Error("bootstrap ran after a failed bootout")
	}
	if have, _ := os.ReadFile(a.plist); string(have) != string(old) {
		t.Errorf("the plist on disk is\n%s\nwant the old one", have)
	}
	if st, err := a.State(ctx, testProgram); err != nil || st.Current {
		t.Errorf("State = %+v, %v; want not current after the failed replace", st, err)
	}
}

func TestBootoutWaitsUntilUnloaded(t *testing.T) {
	a, rec, _ := newAgent(t, map[string][]int{"bootout": {0}, "print": {0, 0, 113}, "bootstrap": {0}})
	if err := a.Replace(context.Background(), testProgram); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(rec.args(), "\n"), "print"); n != 3 {
		t.Errorf("%d prints, want 3", n)
	}
}

func TestBootstrapFails(t *testing.T) {
	a, _, _ := newAgent(t, map[string][]int{"bootstrap": {5}})
	if err := a.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "bootstrap gui/501") {
		t.Fatalf("Start = %v, want the failed bootstrap", err)
	}
}

func TestRunnerError(t *testing.T) {
	m, err := New(Config{Home: t.TempDir(), UID: testUID, Run: func(context.Context, Command) (Result, error) {
		return Result{}, errors.New("no launchctl")
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.State(context.Background(), testProgram); err == nil || !strings.Contains(err.Error(), "no launchctl") {
		t.Fatalf("State = %v, want the runner's error", err)
	}
}

func TestNewRefusesARelativeHome(t *testing.T) {
	if _, err := New(Config{Home: "home"}); err == nil {
		t.Fatal("New took a relative home")
	}
}
