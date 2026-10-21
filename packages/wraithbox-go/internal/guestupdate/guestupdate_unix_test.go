//go:build unix

// The update runs on unix guests only (internal/platform/guesttools), and
// these tests use symbolic links and file modes, which Windows treats
// differently.

package guestupdate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestupdate"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guesttools"
)

const (
	own     = "1.0.0+b1"
	newer   = "1.1.0+b2"
	helper  = "git-remote-wb"
	guestd  = guestupdate.SelfName
	tmpName = "." + guestd + ".new"
)

// errRan is what the fake Exec returns. A real one doesn't return.
var errRan = errors.New("ran the installed program")

// fakePlatform stands in for internal/platform/guesttools.
type fakePlatform struct {
	// mount is CheckMount's answer. nil passes.
	mount      func() error
	prepareErr error

	mu       sync.Mutex
	checks   int
	prepared []string
	execs    []string
}

func (p *fakePlatform) CheckMount(string) error {
	p.mu.Lock()
	p.checks++
	p.mu.Unlock()
	if p.mount == nil {
		return nil
	}
	return p.mount()
}

func (p *fakePlatform) PrepareDir(dir string) error {
	p.prepared = append(p.prepared, dir)
	return p.prepareErr
}

func (p *fakePlatform) Exec(path string) error {
	p.execs = append(p.execs, path)
	return errRan
}

// env is one update's disk, install directory and log.
type env struct {
	t        *testing.T
	disk     string
	dir      string
	platform *fakePlatform
	log      bytes.Buffer
}

// newEnv makes a disk at version v with both programs on it, and an
// empty install directory.
func newEnv(t *testing.T, v string) *env {
	t.Helper()
	e := &env{t: t, disk: t.TempDir(), dir: t.TempDir(), platform: &fakePlatform{}}
	e.write(e.disk, "VERSION", v)
	e.write(e.disk, helper, "helper "+v)
	e.write(e.disk, guestd, "guestd "+v)
	return e
}

