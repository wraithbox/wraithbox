package vm_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// harness is a manager on a temporary <data>, with the fake provider,
// the copying cloner, and a log buffer.
type harness struct {
	t        *testing.T
	dataDir  string
	tools    string
	db       *state.DB
	provider *vmtest.Provider
	cloner   *vmtest.CopyCloner
	shim     *datadisktest.Shim
	logs     *syncBuffer
	cfg      vm.Config
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t: t, dataDir: t.TempDir(), provider: &vmtest.Provider{},
		cloner: &vmtest.CopyCloner{}, logs: &syncBuffer{},
	}
	h.tools = filepath.Join(t.TempDir(), "wb-guest-tools.img")
	h.writeTools("tools v1")
	db, err := state.Open(t.Context(), h.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h.db = db
	logger := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h.shim = &datadisktest.Shim{DataDir: h.dataDir, UUIDs: []string{uuid1, uuid2}}
	disks, err := datadisk.New(datadisk.Config{DataDir: h.dataDir, Records: db, Shim: h.shim, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg = vm.Config{
		DataDir: h.dataDir, ToolsImage: h.tools, Resources: config.Defaults(),
		Generations: db, Provider: h.provider, Cloner: h.cloner, DataDisks: disks,
		Logger: logger,
	}
	return h
}

func (h *harness) writeTools(content string) {
	h.t.Helper()
	if err := os.WriteFile(h.tools, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) manager() *vm.Manager {
	h.t.Helper()
	m, err := vm.New(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) bundle(slot vm.Slot) string {
	return filepath.Join(h.dataDir, "vms", "macos-"+string(slot)+".noindex")
}

// refusal checks that err is a *vm.Refusal with rule.
func refusal(t *testing.T, err error, rule vm.Rule) *vm.Refusal {
	t.Helper()
	r, ok := errors.AsType[*vm.Refusal](err)
	if !ok {
		t.Fatalf("err = %v, want a refusal with %s", err, rule)
	}
	if r.Rule != rule {
		t.Fatalf("rule = %s (%s), want %s", r.Rule, r.Message, rule)
	}
	return r
}

// waitStopped waits for slot to stop, which a state from the provider
// does on another goroutine.
func waitStopped(t *testing.T, m *vm.Manager, slot vm.Slot) vm.Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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

// A new manager has every slot stopped, and starts no VM
// (S04-07-supervision, "A new `wb-hostd`").
func TestNewStartsNothing(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	for _, s := range m.Statuses() {
		if s.Phase != vm.PhaseStopped || s.Generation != 0 {
			t.Errorf("%s = %+v, want stopped", s.Slot, s)
		}
	}
	if n := len(h.provider.Instances()); n != 0 {
		t.Errorf("%d instances opened", n)
	}
}

func TestNewChecksConfig(t *testing.T) {
	h := newHarness(t)
	tests := map[string]func(*vm.Config){
		"relative data":  func(c *vm.Config) { c.DataDir = "data" },
		"relative tools": func(c *vm.Config) { c.ToolsImage = "tools.img" },
		"no provider":    func(c *vm.Config) { c.Provider = nil },
		"no cloner":      func(c *vm.Config) { c.Cloner = nil },
		"no generations": func(c *vm.Config) { c.Generations = nil },
		"no data disks":  func(c *vm.Config) { c.DataDisks = nil },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := h.cfg
			change(&cfg)
			if _, err := vm.New(cfg); err == nil {
				t.Error("New accepted the config")
			}
		})
	}
}

// startAndStop starts and stops the first run of slot, and checks the
// provider saw a start and a request stop of generation 1, and was
// closed.
func startAndStop(t *testing.T, h *harness, m *vm.Manager, slot vm.Slot) {
	t.Helper()
	if err := m.Start(t.Context(), slot); err != nil {
		t.Fatalf("start %s: %v", slot, err)
	}
	inst := h.provider.Last()
	if inst.Slot() != slot {
		t.Errorf("instance slot = %s, want %s", inst.Slot(), slot)
	}
	if s := m.Status(slot); s.Phase != vm.PhaseRunning || s.Generation != 1 {
		t.Errorf("%s after start = %+v", slot, s)
	}
	if err := m.Stop(t.Context(), slot); err != nil {
		t.Fatalf("stop %s: %v", slot, err)
	}
	if s := m.Status(slot); s.Phase != vm.PhaseStopped || s.Generation != 1 {
		t.Errorf("%s after stop = %+v", slot, s)
	}
	stops := inst.Stops()
	if len(stops) != 1 || stops[0].GetMode() != vmdv1.StopMode_STOP_MODE_REQUEST || stops[0].GetGeneration() != 1 {
		t.Errorf("stops = %v, want one request stop of generation 1", stops)
	}
	if !inst.Closed() {
		t.Error("the instance isn't closed after the stop")
	}
}

// C11-host-daemons, "VM manager": a start and a stop of each slot.
func TestC11StartAndStopEachSlot(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	for _, slot := range vm.Slots() {
		t.Run(string(slot), func(t *testing.T) { startAndStop(t, h, m, slot) })
	}
	if n := len(h.provider.Instances()); n != 2 {
		t.Errorf("%d instances, want one per run", n)
	}
	// The next start of a slot gets a new instance and a new generation,
	// which StartRun returns.
	started, err := m.StartRun(t.Context(), vm.SlotWork)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.provider.Instances()); n != 3 {
		t.Errorf("%d instances, want a new one for the new run", n)
	}
	if g := m.Status(vm.SlotWork).Generation; g != 2 || started != 2 {
		t.Errorf("generation = %d, StartRun returned %d, want 2", g, started)
	}
	if g, err := m.StartRun(t.Context(), vm.SlotWork); g != 0 || err == nil {
		t.Errorf("a refused StartRun returned %d, %v", g, err)
	}
}

// C11-host-daemons, "VM manager": a second start is refused with
// vm-running, and a stop of a stopped VM with vm-not-running.
func TestC11RunningAndNotRunning(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.RuleNotRunning)
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleRunning)
	if r.Phase != vm.PhaseRunning || r.Generation != 1 {
		t.Errorf("refusal = %+v", r)
	}
	if n := len(h.provider.Instances()); n != 1 {
		t.Errorf("%d instances, want the refused start to open none", n)
	}
	if !strings.Contains(h.logs.String(), `"rule":"vm-running"`) {
		t.Errorf("the refusal isn't logged with its rule:\n%s", h.logs)
	}
}

