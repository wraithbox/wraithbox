package vm

import (
	"context"
	"fmt"
	"log/slog"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// Start cold-starts slot's VM. Every start in M2 is a cold start. The
// steps, in order:
//
//  1. refused in refusing mode, with its rule;
//  2. refused with vm-running while the slot is starting, running or
//     stopping;
//  3. the bundle directory, created when it is missing;
//  4. the guest tools disk, cloned into the bundle;
//  5. a new generation, committed to state.db;
//  6. a new provider instance, and its start with the configured
//     resources.
//
// It returns once the provider has started the VM. A refusal is a
// *Refusal, logged with its rule.
func (m *Manager) Start(ctx context.Context, slot Slot) error {
	if r := m.checkRequest(slot); r != nil {
		return m.refuse(ctx, "start", r)
	}
	if r := m.reserve(slot); r != nil {
		return m.refuse(ctx, "start", r)
	}
	generation, r := m.prepare(ctx, slot)
	if r != nil {
		m.unreserve(slot)
		return m.refuse(ctx, "start", r)
	}
	return m.boot(ctx, slot, generation)
}

// reserve moves a stopped slot to starting, so a second start is
// refused while the first runs its steps.
func (m *Manager) reserve(slot Slot) *Refusal {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.slots[slot]
	if rec.phase != PhaseStopped {
		return &Refusal{
			Rule: RuleRunning, Slot: slot, Phase: rec.phase, Generation: rec.generation,
			Message: fmt.Sprintf("the %s VM is already %s", slot, rec.phase),
		}
	}
	rec.phase = PhaseStarting
	return nil
}

// unreserve gives back a slot whose start was refused before it had a
// provider instance.
func (m *Manager) unreserve(slot Slot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slots[slot].phase = PhaseStopped
}

// prepare runs the steps before the provider: the bundle, the tools disk
// and the generation.
func (m *Manager) prepare(ctx context.Context, slot Slot) (uint64, *Refusal) {
	bundle, r := ensureBundle(slot, m.bundlePath(slot))
	if r != nil {
		return 0, r
	}
	if err := replaceToolsDisk(m.cfg.Cloner, m.cfg.ToolsImage, bundle); err != nil {
		return 0, &Refusal{
			Rule: RuleToolsDiskCloneFailed, Slot: slot, Phase: PhaseStopped, err: err,
			Message: fmt.Sprintf("the %s VM can't start: the guest tools disk couldn't be copied into its bundle: %v", slot, err),
		}
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "guest tools disk replaced",
		slog.String("decision", "accept"), slog.String("slot", string(slot)),
		slog.String("source", m.cfg.ToolsImage), slog.String("bundle", bundle))
	return m.nextGeneration(ctx, slot)
}

// nextGeneration commits the slot's next generation before any provider
// sees it, so no generation is used twice (S04-02-vm-provider,
// "Restore").
func (m *Manager) nextGeneration(ctx context.Context, slot Slot) (uint64, *Refusal) {
	g, err := m.cfg.Generations.NextGeneration(ctx, slot.name())
	if err != nil {
		return 0, &Refusal{
			Rule: RuleGenerationFailed, Slot: slot, Phase: PhaseStopped, err: err,
			Message: fmt.Sprintf("the %s VM can't start: the state database couldn't record a new generation: %v", slot, err),
		}
	}
	if r := checkGeneration(slot, g); r != nil {
		return 0, r
	}
	return g, nil
}

// checkGeneration refuses generation 0. Generations start at 1, and
// wb-vmd and the hand-off receiver refuse 0 (proto/wraithbox/vmd/v1).
func checkGeneration(slot Slot, g uint64) *Refusal {
	if g != 0 {
		return nil
	}
	return &Refusal{
		Rule: RuleGenerationInvalid, Slot: slot,
		Message: fmt.Sprintf("the %s VM has generation 0, and generations start at 1", slot),
	}
}

// boot opens a new provider instance for generation, follows its states
// and starts the VM.
func (m *Manager) boot(ctx context.Context, slot Slot, generation uint64) error {
	inst, err := m.cfg.Provider.Open(ctx, slot)
	if err != nil {
		m.unreserve(slot)
		return m.refuse(ctx, "start", providerRefusal(slot, generation, "start", err))
	}
	r := newRun(slot, generation, inst)
	m.mu.Lock()
	rec := m.slots[slot]
	rec.run, rec.generation, rec.lastErr = r, generation, nil
	m.mu.Unlock()
	go m.watch(context.WithoutCancel(ctx), r)

	// The configured caps reach wb-vmd on every start
	// (SEC13-bounded-resources).
	req := &vmdv1.StartRequest{
		Generation: generation, CpuCount: m.cfg.Resources.CPUCount, MemoryMib: m.cfg.Resources.MemoryMiB,
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm start",
		slog.String("decision", "accept"), slog.String("slot", string(slot)),
		slog.Uint64("generation", generation), slog.Uint64("cpu_count", uint64(req.GetCpuCount())),
		slog.Uint64("memory_mib", req.GetMemoryMib()))
	if err := inst.Start(ctx, req); err != nil {
		return m.refuse(ctx, "start", m.startFailed(ctx, r, err))
	}
	m.started(ctx, r)
	return nil
}

// startFailed ends a run whose start failed, keeps its framework error,
// and returns the refusal: vm-limit-other-software for the framework's
// limit of running VMs, else the provider's.
func (m *Manager) startFailed(ctx context.Context, r *run, err error) *Refusal {
	ref := providerRefusal(r.slot, r.generation, "start", err)
	m.mu.Lock()
	rec := m.slots[r.slot]
	if ref.VMError != nil && rec.run == r {
		rec.lastErr = cloneVMError(ref.VMError)
	}
	if ref.VMError.GetCode() == vzErrorVirtualMachineLimitExceeded {
		ref = limitRefusal(ref, m.runningLocked(r.slot))
	}
	m.mu.Unlock()
	m.finish(ctx, r, endStartFailed)
	ref.Phase = PhaseStarting
	return ref
}

// started moves the slot to running once the provider's start returned.
// A run whose VM already stopped ends at once.
func (m *Manager) started(ctx context.Context, r *run) {
	m.mu.Lock()
	rec := m.slots[r.slot]
	if rec.run == r && rec.phase == PhaseStarting {
		rec.phase = PhaseRunning
	}
	end := m.settleLocked(r)
	m.mu.Unlock()
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm running",
		slog.String("decision", "accept"), slog.String("slot", string(r.slot)),
		slog.Uint64("generation", r.generation))
	m.finish(ctx, r, end)
}
