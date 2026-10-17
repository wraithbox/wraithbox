package vm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// A wb-vmd that never answers a stop doesn't hold the slot in stopping:
// each deadline covers the provider's call, and the run ends with the
// instance closed.
func TestStopBoundsAProviderThatHangs(t *testing.T) {
	h := newHarness(t)
	h.cfg.RequestStopTimeout = 10 * time.Millisecond
	h.cfg.ForceStopTimeout = 10 * time.Millisecond
	h.provider.OnStop = func(ctx context.Context, _ *vmtest.Instance, _ *vmdv1.StopRequest) error {
		<-ctx.Done()
		return ctx.Err()
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	inst := h.provider.Last()
	if !inst.Closed() || inst.Closes() != 1 {
		t.Errorf("closed = %v after %d closes, want closed once", inst.Closed(), inst.Closes())
	}
	if len(inst.Stops()) != 2 {
		t.Errorf("stops = %v, want request and force", inst.Stops())
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// A wb-vmd that never answers a start is refused with vm-start-timeout,
// and the run ends with the instance closed.
func TestStartBoundsAProviderThatHangs(t *testing.T) {
	h := newHarness(t)
	h.cfg.StartTimeout = 10 * time.Millisecond
	h.provider.OnStart = func(ctx context.Context, _ *vmtest.Instance, _ *vmdv1.StartRequest) error {
		<-ctx.Done()
		return ctx.Err()
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleStartTimeout)
	if inst := h.provider.Last(); !inst.Closed() {
		t.Error("the instance isn't closed")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// A wb-vmd that doesn't open within the deadline refuses the start with
// vm-start-timeout. The open gets a context that isn't cancelled, and an
// instance that opens late is closed at once.
func TestOpenBoundsAProviderThatHangs(t *testing.T) {
	h := newHarness(t)
	h.cfg.StartTimeout = 10 * time.Millisecond
	release := make(chan struct{})
	h.provider.OnOpen = func(ctx context.Context, _ vm.Slot) error {
		<-release
		return ctx.Err()
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleStartTimeout)
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if inst := h.provider.Last(); inst != nil && inst.Closed() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the instance that opened late isn't closed")
		}
		time.Sleep(time.Millisecond)
	}
	if n := len(h.provider.Last().Starts()); n != 0 {
		t.Errorf("the late instance got %d starts", n)
	}
}

// While a stop runs, a second stop is refused with vm-stopping, a start
// with vm-running, and a connection with vm-stopping.
func TestRequestsWhileStopping(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	h.provider.OnStop = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StopRequest) error {
		close(entered)
		<-release
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_STOPPED, nil)
		return nil
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- m.Stop(context.Background(), vm.SlotWork) }()
	<-entered
	refusal(t, m.Stop(t.Context(), vm.SlotWork), vm.RuleStopping)
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleRunning)
	_, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1023)
	refusal(t, err, vm.RuleStopping)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(h.provider.Instances()); n != 1 {
		t.Errorf("%d instances, want the refused start to open none", n)
	}
}

// Of many starts of one slot at once, exactly one opens an instance.
func TestConcurrentStartsOpenOne(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { errs <- m.Start(context.Background(), vm.SlotWork) })
	}
	wg.Wait()
	close(errs)
	started := 0
	for err := range errs {
		if err == nil {
			started++
			continue
		}
		refusal(t, err, vm.RuleRunning)
	}
	if started != 1 || len(h.provider.Instances()) != 1 {
		t.Errorf("%d starts succeeded with %d instances, want 1 and 1", started, len(h.provider.Instances()))
	}
}

// A stop that races wb-vmd's exit closes the instance once, and the slot
// ends stopped.
func TestStopRacesExit(t *testing.T) {
	h := newHarness(t)
	h.cfg.RequestStopTimeout = 10 * time.Millisecond
	h.cfg.ForceStopTimeout = 10 * time.Millisecond
	m := h.manager()
	for range 20 {
		if err := m.Start(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
		inst := h.provider.Last()
		done := make(chan error)
		go func() { done <- m.Stop(context.Background(), vm.SlotWork) }()
		inst.Exit()
		if err := <-done; err != nil {
			refusal(t, err, vm.RuleNotRunning)
		}
		waitStopped(t, m, vm.SlotWork)
		if c := inst.Closes(); c != 1 {
			t.Fatalf("Close ran %d times", c)
		}
	}
}

// After a restart, a stop and a connection reach the new instance, and
// the old one gets neither.
func TestOldInstanceAfterRestart(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	old := h.provider.Last()
	stops := len(old.Stops())
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	conn, err := m.ConnectGuest(t.Context(), vm.SlotWork, 1023)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if len(old.Stops()) != stops || len(old.Connects()) != 0 {
		t.Errorf("the old instance got %d stops and %d connects after the restart",
			len(old.Stops())-stops, len(old.Connects()))
	}
	current := h.provider.Last()
	if current == old || len(current.Stops()) != 1 || len(current.Connects()) != 1 {
		t.Errorf("the new instance got %v and %v", current.Stops(), current.Connects())
	}
}

// A bundle directory that can't be created refuses the start with
// vm-bundle-create-failed.
func TestBundleCreateFailed(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.dataDir, "vms"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleBundleCreateFailed)
	if h.cloner.Calls() != 0 || len(h.provider.Instances()) != 0 {
		t.Error("a step after the bundle ran")
	}
}

// A leftover temporary name that can't be deleted refuses the start, and
// the refusal says to move it away.
func TestLeftoverTempNotDeletable(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.bundle(vm.SlotWork), "tools.img.partial", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleToolsDiskCloneFailed)
	if !strings.Contains(r.Message, "move it away") {
		t.Errorf("message %q doesn't say to move it away", r.Message)
	}
}

// Text from the provider is cut to its bound in the refusal and in the
// slot's last error.
func TestProviderTextBounded(t *testing.T) {
	h := newHarness(t)
	long := strings.Repeat("x", 10_000)
	h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error {
		return &vm.ProviderRefusal{
			Rule: "vm-start-failed", Detail: long,
			VMError: &vmdv1.VMError{Code: 2, FailureReason: long, Description: long},
		}
	}
	m := h.manager()
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), "vm-start-failed")
	if len(r.Message) > 1200 {
		t.Errorf("message is %d bytes", len(r.Message))
	}
	e := m.Status(vm.SlotWork).LastError
	if len(e.GetDescription()) > 1100 || len(e.GetFailureReason()) > 1100 {
		t.Errorf("last error is %d and %d bytes", len(e.GetDescription()), len(e.GetFailureReason()))
	}
	if errors.Unwrap(r) == nil {
		t.Error("the refusal lost its cause")
	}
}
