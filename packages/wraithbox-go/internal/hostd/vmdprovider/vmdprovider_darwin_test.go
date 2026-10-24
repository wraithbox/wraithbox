package vmdprovider_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/logpipe"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vmdprovider"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskclone"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn/vmdfake"
)

// own is the build version of wb-hostd, the fake wb-vmd and its guest
// in these tests.
const own = "1.2.3-dev+abc"

// TestMain runs the fake wb-vmd when the test binary is started as
// wb-vmd. vmdspawn's tests check its start interface.
func TestMain(m *testing.M) {
	if vmdfake.Is() {
		os.Exit(vmdfake.Main(own, nil))
	}
	os.Exit(m.Run())
}

// syncBuffer is wb-hostd's log.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// harness is wb-hostd's VM manager with the vmd provider, which spawns
// this test binary as the fake wb-vmd, and guest links that follow its
// runs.
type harness struct {
	t        *testing.T
	cfg      vm.Config
	provider *vmdprovider.Provider
	links    *guestconn.Links
	files    *logpipe.Files
	logsDir  string
	runDir   string
	log      *syncBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The socket path must fit sun_path, and localipc refuses a run
	// directory that isn't 0700, which t.TempDir's subdirectories aren't.
	runDir, err := os.MkdirTemp("", "vp") //nolint:usetesting // sun_path length and the 0700 mode, see above
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })
	h := &harness{t: t, logsDir: t.TempDir(), runDir: runDir, log: &syncBuffer{}}
	logger := slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h.files = logpipe.NewFiles(h.logsDir, logger)
	h.provider, err = vmdprovider.New(vmdprovider.Config{
		Program: exe, RunDir: runDir, Version: own, Logs: h.files.Writer, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg = h.vmConfig(logger)
	return h
}

func (h *harness) vmConfig(logger *slog.Logger) vm.Config {
	t := h.t
	dataDir := t.TempDir()
	tools := filepath.Join(t.TempDir(), "wb-guest-tools.img")
	if err := os.WriteFile(tools, []byte("tools"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	shim := &datadisktest.Shim{DataDir: dataDir, UUIDs: []string{"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"}}
	disks, err := datadisk.New(datadisk.Config{DataDir: dataDir, Records: db, Shim: shim, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return vm.Config{
		DataDir: dataDir, ToolsImage: tools, Resources: config.Defaults(),
		Generations: db, Provider: h.provider, Cloner: diskclone.Cloner{}, DataDisks: disks,
		OnRunning: func(slot vm.Slot, generation uint64, ended <-chan struct{}) { h.links.Run(slot, generation, ended) },
		Logger:    logger,
	}
}

// manager returns the manager, and the links that follow its runs. The
// provider and the links are closed when the test ends, as wb-hostd
// closes them when it stops.
func (h *harness) manager() *vm.Manager {
	h.t.Helper()
	m, err := vm.New(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.links, err = guestconn.New(guestconn.Config{VMs: m, Version: own, Logger: slog.New(slog.NewJSONHandler(h.log, nil))})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		h.links.Close()
		if err := h.provider.Close(); err != nil {
			h.t.Error(err)
		}
		_ = h.files.Close()
	})
	return m
}

// records reads wb-vmd's log file, <logs>/wb-vmd-<slot>.log, as logpipe
// wrote it: one record for each line wb-vmd wrote on its log pipe.
func (h *harness) records(slot vm.Slot) []map[string]any {
	h.t.Helper()
	f, err := os.Open(filepath.Join(h.logsDir, "wb-vmd-"+string(slot)+".log"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var frame struct {
			Process string `json:"process"`
			Line    string `json:"line"`
		}
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil {
			h.t.Fatalf("log line %q: %v", sc.Bytes(), err)
		}
		if frame.Process != "wb-vmd-"+string(slot) {
			h.t.Errorf("process = %q", frame.Process)
		}
		var rec map[string]any
		if json.Unmarshal([]byte(frame.Line), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// waitEvent waits until wb-vmd's log has a record of event, and returns
// it.
func (h *harness) waitEvent(slot vm.Slot, event string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, rec := range h.records(slot) {
			if rec["event"] == event {
				return rec
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("no %s record in wb-vmd's log; wb-hostd's log:\n%s", event, h.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitStopped(t *testing.T, m *vm.Manager, slot vm.Slot) vm.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := m.Status(slot)
		if s.Phase == vm.PhaseStopped {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is %s, want stopped", slot, s.Phase)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitLinked(t *testing.T, links *guestconn.Links, slot vm.Slot) guestconn.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := links.Status(slot)
		if s.State == guestconn.StateLinked {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s link is %+v, want linked", slot, s)
		}
		time.Sleep(time.Millisecond)
	}
}

// A start spawns wb-vmd for the slot, whose log goes to
// <logs>/wb-vmd-work.log. The guest link says hello through wb-vmd's
// hand-off channel. A stop closes the instance, whose lifeline makes
// wb-vmd exit 0.
func TestStartHelloStop(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("start: %v\n%s", err, h.log.String())
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseRunning || s.Generation != 1 {
		t.Fatalf("after start = %+v", s)
	}
	if rec := h.waitEvent(vm.SlotWork, "started"); rec["slot"] != "work" {
		t.Errorf("started record %v", rec)
	}
	if s := waitLinked(t, h.links, vm.SlotWork); s.GuestVersion != own || s.Generation != 1 {
		t.Errorf("link = %+v", s)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("after stop = %+v", s)
	}
	h.waitEvent(vm.SlotWork, "lifeline-closed")
	if !strings.Contains(h.log.String(), `"msg":"wb-vmd exited"`) || !strings.Contains(h.log.String(), `"state":"exit status 0"`) {
		t.Errorf("no exit 0 in wb-hostd's log:\n%s", h.log.String())
	}
	if _, err := os.Stat(filepath.Join(h.runDir, "vmd-work-1.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
}

// A wb-vmd that exits, here killed, means its VM stopped
// (S04-07-supervision, "`wb-vmd` and `wb-vmd-install`").
func TestExitMarksStopped(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid, ok := h.waitEvent(vm.SlotWork, "started")["pid"].(float64)
	if !ok {
		t.Fatal("no pid in the started record")
	}
	if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if s := waitStopped(t, m, vm.SlotWork); s.Generation != 1 {
		t.Errorf("after the exit = %+v", s)
	}
	// wb-vmd exited on its own, so wb-hostd ended nothing.
	if strings.Contains(h.log.String(), "vmd-state-stream-ended") {
		t.Errorf("wb-hostd logged that it ended a wb-vmd that had exited:\n%s", h.log.String())
	}
	// The next start spawns a new wb-vmd with a new socket.
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseRunning || s.Generation != 2 {
		t.Errorf("after the second start = %+v", s)
	}
}

// A start the framework refuses keeps wb-vmd's rule, and the manager
// gets the framework's error with it.
func TestStartFailedKeepsRule(t *testing.T) {
	h := newHarness(t)
	h.cfg.Resources.MemoryMiB = vmdfake.FailMemoryMiB
	m := h.manager()
	err := m.Start(t.Context(), vm.SlotWork)
	r, ok := errors.AsType[*vm.Refusal](err)
	if !ok || r.Rule != vmdfake.RuleStartFailed {
		t.Fatalf("err = %v, want rule %s", err, vmdfake.RuleStartFailed)
	}
	if r.VMError == nil || r.VMError.GetFailureReason() != "fake" {
		t.Errorf("VMError = %v", r.VMError)
	}
	waitStopped(t, m, vm.SlotWork)
}

// A closed provider ends every wb-vmd it started, as wb-hostd does when
// it stops (S04-07-supervision, "When `wb-hostd` stops"), and starts no
// more.
func TestProviderCloseEndsAll(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	for _, slot := range vm.Slots() {
		if err := m.Start(t.Context(), slot); err != nil {
			t.Fatalf("start %s: %v", slot, err)
		}
	}
	if err := h.provider.Close(); err != nil {
		t.Fatal(err)
	}
	for _, slot := range vm.Slots() {
		h.waitEvent(slot, "lifeline-closed")
		waitStopped(t, m, slot)
	}
	if _, err := h.provider.Open(t.Context(), vm.SlotWork, 9); err == nil {
		t.Error("Open after Close succeeded")
	}
	if n := strings.Count(h.log.String(), `"msg":"wb-vmd started"`); n != len(vm.Slots()) {
		t.Errorf("%d wb-vmd started, want %d: Open after Close spawned one", n, len(vm.Slots()))
	}
}

// A wb-vmd whose state stream ends while it runs is ended, with the
// rule logged, and its VM is stopped: an instance without its states is
// never kept.
func TestStreamEndedEndsVMD(t *testing.T) {
	h := newHarness(t)
	h.cfg.Resources.MemoryMiB = vmdfake.EndStreamsMemoryMiB
	m := h.manager()
	_ = m.Start(t.Context(), vm.SlotWork)
	waitStopped(t, m, vm.SlotWork)
	h.waitEvent(vm.SlotWork, "lifeline-closed")
	if !strings.Contains(h.log.String(), `"rule":"vmd-state-stream-ended"`) {
		t.Errorf("no vmd-state-stream-ended in wb-hostd's log:\n%s", h.log.String())
	}
	// The fake's 4 KiB status message is cut to the provider text bound.
	if !strings.Contains(h.log.String(), "[cut]") || strings.Contains(h.log.String(), strings.Repeat("x", 2048)) {
		t.Errorf("the stream's error text isn't cut in wb-hostd's log:\n%s", h.log.String())
	}
}

// A wb-vmd whose VM fails sends ERROR with the framework's error, then
// exits. The manager gets that last state, so the end of the run is
// recorded with the error, and wb-hostd doesn't log that it ended
// wb-vmd.
func TestCrashKeepsLastError(t *testing.T) {
	h := newHarness(t)
	h.cfg.Resources.MemoryMiB = vmdfake.CrashMemoryMiB
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitStopped(t, m, vm.SlotWork)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.log.String(), `"msg":"vm stopped"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no vm stopped record:\n%s", h.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(h.log.String(), `"failure_reason":"`+vmdfake.CrashReason+`"`) {
		t.Errorf("the run's end has no framework error:\n%s", h.log.String())
	}
	if strings.Contains(h.log.String(), "vmd-state-stream-ended") {
		t.Errorf("wb-hostd logged that it ended a wb-vmd that had exited:\n%s", h.log.String())
	}
}

// ClearLeftovers removes the sockets a crashed wb-hostd left, and
// leaves every other file: one of another name, and one of a socket's
// name that isn't a socket, which it logs with its rule.
func TestClearLeftovers(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"vmd-work-3.sock", "vmd-isolated-7.sock", "wb-hostd.sock"} {
		leftover(t, filepath.Join(h.runDir, name))
	}
	notSocket := filepath.Join(h.runDir, "vmd-work-9.sock")
	if err := os.Symlink(filepath.Join(h.runDir, "elsewhere"), notSocket); err != nil {
		t.Fatal(err)
	}
	n, err := h.provider.ClearLeftovers(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("ClearLeftovers = %d, %v; want 2 removed", n, err)
	}
	for _, name := range []string{"vmd-work-3.sock", "vmd-isolated-7.sock"} {
		if _, err := os.Lstat(filepath.Join(h.runDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", name, err)
		}
	}
	for _, name := range []string{"wb-hostd.sock", "vmd-work-9.sock"} {
		if _, err := os.Lstat(filepath.Join(h.runDir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	if !strings.Contains(h.log.String(), `"rule":"ipc-endpoint-unsafe"`) {
		t.Errorf("the link isn't logged with its rule:\n%s", h.log.String())
	}
}

// leftover leaves a socket file with no listener, as a crash does.
func leftover(t *testing.T, path string) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
}

// A wb-vmd of another build version is refused, and Open says so.
func TestVersionMismatchRefused(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := os.MkdirTemp("", "vp") //nolint:usetesting // sun_path length and the 0700 mode, see newHarness
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })
	files := logpipe.NewFiles(t.TempDir(), nil)
	t.Cleanup(func() { _ = files.Close() })
	p, err := vmdprovider.New(vmdprovider.Config{Program: exe, RunDir: runDir, Version: "9.9.9", Logs: files.Writer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(t.Context(), vm.SlotWork, 1); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v, want a version refusal", err)
	}
}
