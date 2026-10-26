package vm

import (
	"context"
	"log/slog"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// run is one VM run: one provider instance and one generation. Its
// fields but ended are guarded by Manager.mu.
type run struct {
	slot       Slot
	generation uint64
	inst       Instance
	// vmStopped: the instance reported STOPPED or ERROR for this
	// generation, or ended.
	vmStopped bool
	// finishing: finish has begun closing the instance.
	finishing bool
	// ended is closed once the instance is closed and the slot stopped.
	ended chan struct{}
}

func newRun(slot Slot, generation uint64, inst Instance) *run {
	return &run{slot: slot, generation: generation, inst: inst, ended: make(chan struct{})}
}

// ending says why a run ends.
type ending int

const (
	// endNone: the run doesn't end now.
	endNone ending = iota
	// endRequested: the VM stopped after a stop request.
	endRequested
	// endUnexpected: the VM stopped, or its wb-vmd ended, without a
	// stop request.
	endUnexpected
	// endStartFailed: the provider's start failed.
	endStartFailed
	// endClosed: the VM didn't stop after the force stop's deadline, so
	// the manager closed the instance.
	endClosed
)

// End is how a run ended, as Config.OnEnded gets it.
type End struct {
	// Cold is true when the guest stopped and its state is gone: a stop,
	// a crash or a failed start, not a save.
	Cold bool
	// Cause is why the run ended, for the log.
	Cause string
}

func (e ending) String() string {
	switch e {
	case endNone:
		return "none"
	case endRequested:
		return "stop requested"
	case endUnexpected:
		return "stopped unexpectedly"
	case endStartFailed:
		return "start failed"
	case endClosed:
		return "instance closed after the force stop's deadline"
	}
	return "unknown"
}

// watch follows the instance's states until it ends.
func (m *Manager) watch(ctx context.Context, r *run) {
	for st := range r.inst.States() {
		m.observe(ctx, r, st)
	}
	// A wb-vmd that exits means its VM stopped (S04-07-supervision,
	// "`wb-vmd` and `wb-vmd-install`").
	m.mu.Lock()
	r.vmStopped = true
	end := m.settleLocked(r)
	m.mu.Unlock()
	m.finish(ctx, r, end)
}

// observe takes one state from the instance. A state of another
// generation changes nothing: wb-vmd's first state is generation 0,
// before the start.
func (m *Manager) observe(ctx context.Context, r *run, st *vmdv1.VMState) {
	if st.GetGeneration() != r.generation {
		m.log.LogAttrs(ctx, slog.LevelDebug, "vm state of another generation ignored",
			slog.String("slot", string(r.slot)), slog.Uint64("generation", r.generation),
			slog.Uint64("state_generation", st.GetGeneration()), slog.String("state", st.GetState().String()))
		return
	}
	m.mu.Lock()
	if e := st.GetError(); e != nil && m.slots[r.slot].run == r {
		m.slots[r.slot].lastErr = cloneVMError(e)
	}
	if s := st.GetState(); s == vmdv1.State_STATE_STOPPED || s == vmdv1.State_STATE_ERROR {
		r.vmStopped = true
	}
	end := m.settleLocked(r)
	m.mu.Unlock()
	if end != endNone {
		// finish closes the instance on another goroutine, so this
		// reader keeps draining States while Close runs.
		m.runs.Go(func() { m.finish(ctx, r, end) })
	}
}

// settleLocked decides whether a run whose VM stopped ends now. While
// the slot is starting, the start's result decides.
func (m *Manager) settleLocked(r *run) ending {
	rec := m.slots[r.slot]
	if !r.vmStopped || r.finishing || rec.run != r {
		return endNone
	}
	switch rec.phase {
	case PhaseRunning:
		return endUnexpected
	case PhaseStopping:
		return endRequested
	case PhaseStarting, PhaseStopped:
		return endNone
	}
	return endNone
}

// finish ends r: it closes the instance, then stops the slot. It runs
// once per run, and does nothing for endNone.
func (m *Manager) finish(ctx context.Context, r *run, why ending) {
	if why == endNone {
		return
	}
	m.mu.Lock()
	if r.finishing {
		m.mu.Unlock()
		return
	}
	r.finishing = true
	m.mu.Unlock()

	closeErr := r.inst.Close()

	m.mu.Lock()
	rec := m.slots[r.slot]
	if rec.run == r {
		rec.phase, rec.run = PhaseStopped, nil
	}
	lastErr := cloneVMError(rec.lastErr)
	m.mu.Unlock()
	close(r.ended)
	m.logEnd(ctx, r, why, lastErr, closeErr)
	if m.cfg.OnEnded != nil {
		// No run is saved yet, so every end is a cold stop. Warm start
		// sets Cold to false for a save.
		m.cfg.OnEnded(r.slot, r.generation, End{Cold: true, Cause: why.String()})
	}
}

func (m *Manager) logEnd(ctx context.Context, r *run, why ending, lastErr *vmdv1.VMError, closeErr error) {
	attrs := []slog.Attr{
		slog.String("slot", string(r.slot)), slog.Uint64("generation", r.generation),
		slog.String("why", why.String()),
	}
	if closeErr != nil {
		attrs = append(attrs, slog.String("close_error", closeErr.Error()))
	}
	if why != endUnexpected {
		m.log.LogAttrs(ctx, slog.LevelInfo, "vm stopped", attrs...)
		return
	}
	attrs = append(attrs, slog.String("rule", string(RuleStoppedUnexpectedly)), slog.String("decision", "record"))
	if lastErr != nil {
		attrs = append(attrs, vmErrorAttrs(lastErr, RuleStoppedUnexpectedly))
	}
	m.log.LogAttrs(ctx, slog.LevelWarn, "vm stopped", attrs...)
}