// A start while the slot is starting is refused too.
func TestStartWhileStarting(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	h.provider.OnStart = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StartRequest) error {
		close(entered)
		<-release
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_RUNNING, nil)
		return nil
	}
	m := h.manager()
	done := make(chan error)
	go func() { done <- m.Start(context.Background(), vm.SlotWork) }()
	<-entered
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleRunning)
	refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.RuleStarting)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// C11-host-daemons, "VM manager": refusing mode refuses a start with its
// rule, before any step.
func TestC11RefusingMode(t *testing.T) {
	h := newHarness(t)
	h.cfg.Generations = nil
	h.cfg.Refusing = &startup.Refusing{Rule: string(state.RuleNewer), Detail: "the state database is version 9"}
	m := h.manager()
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.Rule(state.RuleNewer))
	if !strings.Contains(r.Message, "version 9") {
		t.Errorf("message %q doesn't hold the detail", r.Message)
	}
	refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.Rule(state.RuleNewer))
	if _, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023); err == nil {
		t.Error("connect accepted in refusing mode")
	}
	if len(h.provider.Instances()) != 0 || h.cloner.Calls() != 0 {
		t.Error("refusing mode ran a step of the start")
	}
	if _, err := os.Lstat(h.bundle(vm.SlotWork)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refusing mode created the bundle: %v", err)
	}
}

func TestUnknownSlot(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.Slot("build")), vm.RuleSlotUnknown)
	refusal(t, m.Stop(t.Context(), vm.Slot("../work")), vm.RuleSlotUnknown)
}

// lastGeneration reads the slot's committed generation on a connection
// of its own.
func lastGeneration(t *testing.T, dataDir, slot string) uint64 {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, state.FileName))
	if err != nil {
		t.Error(err)
		return 0
	}
	defer func() { _ = db.Close() }()
	var g uint64
	if err := db.QueryRowContext(context.Background(), "SELECT last_generation FROM vm_slots WHERE slot = ?", slot).Scan(&g); err != nil {
		t.Error(err)
	}
	return g
}

