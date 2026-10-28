package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

// The variables of the needs-vm test. `mise run go:test-vm` sets the
// last two.
const (
	// bundleVar names a provisioned test bundle, such as the fixture of
	// I379 in the spike cache (docs/agents/host-work.md, "VM bundles").
	bundleVar = "WB_VM_TEST_BUNDLE"
	// appVar is the built Wraith Box.app, whose wb-vmd, wb-diskfmt and
	// guest tools disk the child uses.
	appVar = "WB_VM_TEST_APP"
	// scratchVar is the directory of the child's <config> and <logs>.
	scratchVar = "WB_VM_TEST_SCRATCH"
)

// fixtureFiles are the files of the work slot's bundle the test clones
// from the test bundle (S06-05-disks, "Bundle files"). data.wbdisk isn't
// one: wb-hostd refuses a data disk it has no record of, and makes a new
// one. tools.img is wb-hostd's.
var fixtureFiles = []string{"hw.bin", "mid.bin", "aux.wbstore", "sys.wbdisk"}

// vmRun is wb-hostd with the real <data>, the test's <config> and
// <logs>, and the built bundle, run as a child process of the test.
type vmRun struct {
	t       *testing.T
	paths   paths.Paths
	scratch string
	app     string
	child   *exec.Cmd
	// started is when the test ran, so the log records of an earlier run
	// are never read.
	started time.Time
}

// TestC11HostdVM boots the work VM from a clone of WB_VM_TEST_BUNDLE with
// wb-hostd and the built bundle, and checks the guest link of
// S06-11-reconnect-and-recovery and the time of
// S06-06-devices-and-warm-start, "When the host sends the time". It is
// needs-vm and needs-host: wb-vmd boots only the slot bundle under the
// real <data> (S06-05-disks), so the test refuses to run when <data>
// exists, and removes it at its end. It logs every measurement with
// "measure:". Run it with `mise run go:test-vm`.
func TestC11HostdVM(t *testing.T) {
	r := newVMRun(t)
	r.cloneFixture()
	exe := filepath.Join(r.app, "Contents", "MacOS", "wb-hostd")

	r.startChild(exe)
	r.checkStart()
	r.checkReconnect()
	r.checkStop()

	r.checkKill(exe)
	r.stopChild()

	r.checkMismatch()
}

