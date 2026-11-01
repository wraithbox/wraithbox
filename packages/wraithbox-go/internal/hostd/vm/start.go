package vm

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
)

// Start cold-starts slot's VM. Every start in M2 is a cold start. The
// steps, in order:
//
//  1. refused in refusing mode, with its rule;
//  2. the audit writer's check, refused with audit-free-space or
//     audit-write-failed (S09-11-audit);
//  3. refused with vm-running while the slot is starting, running or
//     stopping;
//  4. the bundle directory, created when it is missing;
//  5. the data disk checks, and a new data disk when the slot has none
//     (S06-13-data-disk-format);
//  6. the guest tools disk, cloned into the bundle;
//  7. a new generation, committed to state.db;
//  8. the start's audit record, written;
//  9. a new provider instance, and its start with the configured
//     resources.
//
// It returns once the provider has started the VM. A refusal is a
// *Refusal, logged with its rule.
func (m *Manager) Start(ctx context.Context, slot Slot) error {
	_, err := m.StartRun(ctx, slot)
	return err
}

// StartRun is Start, and returns the generation of the run it started,
// so a caller never takes a later run for its own.
func (m *Manager) StartRun(ctx context.Context, slot Slot) (uint64, error) {
	if r := m.checkRequest(slot); r != nil {
		return 0, m.refuse(ctx, "start", r)
	}
	// SEC10-audit: a VM starts only with room for its records, and only
	// while records are written (S09-11-audit, "Free space and failed
	// writes").
	if err := m.cfg.Audit.CanStart(string(slot)); err != nil {
		return 0, m.refuse(ctx, "start", auditRefusal(slot, 0, err))
	}
	if r := m.reserve(slot); r != nil {
		return 0, m.refuse(ctx, "start", r)
	}
	generation, r := m.prepare(ctx, slot)
	if r != nil {
		m.unreserve(slot)
		return 0, m.refuse(ctx, "start", r)
	}
	if err := m.boot(ctx, slot, generation); err != nil {
		return 0, err
	}
	return generation, nil
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
	if rec.resetting {
		return resettingRefusal(slot, "start")
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
	m.slots[slot].forced = ""
}

// prepare runs the steps before the provider: the bundle, the data
// disk, the tools disk and the generation.
func (m *Manager) prepare(ctx context.Context, slot Slot) (uint64, *Refusal) {
	bundle, r := ensureBundle(slot, m.bundlePath(slot))
	if r != nil {
		return 0, r
	}
	// SEC15-no-guest-disk-mount: a VM starts only with a data disk that
	// state.db records as formatted (S06-13-data-disk-format, "The one exception").
	// A format that has begun finishes, and records what it did, when
	// the caller goes away.
	if err := m.cfg.DataDisks.Prepare(context.WithoutCancel(ctx), string(slot)); err != nil {
		return 0, dataDiskRefusal(slot, err)
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

// open opens a new provider instance within StartTimeout. The instance
// lives until the manager closes it, not as long as the request that
// started it, so Open gets a context that is never cancelled, and the
// wait for it is bounded here instead. An instance that opens after the
// wait ended is closed at once.
func (m *Manager) open(ctx context.Context, slot Slot, generation uint64) (Instance, *Refusal) {
	type opened struct {
		inst Instance
		err  error
	}
	answer := make(chan opened, 1)
	go func() {
		inst, err := m.cfg.Provider.Open(context.WithoutCancel(ctx), slot, generation)
		answer <- opened{inst, err}
	}()
	timer := time.NewTimer(m.cfg.StartTimeout)
	defer timer.Stop()
	var ref *Refusal
	select {
	case o := <-answer:
		if o.err != nil {
			return nil, providerRefusal(slot, generation, "start", o.err)
		}
		return o.inst, nil
	case <-ctx.Done():
		ref = providerRefusal(slot, generation, "start", ctx.Err())
	case <-timer.C:
		ref = &Refusal{
			Rule: RuleStartTimeout, Slot: slot, Generation: generation,
			Message: fmt.Sprintf("the %s VM didn't start: its wb-vmd didn't open within %s", slot, m.cfg.StartTimeout),
		}
	}
	go func() {
		if o := <-answer; o.err == nil && o.inst != nil {
			_ = o.inst.Close()
		}
	}()
	return nil, ref
}

// boot opens a new provider instance for generation, follows its states
// and starts the VM.
func (m *Manager) boot(ctx context.Context, slot Slot, generation uint64) error {
	// The configured caps reach wb-vmd on every start
	// (SEC13-bounded-resources).
	req := &vmdv1.StartRequest{
		Generation: generation, CpuCount: m.cfg.Resources.CPUCount, MemoryMib: m.cfg.Resources.MemoryMiB,
	}
	if ref := m.recordStart(ctx, slot, req); ref != nil {
		m.unreserve(slot)
		return m.refuse(ctx, "start", ref)
	}
	// From here the start record is written, so every way out writes a
	// stop record too.
	if cause := m.takeForced(slot); cause != "" {
		return m.abortStart(ctx, slot, generation, forcedRefusal(slot, generation, cause))
	}
	inst, ref := m.open(ctx, slot, generation)
	if ref != nil {
		return m.abortStart(ctx, slot, generation, ref)
	}
	r := newRun(slot, generation, inst)
	m.mu.Lock()
	rec := m.slots[slot]
	rec.run, rec.generation, rec.lastErr = r, generation, nil
	cause := rec.forced
	rec.forced = ""
	m.mu.Unlock()
	m.runs.Go(func() { m.watch(context.WithoutCancel(ctx), r) })
	if cause != "" {
		return m.refuse(ctx, "start", m.startFailed(ctx, r, forcedRefusal(slot, generation, cause)))
	}
	if ref := m.startVM(ctx, r, req); ref != nil {
		return m.refuse(ctx, "start", m.startFailed(ctx, r, ref))
	}
	m.started(ctx, r)
	return nil
}

// recordStart logs the accepted start and writes its audit record. The
// VM starts only once the record is written (SEC10-audit, S09-11-audit,
// "Free space and failed writes"), so a start the writer refuses never
// reaches the provider.
func (m *Manager) recordStart(ctx context.Context, slot Slot, req *vmdv1.StartRequest) *Refusal {
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm start",
		slog.String("decision", "accept"), slog.String("rule", string(RuleStart)), slog.String("slot", string(slot)),
		slog.Uint64("generation", req.GetGeneration()), slog.Uint64("cpu_count", uint64(req.GetCpuCount())),
		slog.Uint64("memory_mib", req.GetMemoryMib()))
	rec := audit.VMStarting(string(slot), req.GetGeneration(), string(RuleStart),
		audit.Resources{CPUCount: req.GetCpuCount(), MemoryMiB: req.GetMemoryMib()})
	if err := m.cfg.Audit.Write(ctx, rec); err != nil {
		return auditRefusal(slot, req.GetGeneration(), err)
	}
	return nil
}

// takeForced returns and clears the cause of a ForceStop that came
// while slot was starting without a run.
func (m *Manager) takeForced(slot Slot) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cause := m.slots[slot].forced
	m.slots[slot].forced = ""
	return cause
}

// forcedRefusal refuses a start that the audit writer stopped before
// the VM ran (SEC10-audit).
func forcedRefusal(slot Slot, generation uint64, cause string) *Refusal {
	return &Refusal{
		Rule: Rule(cause), Slot: slot, Generation: generation,
		Message: fmt.Sprintf("the %s VM's start was stopped by the audit writer (%s). Run wb vm status for the reason", slot, cause),
	}
}

// abortStart ends a start that has its start record but no run: the
// slot is stopped again, and the stop record says why, as a failure.
func (m *Manager) abortStart(ctx context.Context, slot Slot, generation uint64, ref *Refusal) error {
	m.unreserve(slot)
	m.cfg.Audit.Record(audit.VMStopped(string(slot), generation, string(ref.Rule), "start failed", true))
	return m.refuse(ctx, "start", ref)
}

// startVM runs the provider's start within StartTimeout. The call runs
// on its own goroutine, so a wb-vmd that never answers is refused with
// vm-start-timeout. The call ends at the latest when startFailed closes
// the instance.
func (m *Manager) startVM(ctx context.Context, r *run, req *vmdv1.StartRequest) *Refusal {
	callCtx, cancel := context.WithTimeout(ctx, m.cfg.StartTimeout)
	defer cancel()
	answer := make(chan error, 1)
	go func() { answer <- r.inst.Start(callCtx, req) }()
	select {
	case err := <-answer:
		if err != nil {
			return providerRefusal(r.slot, r.generation, "start", err)
		}
		return nil
	case <-callCtx.Done():
		if err := ctx.Err(); err != nil {
			return providerRefusal(r.slot, r.generation, "start", err)
		}
		return &Refusal{
			Rule: RuleStartTimeout, Slot: r.slot, Generation: r.generation, err: callCtx.Err(),
			Message: fmt.Sprintf("the %s VM didn't start: wb-vmd didn't answer within %s", r.slot, m.cfg.StartTimeout),
		}
	}
}

// startFailed ends a run whose start failed, keeps its framework error,
// and returns the refusal: vm-limit-other-software for the framework's
// limit of running VMs, else ref.
func (m *Manager) startFailed(ctx context.Context, r *run, ref *Refusal) *Refusal {
	m.mu.Lock()
	rec := m.slots[r.slot]
	if ref.VMError != nil && rec.run == r {
		rec.lastErr = cloneVMError(ref.VMError)
	}
	if ref.VMError.GetCode() == vzErrorVirtualMachineLimitExceeded {
		ref = limitRefusal(ref, m.runningLocked(r.slot))
	}
	if r.rule == "" {
		r.rule = ref.Rule
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
	running := rec.run == r && rec.phase == PhaseStarting
	if running {
		rec.phase = PhaseRunning
	}
	end := m.settleLocked(r)
	m.mu.Unlock()
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm running",
		slog.String("decision", "accept"), slog.String("slot", string(r.slot)),
		slog.Uint64("generation", r.generation))
	if running {
		m.cfg.Audit.Record(audit.VMRunning(string(r.slot), r.generation, string(RuleStart)))
	}
	if running && m.cfg.OnRunning != nil {
		m.cfg.OnRunning(r.slot, r.generation, r.ended)
	}
	// The first start with a disk ends the SEC15-no-guest-disk-mount
	// exception for it. A failed record is logged, and doesn't stop the
	// VM.
	_ = m.cfg.DataDisks.Started(context.WithoutCancel(ctx), string(r.slot))
	m.finish(ctx, r, end)
}