// C11-host-daemons, "VM manager": two managers on one state.db give
// strictly increasing generations, and the generation is committed
// before the provider sees it.
func TestC11GenerationsIncreaseAcrossManagers(t *testing.T) {
	h := newHarness(t)
	var seen []uint64
	var mu sync.Mutex
	h.provider.OnStart = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StartRequest) error {
		if c := lastGeneration(t, h.dataDir, "macos-work"); c != req.GetGeneration() {
			t.Errorf("state.db has %d when the provider sees %d", c, req.GetGeneration())
		}
		mu.Lock()
		seen = append(seen, req.GetGeneration())
		mu.Unlock()
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_RUNNING, nil)
		return nil
	}
	first := h.manager()
	db2, err := state.Open(t.Context(), h.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	cfg2 := h.cfg
	cfg2.Generations = db2
	second, err := vm.New(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*vm.Manager{first, second, first, second} {
		if err := m.Start(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
		if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("generations %v don't strictly increase", seen)
		}
	}
	if len(seen) != 4 || seen[0] != 1 {
		t.Errorf("generations = %v, want 4 starting at 1", seen)
	}
}

// Generation 0 from the state is refused before the provider sees it,
// and Stop and ConnectGuest refuse a run of generation 0.
func TestC11GenerationZeroRefused(t *testing.T) {
	h := newHarness(t)
	h.cfg.Generations = zeroGenerations{}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleGenerationInvalid)
	if len(h.provider.Instances()) != 0 {
		t.Error("an instance was opened for generation 0")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s after a refused start", s.Phase)
	}

	h = newHarness(t)
	m = h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	vm.SetGeneration(m, vm.SlotWork, 0)
	refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.RuleGenerationInvalid)
	_, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023)
	refusal(t, err, vm.RuleGenerationInvalid)
	inst := h.provider.Last()
	if len(inst.Stops()) != 0 || len(inst.Connects()) != 0 {
		t.Error("the provider saw generation 0")
	}
}

type zeroGenerations struct{}

func (zeroGenerations) NextGeneration(context.Context, string) (uint64, error) { return 0, nil }

type failingGenerations struct{}

func (failingGenerations) NextGeneration(context.Context, string) (uint64, error) {
	return 0, errors.New("disk I/O error")
}

func TestGenerationFailureRefused(t *testing.T) {
	h := newHarness(t)
	h.cfg.Generations = failingGenerations{}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleGenerationFailed)
	if len(h.provider.Instances()) != 0 {
		t.Error("an instance was opened without a generation")
	}
}

// C11-host-daemons, "VM manager": a provider refusal is passed on with
// its rule. A rule that isn't a rule name, and an error without one, are
// vm-provider-failed.
func TestC11ProviderRefusalPassedOn(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want vm.Rule
	}{
		{"wb-vmd rule", &vm.ProviderRefusal{Rule: "vm-resources", Detail: "cpu_count 99"}, "vm-resources"},
		{"device set rule", &vm.ProviderRefusal{Rule: "device-set-file-outside-bundle"}, "device-set-file-outside-bundle"},
		{"not a rule name", &vm.ProviderRefusal{Rule: "Vm Resources\n"}, vm.RuleProviderFailed},
		{"rule too long", &vm.ProviderRefusal{Rule: strings.Repeat("a", 65)}, vm.RuleProviderFailed},
		{"transport error", errors.New("connection reset"), vm.RuleProviderFailed},
		{"the manager's own rule", &vm.ProviderRefusal{Rule: "vm-limit-other-software"}, vm.RuleProviderFailed},
		{"the manager's running rule", &vm.ProviderRefusal{Rule: "vm-running"}, vm.RuleProviderFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error { return tt.err }
			m := h.manager()
			r := refusal(t, m.Start(t.Context(), vm.SlotWork), tt.want)
			if r.Generation != 1 {
				t.Errorf("generation = %d", r.Generation)
			}
			if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
				t.Errorf("phase = %s after a refused start", s.Phase)
			}
			if !h.provider.Last().Closed() {
				t.Error("the instance of the failed start isn't closed")
			}
		})
	}
}

