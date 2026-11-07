package vm_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// fakeNetwork is a vm.Network whose runs record their stops.
type fakeNetwork struct {
	mu sync.Mutex
	// prepareErr fails Prepare, and err fails Start.
	prepareErr error
	err        error
	runs       []*fakeNetworkRun
	macs       [][]byte
	// prepares counts the Prepare calls, and starts the provider's
	// starts that came before each.
	prepares int
	starts   []int
	provider *vmtest.Provider
	// exitAtStart: each run exits before Start returns.
	exitAtStart bool
}

func (n *fakeNetwork) Prepare(_ context.Context, _ vm.Slot, _ uint64, mac []byte) (vm.NetworkStart, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prepares++
	if n.provider != nil {
		if inst := n.provider.Instances(); len(inst) > 0 {
			n.starts = append(n.starts, len(inst[len(inst)-1].Starts()))
		}
	}
	if n.prepareErr != nil {
		return nil, n.prepareErr
	}
	return fakeStart{n: n, mac: mac}, nil
}

type fakeStart struct {
	n   *fakeNetwork
	mac []byte
}

func (s fakeStart) Start(_ context.Context, t vm.PacketTransport) (vm.NetworkRun, error) {
	n := s.n
	n.mu.Lock()
	defer n.mu.Unlock()
	n.macs = append(n.macs, s.mac)
	if n.err != nil {
		return nil, n.err
	}
	tr, _ := t.(*vmtest.Transport)
	r := &fakeNetworkRun{exited: make(chan struct{}), transport: tr}
	n.runs = append(n.runs, r)
	if n.exitAtStart {
		r.exit()
	}
	return r, nil
}

func (n *fakeNetwork) last(t *testing.T) *fakeNetworkRun {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.runs) == 0 {
		t.Fatal("no wb-netd started")
	}
	return n.runs[len(n.runs)-1]
}

type fakeNetworkRun struct {
	exited    chan struct{}
	exitOnce  sync.Once
	transport *vmtest.Transport

	mu    sync.Mutex
	stops int
	// closesAtStop is the transport's closes when Stop was called.
	closesAtStop int
}

func (r *fakeNetworkRun) Exited() <-chan struct{} { return r.exited }

func (r *fakeNetworkRun) exit() { r.exitOnce.Do(func() { close(r.exited) }) }

func (r *fakeNetworkRun) Stop() []audit.OpenRun {
	r.mu.Lock()
	r.stops++
	if r.transport != nil {
		r.closesAtStop = r.transport.Closes()
	}
	r.mu.Unlock()
	r.exit()
	return []audit.OpenRun{{Sender: "wb-netd", Rule: "dns-rate", Decision: "refuse", Count: 7}}
}

func (r *fakeNetworkRun) stopped() (stops, closesAtStop int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops, r.closesAtStop
}

// TestC11NetdStartsAndStopsWithTheRun is C11-host-daemons, "VM
// manager": each run's wb-netd starts with the guest's MAC address once
// the transport came, and at the stop it is stopped before the
// transport is closed, its open runs in the stop record
// (S04-09-netd-start, "No restart yet").
func TestC11NetdStartsAndStopsWithTheRun(t *testing.T) {
	h := newHarness(t)
	network := &fakeNetwork{}
	h.cfg.Network = network
	m := h.manager()
	startAndStop(t, h, m, vm.SlotWork)
	waitStopRecord(t, h)
	r := network.last(t)
	if stops, closes := r.stopped(); stops != 1 || closes != 0 {
		t.Fatalf("wb-netd stopped %d times, the transport closed %d times before; want 1, 0", stops, closes)
	}
	want, err := h.db.MACAddress(t.Context(), "macos-work")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(network.macs[0], want) {
		t.Errorf("wb-netd got MAC %x, want %x", network.macs[0], want)
	}
	runs := h.audit.Find("vm stopped")[0].Fields().OpenRuns
	if len(runs) != 1 || runs[0].Count != 7 {
		t.Errorf("stop record's open runs %+v", runs)
	}
}

// A wb-netd that doesn't start refuses the start with netd-start-failed,
// and the run ends with its transport closed (fails closed).
func TestNetdStartFailedRefused(t *testing.T) {
	h := newHarness(t)
	h.cfg.Network = &fakeNetwork{err: errors.New("no version record")}
	m := h.manager()
	err := m.Start(t.Context(), vm.SlotWork)
	ref, ok := errors.AsType[*vm.Refusal](err)
	if !ok || ref.Rule != vm.RuleNetdStartFailed {
		t.Fatalf("start = %v, want %s", err, vm.RuleNetdStartFailed)
	}
	waitStopped(t, m, vm.SlotWork)
	inst := h.provider.Last()
	if tr := inst.Transports(); !inst.Closed() || len(tr) != 1 || tr[0].Closes() != 1 {
		t.Errorf("instance closed %v, transports %v", inst.Closed(), tr)
	}
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stopped", string(vm.RuleNetdStartFailed), statusFailure)
}