func (e *env) write(dir, name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) config() guestupdate.Config {
	return guestupdate.Config{
		Own:      own,
		Log:      slog.New(slog.NewJSONHandler(&e.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Disk:     e.disk,
		Dir:      e.dir,
		Programs: []string{helper, guestd},
		Platform: e.platform,
		Wait:     250 * time.Millisecond,
	}
}

func (e *env) run(cfg guestupdate.Config) error {
	e.t.Helper()
	return guestupdate.AtStart(e.t.Context(), cfg)
}

// rules returns the rule of each log record that has one.
func (e *env) rules() []string {
	var rules []string
	for line := range strings.Lines(e.log.String()) {
		var rec struct{ Rule string }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			e.t.Fatalf("log line %q: %v", line, err)
		}
		if rec.Rule != "" {
			rules = append(rules, rec.Rule)
		}
	}
	return rules
}

// installed returns the install directory's files and their contents.
func (e *env) installed() map[string]string {
	e.t.Helper()
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	files := map[string]string{}
	for _, de := range entries {
		b, err := os.ReadFile(filepath.Join(e.dir, de.Name()))
		if err != nil {
			files[de.Name()] = "unreadable: " + err.Error()
			continue
		}
		files[de.Name()] = string(b)
	}
	return files
}

// refusedWith checks that the update was refused with rule, installed
// nothing and ran nothing.
func (e *env) refusedWith(err error, rule string) {
	e.t.Helper()
	if err != nil {
		e.t.Fatalf("AtStart = %v, want nil: wb-guestd serves as it is", err)
	}
	if got := e.rules(); !slices.Equal(got, []string{rule}) {
		e.t.Fatalf("rules %q, want [%s]\n%s", got, rule, e.log.String())
	}
	if len(e.platform.execs) != 0 {
		e.t.Fatalf("ran %q", e.platform.execs)
	}
	if got := e.installed(); len(got) != 0 {
		e.t.Fatalf("installed %v", got)
	}
}

// TestCurrent: a disk of wb-guestd's own version installs nothing.
func TestCurrent(t *testing.T) {
	e := newEnv(t, own)
	if err := e.run(e.config()); err != nil {
		t.Fatalf("AtStart = %v", err)
	}
	if got := e.installed(); len(got) != 0 || len(e.platform.execs) != 0 || len(e.rules()) != 0 {
		t.Fatalf("installed %v, ran %q, rules %q", got, e.platform.execs, e.rules())
	}
	if !slices.Equal(e.platform.prepared, []string{e.dir}) {
		t.Fatalf("prepared %q", e.platform.prepared)
	}
}

// TestInstall: a disk of another version, or a wb-guestd without a
// version, installs every program, mode 0755, and runs the installed
// wb-guestd.
func TestInstall(t *testing.T) {
	for _, from := range []string{own, ""} {
		t.Run("from "+from, func(t *testing.T) {
			e := newEnv(t, newer)
			cfg := e.config()
			cfg.Own = from
			if err := e.run(cfg); !errors.Is(err, errRan) {
				t.Fatalf("AtStart = %v, want the exec's error", err)
			}
			e.installedNewer()
			if !slices.Equal(e.platform.execs, []string{filepath.Join(e.dir, guestd)}) {
				t.Fatalf("ran %q", e.platform.execs)
			}
			if got := e.rules(); !slices.Equal(got, []string{guestupdate.RuleExecFailed}) {
				t.Fatalf("rules %q", got)
			}
		})
	}
}

// installedNewer checks that the install directory holds both programs
// of the disk at version newer, mode 0755, and nothing else.
func (e *env) installedNewer() {
	e.t.Helper()
	want := map[string]string{helper: "helper " + newer, guestd: "guestd " + newer}
	if got := e.installed(); !maps.Equal(got, want) {
		e.t.Fatalf("installed %v, want %v", got, want)
	}
	for name := range want {
		fi, err := os.Lstat(filepath.Join(e.dir, name))
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o755 {
			e.t.Fatalf("%s: %v, %v; want a regular file, mode 0755", name, fi, err)
		}
	}
}

// TestReplacesLinks: a symbolic link at a program's name or at the
// temporary name is replaced, and the file it points to is left alone.
func TestReplacesLinks(t *testing.T) {
	e := newEnv(t, newer)
	outside := filepath.Join(t.TempDir(), "outside")
	e.write(filepath.Dir(outside), "outside", "keep")
	for _, name := range []string{guestd, tmpName} {
		if err := os.Symlink(outside, filepath.Join(e.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.run(e.config()); !errors.Is(err, errRan) {
		t.Fatalf("AtStart = %v", err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("outside file: %q, %v", b, err)
	}
	e.installedNewer()
}

// TestVersionInvalid: a VERSION that is missing, a link, a directory,
// too long, or not a build version is refused before anything is
// installed.
func TestVersionInvalid(t *testing.T) {
	cases := map[string]func(e *env){
		"missing":       func(e *env) { must(t, os.Remove(filepath.Join(e.disk, "VERSION"))) },
		"empty":         func(e *env) { e.write(e.disk, "VERSION", "") },
		"newline":       func(e *env) { e.write(e.disk, "VERSION", newer+"\n") },
		"not a version": func(e *env) { e.write(e.disk, "VERSION", "latest") },
		"too long":      func(e *env) { e.write(e.disk, "VERSION", "1.0.0+"+strings.Repeat("a", 59)) },
		"a link": func(e *env) {
			must(t, os.Remove(filepath.Join(e.disk, "VERSION")))
			e.write(e.disk, "real", newer)
			must(t, os.Symlink("real", filepath.Join(e.disk, "VERSION")))
		},
		"a directory": func(e *env) {
			must(t, os.Remove(filepath.Join(e.disk, "VERSION")))
			must(t, os.Mkdir(filepath.Join(e.disk, "VERSION"), 0o755))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, newer)
			change(e)
			e.refusedWith(e.run(e.config()), guestupdate.RuleVersionInvalid)
		})
	}
}

// TestProgramInvalid: a program of the layout that is missing, a link,
// a directory or over the size cap is refused before any program is
// installed, the others included.
func TestProgramInvalid(t *testing.T) {
	cases := map[string]func(e *env, cfg *guestupdate.Config){
		"missing": func(e *env, _ *guestupdate.Config) { must(t, os.Remove(filepath.Join(e.disk, guestd))) },
		"a link": func(e *env, _ *guestupdate.Config) {
			must(t, os.Remove(filepath.Join(e.disk, guestd)))
			must(t, os.Symlink(helper, filepath.Join(e.disk, guestd)))
		},
		"a directory": func(e *env, _ *guestupdate.Config) {
			must(t, os.Remove(filepath.Join(e.disk, guestd)))
			must(t, os.Mkdir(filepath.Join(e.disk, guestd), 0o755))
		},
		"over the cap":       func(_ *env, cfg *guestupdate.Config) { cfg.MaxSize = 8 },
		"wb-guestd not last": func(_ *env, cfg *guestupdate.Config) { cfg.Programs = []string{guestd, helper} },
		"no programs":        func(_ *env, cfg *guestupdate.Config) { cfg.Programs = []string{} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, newer)
			cfg := e.config()
			change(e, &cfg)
			e.refusedWith(e.run(cfg), guestupdate.RuleProgramInvalid)
		})
	}
}

// TestNoEffect: when the installed wb-guestd is already the disk's, yet
// the disk's VERSION isn't wb-guestd's own version, the update doesn't
// install again, so a disk whose VERSION doesn't match its program can't
// make wb-guestd loop.
func TestNoEffect(t *testing.T) {
	e := newEnv(t, newer)
	e.write(e.dir, guestd, "guestd "+newer)
	err := e.run(e.config())
	if err != nil || !slices.Equal(e.rules(), []string{guestupdate.RuleNoEffect}) || len(e.platform.execs) != 0 {
		t.Fatalf("AtStart = %v, rules %q, ran %q", err, e.rules(), e.platform.execs)
	}
	if got := e.installed(); len(got) != 1 {
		t.Fatalf("installed %v", got)
	}
}

// TestInstallFailed: when wb-guestd can't be installed, the programs
// before it stay installed, nothing runs, and wb-guestd serves as it is.
// wb-guestd is installed last, so the next start tries again.
func TestInstallFailed(t *testing.T) {
	e := newEnv(t, newer)
	must(t, os.Mkdir(filepath.Join(e.dir, guestd), 0o755))
	e.write(filepath.Join(e.dir, guestd), "x", "")
	err := e.run(e.config())
	if err != nil || !slices.Equal(e.rules(), []string{guestupdate.RuleInstallFailed}) || len(e.platform.execs) != 0 {
		t.Fatalf("AtStart = %v, rules %q, ran %q", err, e.rules(), e.platform.execs)
	}
	got := e.installed()
	if got[helper] != "helper "+newer {
		t.Fatalf("installed %v, want %s first", got, helper)
	}
	if _, ok := got[tmpName]; ok {
		t.Fatalf("left %s", tmpName)
	}
}

// TestDiskRefused: a disk that never passes its mount check, an install
// directory that fails its check, and a platform without a tools disk
// each leave wb-guestd serving as it is, with the platform's rule
// logged. A platform without a tools disk doesn't wait.
func TestDiskRefused(t *testing.T) {
	notMounted := &guesttools.Refusal{Rule: guesttools.RuleNotMounted, Path: "/Volumes/wb-guest-tools"}
	unsafe := &guesttools.Refusal{Rule: guesttools.RuleDirUnsafe, Path: "/Library"}
	cases := []struct {
		name     string
		platform *fakePlatform
		rule     string
		maxWait  time.Duration
	}{
		{"never mounted", &fakePlatform{mount: func() error { return notMounted }}, guesttools.RuleNotMounted, 2 * time.Second},
		{"unsafe install directory", &fakePlatform{prepareErr: unsafe}, guesttools.RuleDirUnsafe, time.Second},
		{"unsupported", &fakePlatform{mount: func() error { return errors.ErrUnsupported }}, guestupdate.RuleUnsupported, 50 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, newer)
			e.platform = tc.platform
			start := time.Now()
			err := e.run(e.config())
			if took := time.Since(start); took > tc.maxWait {
				t.Fatalf("took %v", took)
			}
			e.refusedWith(err, tc.rule)
		})
	}
}