func TestOpenFailureRefused(t *testing.T) {
	h := newHarness(t)
	h.provider.OpenErr = &vm.ProviderRefusal{Rule: "vmd-slot-bundle-mismatch"}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), "vmd-slot-bundle-mismatch")
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// C11-host-daemons, "VM manager": the framework's limit of running VMs
// is refused with vm-limit-other-software, naming the running VM, with
// no retry, and no VM of Wraith Box's stopped (S06-07-slot-admission,
// "Other software").
func TestC11LimitOtherSoftware(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotIsolated); err != nil {
		t.Fatal(err)
	}
	limit := &vmdv1.VMError{Code: 6, Description: "The number of virtual machines exceeds the limit."}
	h.provider.OnStart = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StartRequest) error {
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_ERROR, limit)
		return &vm.ProviderRefusal{Rule: "vm-start-failed", Detail: limit.GetDescription(), VMError: limit}
	}
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleLimitOtherSoftware)
	if len(r.Running) != 1 || r.Running[0].Slot != vm.SlotIsolated {
		t.Errorf("running = %+v, want the isolated VM", r.Running)
	}
	for _, want := range []string{"outside Wraith Box", "the isolated VM (running, generation 1)", "Quit the other VM"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message %q doesn't say %q", r.Message, want)
		}
	}
	if r.VMError.GetCode() != 6 {
		t.Errorf("VMError = %v", r.VMError)
	}
	var work []*vmtest.Instance
	for _, i := range h.provider.Instances() {
		if i.Slot() == vm.SlotWork {
			work = append(work, i)
		}
	}
	if len(work) != 1 || len(work[0].Starts()) != 1 {
		t.Errorf("the work VM was started %d times in %d instances, want once", len(work[0].Starts()), len(work))
	}
	if s := m.Status(vm.SlotIsolated); s.Phase != vm.PhaseRunning {
		t.Errorf("the isolated VM is %s, want it left running", s.Phase)
	}
	if s := m.Status(vm.SlotWork); s.LastError.GetCode() != 6 {
		t.Errorf("the work VM's last error = %v, want the limit", s.LastError)
	}
	if !strings.Contains(h.logs.String(), `"classified":"vm-limit-other-software"`) {
		t.Errorf("the error isn't logged with its classification:\n%s", h.logs)
	}
}

// With no VM of Wraith Box's running, the refusal says so.
func TestLimitWithNothingRunning(t *testing.T) {
	h := newHarness(t)
	h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error {
		return &vm.ProviderRefusal{Rule: "vm-start-failed", VMError: &vmdv1.VMError{Code: 6}}
	}
	m := h.manager()
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleLimitOtherSoftware)
	if len(r.Running) != 0 || !strings.Contains(r.Message, "runs no other VM") {
		t.Errorf("refusal = %+v", r)
	}
}

// C11-host-daemons, "VM manager": a VM that stops on its own reaches
// stopped, with its error kept, and is logged with
// vm-stopped-unexpectedly.
func TestC11StoppedUnexpectedly(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	inst := h.provider.Last()
	vmErr := &vmdv1.VMError{Code: 1, FailureReason: "internal", Errno: 5, Description: "The virtual machine stopped unexpectedly."}
	// A late state of another generation changes nothing.
	inst.Publish(7, vmdv1.State_STATE_STOPPED, nil)
	inst.Publish(1, vmdv1.State_STATE_ERROR, vmErr)
	s := waitStopped(t, m, vm.SlotWork)
	if e := s.LastError; e.GetCode() != 1 || e.GetFailureReason() != "internal" || e.GetErrno() != 5 || e.GetDescription() != vmErr.GetDescription() {
		t.Errorf("last error = %v, want %v", e, vmErr)
	}
	if !inst.Closed() {
		t.Error("the instance isn't closed")
	}
	if len(inst.Stops()) != 0 {
		t.Error("the manager asked for a stop")
	}
	if len(h.provider.Instances()) != 1 {
		t.Error("the manager started the VM again by itself")
	}
	logs := h.logs.String()
	if !strings.Contains(logs, `"rule":"vm-stopped-unexpectedly"`) || !strings.Contains(logs, `"errno":5`) {
		t.Errorf("the stop isn't logged with its rule and error:\n%s", logs)
	}
}

// A wb-vmd that exits means its VM stopped.
func TestInstanceExitStopsSlot(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotIsolated); err != nil {
		t.Fatal(err)
	}
	h.provider.Last().Exit()
	waitStopped(t, m, vm.SlotIsolated)
	if err := m.Start(t.Context(), vm.SlotIsolated); err != nil {
		t.Fatalf("start after the exit: %v", err)
	}
}