// wb-netd's start is prepared, its policy loaded, before the provider's
// start, so no packet transport waits unread on it (S04-09-netd-start,
// "The held transport"). A failed prepare refuses the start with
// netd-start-failed, and the VM isn't started.
func TestNetdPreparedBeforeTheVM(t *testing.T) {
	h := newHarness(t)
	network := &fakeNetwork{provider: h.provider}
	h.cfg.Network = network
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if network.prepares != 1 || len(network.starts) != 1 || network.starts[0] != 0 {
		t.Fatalf("prepares %d, provider starts before each %v; want 1, [0]", network.prepares, network.starts)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}

	h = newHarness(t)
	h.cfg.Network = &fakeNetwork{prepareErr: errors.New("no policy")}
	m = h.manager()
	err := m.Start(t.Context(), vm.SlotWork)
	if ref, ok := errors.AsType[*vm.Refusal](err); !ok || ref.Rule != vm.RuleNetdStartFailed {
		t.Fatalf("start = %v, want %s", err, vm.RuleNetdStartFailed)
	}
	waitStopped(t, m, vm.SlotWork)
	if inst := h.provider.Last(); len(inst.Starts()) != 0 || !inst.Closed() {
		t.Errorf("provider starts %v, closed %v: want none, closed", inst.Starts(), inst.Closed())
	}
}

// A wb-netd that exits before the VM is running, here before its Start
// returned, still stops the VM with netd-exited: the VM doesn't run on
// without it.
func TestNetdExitDuringStartStopsTheVM(t *testing.T) {
	h := newHarness(t)
	h.cfg.Network = &fakeNetwork{exitAtStart: true}
	m := h.manager()
	_ = m.Start(t.Context(), vm.SlotWork)
	waitStopped(t, m, vm.SlotWork)
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stopped", string(vm.RuleNetdExited), statusFailure)
	if !strings.Contains(h.logs.String(), `"rule":"netd-exited","decision":"stop-vm"`) {
		t.Errorf("the stop isn't logged:\n%s", h.logs.String())
	}
	if !h.provider.Last().Closed() {
		t.Error("the instance isn't closed")
	}
}

// A wb-netd that exits while the run is live stops the VM at once, with
// netd-exited, and isn't restarted (S04-07-supervision, "Restarting a
// daemon").
func TestNetdExitStopsTheVM(t *testing.T) {
	h := newHarness(t)
	network := &fakeNetwork{}
	h.cfg.Network = network
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	network.last(t).exit()
	waitStopped(t, m, vm.SlotWork)
	inst := h.provider.Last()
	if !inst.Closed() || len(inst.Stops()) != 0 {
		t.Errorf("closed %v, stops %v: want closed without a stop request", inst.Closed(), inst.Stops())
	}
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stop", string(vm.RuleNetdExited), statusSuccess)
	checkRecord(t, h, "vm stopped", string(vm.RuleNetdExited), statusFailure)
	if !strings.Contains(h.logs.String(), `"rule":"netd-exited","decision":"stop-vm"`) {
		t.Errorf("the stop isn't logged:\n%s", h.logs.String())
	}
	if n := len(network.runs); n != 1 {
		t.Errorf("%d wb-netd starts, want 1: no restart", n)
	}
}

// A wb-netd that exits while a requested stop is under way leaves that
// stop and its vm-stop rule as they are: netd-exited doesn't take over.
func TestNetdExitDuringStopKeepsTheStop(t *testing.T) {
	h := newHarness(t)
	network := &fakeNetwork{}
	h.cfg.Network = network
	h.provider.OnStop = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StopRequest) error {
		network.last(t).exit()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(h.logs.String(), `"msg":"vm stop not needed","rule":"netd-exited","decision":"none"`) {
			if time.Now().After(deadline) {
				return errors.New("the exit wasn't seen")
			}
			time.Sleep(time.Millisecond)
		}
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_STOPPED, nil)
		return nil
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stopped", string(vm.RuleStop), statusSuccess)
	if strings.Contains(h.logs.String(), `"rule":"netd-exited","decision":"stop-vm"`) {
		t.Errorf("netd-exited took over the stop:\n%s", h.logs.String())
	}
	if stops := h.provider.Last().Stops(); len(stops) != 1 {
		t.Errorf("stops = %v, want the request only", stops)
	}
}
