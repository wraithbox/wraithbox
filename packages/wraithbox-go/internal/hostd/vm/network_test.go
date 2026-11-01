package vm_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// TestC11MACAddressInEachStart is C11-host-daemons, "VM manager": each
// StartRequest carries the slot's MAC address from state.db, the same
// on every start of the slot, and another slot has another one
// (S06-06-devices-and-warm-start, "Devices").
func TestC11MACAddressInEachStart(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	macs := map[vm.Slot][][]byte{}
	for _, slot := range []vm.Slot{vm.SlotWork, vm.SlotIsolated, vm.SlotWork} {
		if err := m.Start(t.Context(), slot); err != nil {
			t.Fatal(err)
		}
		if err := m.Stop(t.Context(), slot); err != nil {
			t.Fatal(err)
		}
		waitStopped(t, m, slot)
		starts := h.provider.Last().Starts()
		macs[slot] = append(macs[slot], starts[len(starts)-1].GetMacAddress())
	}
	work, isolated := macs[vm.SlotWork], macs[vm.SlotIsolated]
	want, err := h.db.MACAddress(t.Context(), "macos-work")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(work[0], want) || !bytes.Equal(work[1], want) {
		t.Errorf("work starts had %x, want %x on each", work, want)
	}
	if bytes.Equal(isolated[0], want) || len(isolated[0]) != 6 {
		t.Errorf("isolated start had %x, want another address than work's %x", isolated[0], want)
	}
}

type failingMACs struct{}

func (failingMACs) MACAddress(context.Context, string) ([]byte, error) {
	return nil, errors.New("disk I/O error")
}

// A slot whose MAC address state.db can't give doesn't start, and no
// provider sees it.
func TestMACFailureRefused(t *testing.T) {
	h := newHarness(t)
	h.cfg.MACAddresses = failingMACs{}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleMACFailed)
	if len(h.provider.Instances()) != 0 {
		t.Error("an instance was opened without a MAC address")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s after a refused start", s.Phase)
	}
	if !strings.Contains(h.logs.String(), string(vm.RuleMACFailed)) {
		t.Error("the refusal isn't logged with its rule")
	}
}

// TestC11TransportHeldUntilStop is C11-host-daemons, "VM manager": the
// manager holds the VM's packet transport while the VM runs, and closes
// it once when the run ends, by a stop or by a wb-vmd that exits.
func TestC11TransportHeldUntilStop(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	tr := onlyTransport(t, h.provider.Last())
	if n := tr.Closes(); n != 0 {
		t.Fatalf("the transport was closed %d times while the VM runs", n)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, m, vm.SlotWork)
	if n := tr.Closes(); n != 1 {
		t.Errorf("after a stop the transport was closed %d times, want 1", n)
	}

	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	inst := h.provider.Last()
	tr = onlyTransport(t, inst)
	inst.Exit()
	waitStopped(t, m, vm.SlotWork)
	if n := tr.Closes(); n != 1 {
		t.Errorf("after wb-vmd exited the transport was closed %d times, want 1", n)
	}
}

func onlyTransport(t *testing.T, inst *vmtest.Instance) *vmtest.Transport {
	t.Helper()
	all := inst.Transports()
	if len(all) != 1 {
		t.Fatalf("the instance returned %d transports, want 1", len(all))
	}
	return all[0]
}

// A VM that starts without its packet transport is refused with
// vm-network-missing and stopped: no VM runs without the transport
// wb-netd reads (S07-02-packet-path, "Packet transport").
func TestNetworkMissingRefused(t *testing.T) {
	h := newHarness(t)
	h.provider.NoTransport = true
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleNetworkMissing)
	if inst := h.provider.Last(); !inst.Closed() {
		t.Error("the instance isn't closed")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
	if !strings.Contains(h.logs.String(), string(vm.RuleNetworkMissing)) {
		t.Error("the refusal isn't logged with its rule")
	}
}

// A transport that comes after the start's deadline is closed.
func TestLateTransportClosed(t *testing.T) {
	h := newHarness(t)
	h.cfg.StartTimeout = 10 * time.Millisecond
	release := make(chan struct{})
	h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error {
		<-release
		return nil
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleStartTimeout)
	close(release)
	inst := h.provider.Last()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if all := inst.Transports(); len(all) == 1 && all[0].Closes() == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the late transport wasn't closed")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestPacketTransportHasNoReadOrWrite: the type the manager holds the
// transport in has Close and nothing else, so nothing in wb-hostd reads
// or writes the guest's frames through it.
func TestPacketTransportHasNoReadOrWrite(t *testing.T) {
	typ := reflect.TypeFor[vm.PacketTransport]()
	if typ.NumMethod() != 1 || typ.Method(0).Name != "Close" {
		t.Fatalf("vm.PacketTransport has %d methods, want Close only", typ.NumMethod())
	}
}