// Wait returns once the end of every run whose instance closed is
// logged, as wb-hostd needs before it closes its log.
func TestWaitForRunEnds(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	for _, slot := range vm.Slots() {
		if err := m.Start(t.Context(), slot); err != nil {
			t.Fatal(err)
		}
	}
	for _, i := range h.provider.Instances() {
		i.Exit()
	}
	m.Wait()
	if n := strings.Count(h.logs.String(), `"msg":"vm stopped"`); n != len(vm.Slots()) {
		t.Errorf("%d vm stopped records after Wait, want %d:\n%s", n, len(vm.Slots()), h.logs.String())
	}
}

// A VM that stops between the provider's start and its answer ends as
// stopped.
func TestStoppedDuringStart(t *testing.T) {
	h := newHarness(t)
	h.provider.OnStart = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StartRequest) error {
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_RUNNING, nil)
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_STOPPED, nil)
		return nil
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, m, vm.SlotWork)
}

// C11-host-daemons, "VM manager": a request stop that doesn't finish in
// time goes to a force stop.
func TestC11RequestStopTimesOutToForce(t *testing.T) {
	h := newHarness(t)
	h.cfg.RequestStopTimeout = 10 * time.Millisecond
	h.provider.OnStop = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StopRequest) error {
		if req.GetMode() == vmdv1.StopMode_STOP_MODE_FORCE {
			i.Publish(req.GetGeneration(), vmdv1.State_STATE_STOPPED, nil)
		}
		return nil
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	stops := h.provider.Last().Stops()
	if len(stops) != 2 || stops[0].GetMode() != vmdv1.StopMode_STOP_MODE_REQUEST || stops[1].GetMode() != vmdv1.StopMode_STOP_MODE_FORCE {
		t.Errorf("stops = %v, want request then force", stops)
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// A VM that neither stop stops has its instance closed, which stops it
// as a lost wb-hostd does. A failing stop call goes to the next step at
// once.
func TestStopClosesInstanceAfterForce(t *testing.T) {
	h := newHarness(t)
	h.cfg.RequestStopTimeout = time.Hour
	h.cfg.ForceStopTimeout = 10 * time.Millisecond
	h.provider.OnStop = func(_ context.Context, _ *vmtest.Instance, req *vmdv1.StopRequest) error {
		if req.GetMode() == vmdv1.StopMode_STOP_MODE_REQUEST {
			return &vm.ProviderRefusal{Rule: "vm-stop-failed"}
		}
		return nil
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if !h.provider.Last().Closed() || m.Status(vm.SlotWork).Phase != vm.PhaseStopped {
		t.Error("the VM isn't stopped with its instance closed")
	}
	if len(h.provider.Last().Stops()) != 2 {
		t.Errorf("stops = %v", h.provider.Last().Stops())
	}
}

// C11-host-daemons, "VM manager": the configured resources reach the
// provider (SEC13-bounded-resources).
func TestC11ResourcesReachProvider(t *testing.T) {
	h := newHarness(t)
	h.cfg.Resources = config.Config{CPUCount: 6, MemoryMiB: 12288}
	m := h.manager()
	for _, slot := range vm.Slots() {
		if err := m.Start(t.Context(), slot); err != nil {
			t.Fatal(err)
		}
		starts := h.provider.Last().Starts()
		if len(starts) != 1 || starts[0].GetCpuCount() != 6 || starts[0].GetMemoryMib() != 12288 || starts[0].GetGeneration() != 1 {
			t.Errorf("%s start = %v", slot, starts)
		}
	}
}

// C11-host-daemons, "VM manager": the stop refusal with a fake session
// lists it with its state, its wb's process ID and how it ends
// (S06-09-session-lifecycle, "Idle suspend").
func TestC11StopRefusedWithSessions(t *testing.T) {
	h := newHarness(t)
	h.cfg.Sessions = func(slot vm.Slot) []vm.Session {
		if slot != vm.SlotWork {
			return nil
		}
		return []vm.Session{
			{ID: "s-1", State: vm.SessionRunning, PID: 4242},
			{ID: "s-2", State: vm.SessionEnding, PID: 4343},
		}
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	r := refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.RuleLiveSessions)
	for _, want := range []string{
		"session s-1 is running (wb process 4242): close its terminal",
		"session s-2 is ending (wb process 4343): wait for its deadline",
		"wb sessions",
	} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message %q doesn't say %q", r.Message, want)
		}
	}
	if len(r.Sessions) != 2 {
		t.Errorf("sessions = %v", r.Sessions)
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseRunning {
		t.Errorf("phase = %s, want the VM left running", s.Phase)
	}
	if len(h.provider.Last().Stops()) != 0 {
		t.Error("the provider was asked to stop")
	}
}

func TestHowItEnds(t *testing.T) {
	tests := map[vm.SessionState]string{
		vm.SessionStarting: "wait for its deadline",
		vm.SessionRunning:  "close its terminal",
		vm.SessionPaused:   "close its terminal",
		vm.SessionEnding:   "wait for its deadline",
	}
	for state, want := range tests {
		if got := (vm.Session{State: state}).HowItEnds(); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}
}

// ConnectGuest reaches the provider with the current generation, only
// for a running VM and a guest port below 1024 (S04-02-vm-provider,
// "Guest ports below 1024").
func TestConnectGuest(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	_, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023)
	refusal(t, err, vm.RuleNotRunning)
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	for _, port := range []uint32{0, 1024, 65535} {
		_, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1, port)
		refusal(t, err, vm.RuleGuestPort)
	}
	for _, g := range []uint64{2, 99} {
		_, err := m.ConnectGuest(t.Context(), vm.SlotWork, g, 1023)
		refusal(t, err, vm.RuleGenerationStale)
	}
	conn, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if got := h.provider.Last().Connects(); len(got) != 1 || got[0] != (vmtest.Connect{Generation: 1, Port: 1023}) {
		t.Errorf("connects = %v", got)
	}
	h.provider.Last().Exit()
	waitStopped(t, m, vm.SlotWork)
	_, err = m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023)
	refusal(t, err, vm.RuleNotRunning)
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	_, err = m.ConnectGuest(t.Context(), vm.SlotWork, 1, 1023)
	refusal(t, err, vm.RuleGenerationStale)
}