// TestWaitsForDisk: the update waits for the disk to be mounted, and
// then installs.
func TestWaitsForDisk(t *testing.T) {
	e := newEnv(t, newer)
	tries := 0
	e.platform.mount = func() error {
		if tries++; tries < 3 {
			return &guesttools.Refusal{Rule: guesttools.RuleNotMounted}
		}
		return nil
	}
	if err := e.run(e.config()); !errors.Is(err, errRan) {
		t.Fatalf("AtStart = %v", err)
	}
	if tries != 3 || !strings.Contains(e.log.String(), `"msg":"tools disk mounted"`) {
		t.Fatalf("%d tries\n%s", tries, e.log.String())
	}
}

// TestWaitCanceled: a stop while the update waits for the disk returns
// the context's error, and installs nothing.
func TestWaitCanceled(t *testing.T) {
	e := newEnv(t, newer)
	e.platform.mount = func() error { return &guesttools.Refusal{Rule: guesttools.RuleNotMounted} }
	cfg := e.config()
	cfg.Wait = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if err := guestupdate.AtStart(ctx, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AtStart = %v", err)
	}
	if got := e.installed(); len(got) != 0 {
		t.Fatalf("installed %v", got)
	}
}

// TestPrograms: the programs come from the layout table, wb-guestd last.
func TestPrograms(t *testing.T) {
	got := guestupdate.Programs()
	if len(got) == 0 || got[len(got)-1] != guestupdate.SelfName {
		t.Fatalf("Programs = %q", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