func newVMRun(t *testing.T) *vmRun {
	t.Helper()
	fixture, app, scratch := os.Getenv(bundleVar), os.Getenv(appVar), os.Getenv(scratchVar)
	if fixture == "" {
		t.Skipf("needs-vm: set %s to a provisioned test bundle, and run `mise run go:test-vm`", bundleVar)
	}
	if app == "" || scratch == "" {
		t.Fatalf("set %s and %s: run `mise run go:test-vm`", appVar, scratchVar)
	}
	if version.Build() == "" {
		t.Fatal("the test binary is unstamped: run `mise run go:test-vm`, which stamps it with build/version")
	}
	p, err := paths.Current(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	r := &vmRun{t: t, paths: p, scratch: scratch, app: app, started: time.Now()}
	// The test claims <data> by making it, so it never runs on a <data>
	// it didn't make.
	if err := os.Mkdir(p.Data, 0o700); errors.Is(err, fs.ErrExist) {
		t.Fatalf("%s exists: this test makes <data> and removes it at its end, so it runs only without one. "+
			"It may be left by an earlier run of this test: stop any wb-hostd it left (pgrep -fl wb-hostd.test, then kill <pid>), "+
			"check that nothing else of Wraith Box uses it, and remove it", p.Data)
	} else if err != nil {
		t.Fatal(err)
	}
	// Registered before stopChild, so it runs after the child stops.
	t.Cleanup(r.removeData)
	r.paths.Config = filepath.Join(scratch, "config")
	r.paths.Logs = filepath.Join(scratch, "logs")
	for _, dir := range []string{r.paths.Config, r.paths.Logs} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(r.stopChild)
	return r
}

// dataNames are the names in <data> that wb-hostd makes for the work VM:
// the run directory, the slot bundles' directory, and state.db with its
// journal and backups. vmsNames are those in <data>/vms.
var (
	dataNames = []string{"run", "vms", state.FileName}
	vmsNames  = []string{filepath.Base(vmbundle.Rel(vmbundle.SlotWork))}
)

// foreign returns the names in dir that aren't one of names or start
// with one of them.
func foreign(dir string, names []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !slices.ContainsFunc(names, func(n string) bool { return ownName(e.Name(), n) }) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// ownName reports whether name is n, or n with a suffix after a dot or a
// hyphen, such as state.db-wal and state.db.bak.
func ownName(name, n string) bool {
	return name == n || strings.HasPrefix(name, n+".") || strings.HasPrefix(name, n+"-")
}

// cloneFixture clones the test bundle's files into the work slot's
// bundle, with a placeholder tools.img whose inode the start replaces.
func (r *vmRun) cloneFixture() {
	t := r.t
	slot := r.slotBundle()
	if err := os.MkdirAll(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	src := os.Getenv(bundleVar)
	for _, name := range fixtureFiles {
		r.command("cp", "-c", filepath.Join(src, name), filepath.Join(slot, name))
	}
	if err := os.WriteFile(filepath.Join(slot, vm.ToolsDiskName), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *vmRun) slotBundle() string {
	return vmbundle.Path(r.paths.Data, vmbundle.SlotWork)
}

// checkStart starts the work VM, and checks the link, the status, the
// tools disk and the time.
func (r *vmRun) checkStart() {
	t := r.t
	tools := filepath.Join(r.slotBundle(), vm.ToolsDiskName)
	before := inode(t, tools)
	began := time.Now()
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	hello := r.waitRecord("guest hello accepted", began, time.Minute)
	t.Logf("measure: start to accepted hello %v", hello.time(t).Sub(began))
	_, out := r.wb("status", "work")
	want := "guest link: linked, wb-guestd version " + version.Build()
	if !strings.Contains(out, want) {
		t.Errorf("wb vm status: %q, want %q", out, want)
	}
	t.Logf("measure: generation %s", generationOf(t, out))
	if after := inode(t, tools); after == before {
		t.Errorf("tools.img has inode %d after the start, the placeholder's", after)
	} else {
		t.Logf("measure: tools.img inode %d before the start, %d after", before, after)
	}
	r.checkTime(began)
}

// checkTime checks the boot's time call. The host never reads the
// guest's clock (S06-06-devices-and-warm-start, "When the host sends the
// time"), and wb-guestd steps its clock to the time sent for a boot, so
// the guest's clock is off the host's by at most the call's round trip.
func (r *vmRun) checkTime(after time.Time) {
	t := r.t
	rec := r.waitRecord("guest time sent", after, 30*time.Second)
	if rec["reason"] != "TIME_REASON_BOOT" {
		t.Errorf("first time call reason %v, want TIME_REASON_BOOT", rec["reason"])
	}
	took := rec.duration(t, "took")
	t.Logf("measure: boot time call took %v, so the guest clock is within that of the host's", took)
	if took >= time.Second {
		t.Errorf("the boot time call took %v, want under 1s", took)
	}
}

// checkReconnect closes wb-hostd's end of the control link, and checks
// that the link is back within the reconnect waits.
func (r *vmRun) checkReconnect() {
	t := r.t
	dropped := time.Now()
	if err := r.child.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	r.waitRecord("guest link dropped", dropped, 10*time.Second)
	hello := r.waitRecord("guest hello accepted", dropped, 30*time.Second)
	took := hello.time(t).Sub(dropped)
	t.Logf("measure: link closed to accepted hello %v", took)
	// The first try is guestconn.FirstWait after the drop. A hello on a
	// guest that answers takes well under a second.
	if limit := guestconn.FirstWait + 2*time.Second; took > limit {
		t.Errorf("the link came back after %v, want within %v", took, limit)
	}
	if _, out := r.wb("status", "work"); !strings.Contains(out, "guest link: linked") {
		t.Errorf("wb vm status after the reconnect: %q, want linked", out)
	}
}

// checkStop stops the work VM, and checks that its wb-vmd exits.
func (r *vmRun) checkStop() {
	t := r.t
	pid := r.lastVMD()
	began := time.Now()
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Fatalf("wb vm stop work: exit %d, want 0", code)
	}
	t.Logf("measure: wb vm stop took %v", time.Since(began))
	if _, out := r.wb("status", "work"); !strings.Contains(out, "work VM: stopped") {
		t.Errorf("wb vm status after the stop: %q, want stopped", out)
	}
	t.Logf("measure: wb-vmd %d exited %v after the stop began", pid, waitExit(t, pid, time.Minute).Sub(began))
}

// checkKill kills wb-hostd with SIGKILL while the VM runs, checks that
// its wb-vmd stops the VM and exits by the lifeline
// (S04-07-supervision), and starts a new wb-hostd and the VM at once.
// The start's outcome is recorded, not checked: decision 5 of I288.
func (r *vmRun) checkKill(exe string) {
	t := r.t
	if code, _ := r.wb("start", "work"); code != 0 {
		t.Fatalf("wb vm start work: exit %d, want 0", code)
	}
	pid := r.lastVMD()
	killed := time.Now()
	if err := r.child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = r.child.Wait()
	r.child = nil
	r.startChild(exe)
	code, out := r.wb("start", "work")
	t.Logf("measure: start right after kill -9: exit %d, %v after the kill\n%s", code, time.Since(killed), out)
	_, status := r.wb("status", "work")
	t.Logf("measure: status right after kill -9:\n%s", status)
	t.Logf("measure: wb-vmd %d of the killed wb-hostd exited %v after the kill", pid, waitExit(t, pid, time.Minute).Sub(killed))
	if code != 0 {
		// Once the old wb-vmd is gone, the start goes through.
		if code, _ := r.wb("start", "work"); code != 0 {
			t.Errorf("wb vm start work after the old wb-vmd exited: exit %d, want 0", code)
		}
	}
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// checkMismatch runs wb-hostd from a bundle whose tools disk has
// another build's wb-guestd. The guest installs it at boot, and the
// link refuses its hello with the rule and both versions
// (S04-08-records-and-bounds, "With `wb-guestd`").
func (r *vmRun) checkMismatch() {
	t := r.t
	other := version.Build() + ".i288"
	r.startChild(r.mismatchApp(other))
	began := time.Now()
	if code, _ := r.wb("start", "work"); code != vmcmdFailure {
		t.Errorf("wb vm start work with another tools disk: exit %d, want %d", code, vmcmdFailure)
	}
	r.waitRecord("guest hello refused", began, time.Minute)
	_, out := r.wb("status", "work")
	for _, want := range []string{"guest link: refused", version.RuleMismatch, version.Label(other), version.Label(version.Build())} {
		if !strings.Contains(out, want) {
			t.Errorf("wb vm status: %q, want %q", out, want)
		}
	}
	if code, _ := r.wb("stop", "work"); code != 0 {
		t.Errorf("wb vm stop work: exit %d, want 0", code)
	}
}

// vmcmdFailure is wb's exit status of a failure
// (S05-02-exit-status-and-signals, "Exit status").
const vmcmdFailure = 255

// mismatchApp clones the built bundle into the scratch directory with a
// tools disk whose wb-guestd and VERSION are of version other, as
// scripts/app-bundle.sh makes it, and returns its wb-hostd path. The
// clone's seal is broken, which neither wb-hostd nor wb-vmd checks.
func (r *vmRun) mismatchApp(other string) string {
	t := r.t
	dir := filepath.Join(r.scratch, "mismatch")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	disk, mnt := filepath.Join(dir, "disk"), filepath.Join(dir, "mnt")
	for _, d := range []string{disk, mnt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	app := filepath.Join(dir, "Wraith Box.app")
	r.command("cp", "-Rc", r.app, app)
	img := filepath.Join(app, filepath.FromSlash(appbundle.ToolsDiskPath))
	r.command("hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mnt, img)
	r.command("cp", "-R", mnt+"/.", disk)
	r.command("hdiutil", "detach", "-quiet", mnt)
	guestd := filepath.Join(disk, "wb-guestd")
	if err := os.Remove(guestd); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags=-X "+version.StampVar+"="+other, "-o", guestd, "../wb-guestd")
	build.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build wb-guestd: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(disk, appbundle.ToolsVersionFile), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	r.command("hdiutil", "create", "-quiet", "-srcfolder", disk, "-fs", "APFS", "-volname", "wb-guest-tools",
		"-format", "UDTO", "-o", filepath.Join(dir, "tools"))
	if err := os.Rename(filepath.Join(dir, "tools.cdr"), img); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(app, "Contents", "MacOS", "wb-hostd")
}

// startChild starts the test binary as wb-hostd running from exe, and
// waits until it serves.
func (r *vmRun) startChild(exe string) {
	t := r.t
	out, err := os.OpenFile(filepath.Join(r.scratch, "child.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	// Not t.Context: the test stops the child itself, with SIGTERM.
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childVar+"="+exe, configVar+"="+r.paths.Config, logsVar+"="+r.paths.Logs)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.child = cmd
	deadline := time.Now().Add(30 * time.Second)
	for {
		if code, _ := r.wbQuiet("status", "work"); code == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("wb-hostd doesn't serve after 30s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopChild stops the child with SIGTERM, which stops its VMs by their
// lifelines.
func (r *vmRun) stopChild() {
	if r.child == nil {
		return
	}
	c := r.child
	r.child = nil
	_ = c.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			r.t.Errorf("wb-hostd child: %v", err)
		}
	case <-time.After(time.Minute):
		_ = c.Process.Kill()
		r.t.Errorf("wb-hostd child didn't stop on SIGTERM in 1m, killed")
		<-done
	}
}

// removeData removes the <data> the test made, once every wb-vmd it
// started has exited. It leaves a <data> that holds anything wb-hostd
// doesn't make for the work VM, which something else put there during
// the run.
func (r *vmRun) removeData() {
	t := r.t
	for _, pid := range r.vmdPIDs() {
		if alive(pid) {
			waitExit(t, pid, time.Minute)
		}
	}
	top, err := foreign(r.paths.Data, dataNames)
	if err == nil {
		var vms []string
		vms, err = foreign(filepath.Join(r.paths.Data, "vms"), vmsNames)
		top = append(top, vms...)
	}
	if err != nil || len(top) > 0 {
		t.Errorf("%s holds %v that this test didn't make (%v), so it is left: check what made it, and remove <data> by hand", r.paths.Data, top, err)
		return
	}
	if err := os.RemoveAll(r.paths.Data); err != nil {
		t.Errorf("remove %s: %v: remove it by hand", r.paths.Data, err)
	}
}

// wb runs `wb vm <args>` against the child, and logs its output.
func (r *vmRun) wb(args ...string) (int, string) {
	code, out := r.wbQuiet(args...)
	r.t.Logf("wb vm %s: exit %d\n%s", strings.Join(args, " "), code, out)
	return code, out
}

func (r *vmRun) wbQuiet(args ...string) (int, string) {
	var out bytes.Buffer
	env := vmcmd.SystemEnv(slog.New(slog.DiscardHandler))
	env.Stdout, env.Stderr = &out, &out
	code := vmcmd.Run(r.t.Context(), env, args)
	return code, out.String()
}

// command runs a program, and fails the test when it fails.
func (r *vmRun) command(name string, args ...string) {
	r.t.Helper()
	if out, err := exec.CommandContext(r.t.Context(), name, args...).CombinedOutput(); err != nil {
		r.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// record is a record of wb-hostd.log.
type record map[string]any

func (rec record) time(t *testing.T) time.Time {
	t.Helper()
	s, _ := rec["time"].(string)
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("record time %q: %v", s, err)
	}
	return at
}

// duration reads a slog.Duration, which the JSON handler writes in
// nanoseconds.
func (rec record) duration(t *testing.T, key string) time.Duration {
	t.Helper()
	n, ok := rec[key].(float64)
	if !ok {
		t.Fatalf("record %v has no duration %s", rec, key)
	}
	return time.Duration(n)
}

// records returns the records of wb-hostd.log with msg, logged at or
// after after, oldest first.
func (r *vmRun) records(msg string, after time.Time) []record {
	t := r.t
	b, err := os.ReadFile(filepath.Join(r.paths.Logs, "wb-hostd.log"))
	if err != nil {
		return nil
	}
	var out []record
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		rec := record{}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec["msg"] != msg {
			continue
		}
		if !rec.time(t).Before(after) {
			out = append(out, rec)
		}
	}
	return out
}

// waitRecord waits for the first record with msg at or after after.
func (r *vmRun) waitRecord(msg string, after time.Time, limit time.Duration) record {
	t := r.t
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if recs := r.records(msg, after); len(recs) > 0 {
			return recs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in wb-hostd.log after %v", msg, limit)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// vmdPIDs are the process IDs of every wb-vmd the test's wb-hostd
// started.
func (r *vmRun) vmdPIDs() []int {
	var ids []int
	for _, rec := range r.records("wb-vmd started", r.started) {
		if pid, ok := rec["pid"].(float64); ok {
			ids = append(ids, int(pid))
		}
	}
	return ids
}

func (r *vmRun) lastVMD() int {
	ids := r.vmdPIDs()
	if len(ids) == 0 {
		r.t.Fatal("no wb-vmd started in wb-hostd.log")
	}
	return ids[len(ids)-1]
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitExit waits until pid has exited, and returns when it saw that.
func waitExit(t *testing.T, pid int, limit time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(limit)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("wb-vmd %d still runs after %v", pid, limit)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Now()
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s: no stat", path)
	}
	return st.Ino
}

var generationRE = regexp.MustCompile(`generation (\d+)`)

func generationOf(t *testing.T, status string) string {
	t.Helper()
	m := generationRE.FindStringSubmatch(status)
	if m == nil {
		t.Errorf("wb vm status has no generation: %q", status)
		return ""
	}
	return m[1]
}