// OnRunning is called once for each run that reaches running, with its
// generation, and its channel closes once the run has ended.
// OnEnded is called once for each run that ended, with its generation
// and why it ended: the kill tracker closes the run's pending kills
// through it (S06-10-leftover-processes, "Failing closed").
func TestOnEnded(t *testing.T) {
	h := newHarness(t)
	type call struct {
		slot       vm.Slot
		generation uint64
		cause      string
	}
	calls := make(chan call, 4)
	h.cfg.OnEnded = func(slot vm.Slot, generation uint64, cause string) {
		calls <- call{slot, generation, cause}
	}
	m := h.manager()
	next := func() call {
		t.Helper()
		select {
		case c := <-calls:
			return c
		case <-time.After(5 * time.Second):
			t.Fatal("OnEnded wasn't called")
			return call{}
		}
	}
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if c := next(); c != (call{vm.SlotWork, 1, "stop requested"}) {
		t.Fatalf("OnEnded%+v", c)
	}
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.provider.Last().Publish(2, vmdv1.State_STATE_STOPPED, nil)
	if c := next(); c != (call{vm.SlotWork, 2, "stopped unexpectedly"}) {
		t.Fatalf("OnEnded%+v", c)
	}
	select {
	case c := <-calls:
		t.Fatalf("OnEnded%+v called again", c)
	default:
	}
}

func TestOnRunning(t *testing.T) {
	h := newHarness(t)
	type call struct {
		slot       vm.Slot
		generation uint64
		ended      <-chan struct{}
	}
	calls := make(chan call, 4)
	h.cfg.OnRunning = func(slot vm.Slot, generation uint64, ended <-chan struct{}) {
		calls <- call{slot, generation, ended}
	}
	m := h.manager()
	for want := uint64(1); want <= 2; want++ {
		if err := m.Start(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
		c := <-calls
		if c.slot != vm.SlotWork || c.generation != want {
			t.Fatalf("OnRunning(%s, %d), want (%s, %d)", c.slot, c.generation, vm.SlotWork, want)
		}
		select {
		case <-c.ended:
			t.Fatal("ended is closed while the VM runs")
		default:
		}
		if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
		select {
		case <-c.ended:
		case <-time.After(5 * time.Second):
			t.Fatal("ended isn't closed after the stop")
		}
	}
	h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error {
		return &vm.ProviderRefusal{Rule: "vm-config", Detail: "refused"}
	}
	_ = m.Start(t.Context(), vm.SlotWork)
	select {
	case c := <-calls:
		t.Fatalf("OnRunning(%s, %d) for a failed start", c.slot, c.generation)
	default:
	}
}
