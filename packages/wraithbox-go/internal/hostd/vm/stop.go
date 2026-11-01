package vm

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
)

// SessionState is the state of a session that holds its VM
// (S06-09-session-lifecycle, "Idle suspend").
type SessionState string

// The states of a live session. A lost or ended session doesn't hold
// its VM.
const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionPaused   SessionState = "paused"
	SessionEnding   SessionState = "ending"
)

// Session is a live session in a VM, a debug shell included, as the
// Sessions hook lists it.
type Session struct {
	ID    string
	State SessionState
	// PID is the process ID of the session's wb.
	PID int
}

// HowItEnds says how the user ends the session: close its terminal for a
// running or paused session, wait for its deadline for a starting or
// ending one (S06-09-session-lifecycle, "Idle suspend").
func (s Session) HowItEnds() string {
	switch s.State {
	case SessionRunning, SessionPaused:
		return "close its terminal"
	case SessionStarting, SessionEnding:
		return "wait for its deadline"
	}
	return "wait for its deadline"
}

// Stop stops slot's VM: a requested stop, then a forced one after
// RequestStopTimeout (S05-03-management-commands, "`wb vm`"). It is
// refused with vm-not-running when the VM is stopped, and with
// vm-live-sessions while the VM has a session that is starting,
// running, paused or ending. It returns once the VM has stopped and its
// instance is closed.
func (m *Manager) Stop(ctx context.Context, slot Slot) error {
	if r := m.checkRequest(slot); r != nil {
		return m.refuse(ctx, "stop", r)
	}
	// The hook runs before the slot moves to stopping, so a session
	// admitted in between isn't listed. Session admission must refuse a
	// VM that isn't running, stopping included, which closes that window.
	var sessions []Session
	if m.cfg.Sessions != nil {
		sessions = m.cfg.Sessions(slot)
	}
	r, generation, ref := m.beginStop(slot, sessions)
	if ref != nil {
		return m.refuse(ctx, "stop", ref)
	}
	m.log.LogAttrs(ctx, slog.LevelInfo, "vm stop",
		slog.String("decision", "accept"), slog.String("rule", string(RuleStop)), slog.String("slot", string(slot)),
		slog.Uint64("generation", generation))
	m.cfg.Audit.Record(audit.VMStopping(string(slot), generation, string(RuleStop), "requested"))
	// A caller that goes away doesn't leave the VM half stopped. The
	// deadlines bound each step instead, the provider's calls included,
	// and the last step closes the instance.
	m.stopRun(context.WithoutCancel(ctx), r, generation)
	return nil
}

// ForceStop stops slot's VM at once for the audit writer, with cause as
// the rule: a record that can't be written, or a data disk that grew
// while the log's volume is below the free space floor (SEC10-audit,
// S09-11-audit, "Free space and failed writes"). It closes the run's
// instance, which stops the VM as a lost wb-hostd does
// (S04-07-supervision, "When `wb-hostd` stops"), without a stop request
// the guest could delay. It returns without waiting for the VM, so the
// writer's goroutine that calls it never waits on a VM. A slot without
// a run, or whose run is already ending, is left as it is. It
// implements audit.Stopper.
func (m *Manager) ForceStop(slot, cause string) {
	m.mu.Lock()
	rec, ok := m.slots[Slot(slot)]
	if !ok || rec.run == nil || rec.run.finishing {
		m.mu.Unlock()
		m.log.Info("vm stop not needed", "rule", cause, "decision", "none", "slot", slot,
			"reason", "the VM has no run that isn't already ending")
		return
	}
	r := rec.run
	r.rule = Rule(cause)
	if rec.phase == PhaseRunning {
		// No session is admitted, and no stop accepted, once the slot
		// isn't running.
		rec.phase = PhaseStopping
	}
	m.mu.Unlock()
	m.log.Warn("vm stop", "rule", cause, "decision", "stop-vm", "slot", slot,
		"generation", r.generation, "mode", "close")
	m.cfg.Audit.Record(audit.VMStopping(slot, r.generation, cause, cause))
	m.runs.Go(func() { m.finish(context.Background(), r, endForced) })
}

// beginStop moves a running slot to stopping, and returns its run and
// the generation the stop requests carry.
func (m *Manager) beginStop(slot Slot, sessions []Session) (*run, uint64, *Refusal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.slots[slot]
	if rec.phase != PhaseRunning {
		return nil, 0, phaseRefusal(slot, rec, "stop")
	}
	if r := checkGeneration(slot, rec.generation); r != nil {
		r.Phase = rec.phase
		return nil, 0, r
	}
	if len(sessions) > 0 {
		return nil, 0, sessionsRefusal(slot, rec, sessions)
	}
	rec.phase = PhaseStopping
	return rec.run, rec.generation, nil
}

// sessionsRefusal names each live session with its state, the process
// ID of its wb, and how it ends (S06-09-session-lifecycle, "Idle
// suspend", NFR06-explained-refusals).
func sessionsRefusal(slot Slot, rec *slotRecord, sessions []Session) *Refusal {
	parts := make([]string, len(sessions))
	for i, s := range sessions {
		parts[i] = fmt.Sprintf("session %s is %s (wb process %d): %s", s.ID, s.State, s.PID, s.HowItEnds())
	}
	return &Refusal{
		Rule: RuleLiveSessions, Slot: slot, Phase: rec.phase, Generation: rec.generation,
		Sessions: append([]Session(nil), sessions...),
		Message: fmt.Sprintf("the %s VM has live sessions and isn't stopped: %s. wb sessions lists them",
			slot, strings.Join(parts, "; ")),
	}
}

// stopRun asks for a requested stop, then a forced one, and closes the
// instance when neither stopped the VM in time. Closing it closes
// wb-vmd's lifeline, and wb-vmd stops its VM at once and exits
// (S04-07-supervision, "When `wb-hostd` stops").
func (m *Manager) stopRun(ctx context.Context, r *run, generation uint64) {
	request := &vmdv1.StopRequest{Generation: generation, Mode: vmdv1.StopMode_STOP_MODE_REQUEST}
	if m.stopWith(ctx, r, request, m.cfg.RequestStopTimeout) {
		return
	}
	force := &vmdv1.StopRequest{Generation: generation, Mode: vmdv1.StopMode_STOP_MODE_FORCE}
	if m.stopWith(ctx, r, force, m.cfg.ForceStopTimeout) {
		return
	}
	m.finish(ctx, r, endClosed)
	<-r.ended
}

// stopWith asks for the stop req, and reports whether the run ended
// within timeout. The deadline covers the provider's call as well as the
// wait for the VM to stop, so a wb-vmd that never answers doesn't hold
// the slot in stopping. The call runs on its own goroutine, and ends at
// the latest when the instance is closed.
func (m *Manager) stopWith(ctx context.Context, r *run, req *vmdv1.StopRequest, timeout time.Duration) bool {
	attrs := []slog.Attr{
		slog.String("slot", string(r.slot)), slog.Uint64("generation", req.GetGeneration()),
		slog.String("mode", req.GetMode().String()),
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answer := make(chan error, 1)
	go func() { answer <- r.inst.Stop(callCtx, req) }()
	for {
		select {
		case <-r.ended:
			return true
		case err := <-answer:
			if err == nil {
				// Wait for the VM to stop. A nil channel never receives.
				answer = nil
				continue
			}
			ref := providerRefusal(r.slot, req.GetGeneration(), "stop", err)
			attrs = append(attrs, slog.String("rule", string(ref.Rule)), slog.String("reason", ref.Message))
			m.log.LogAttrs(ctx, slog.LevelWarn, "vm stop failed", attrs...)
			return isClosed(r.ended)
		case <-callCtx.Done():
			attrs = append(attrs, slog.Duration("timeout", timeout))
			m.log.LogAttrs(ctx, slog.LevelWarn, "vm stop deadline passed", attrs...)
			return isClosed(r.ended)
		}
	}
}

// isClosed reports whether ch is closed, without waiting.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// ConnectGuest opens a connection to port in slot's running guest
// through the provider. generation is the run the caller follows: a
// connection for any generation but the current one is refused with
// vm-generation-stale, so a caller never gets a connection to a newer
// run than its own (S04-02-vm-provider, "Restore").
func (m *Manager) ConnectGuest(ctx context.Context, slot Slot, generation uint64, port uint32) (net.Conn, error) {
	if r := m.checkRequest(slot); r != nil {
		return nil, m.refuse(ctx, "connect", r)
	}
	// Every guest port wb-hostd connects to is below 1024, which only
	// root in the guest can bind (S04-02-vm-provider, "Guest ports below
	// 1024").
	if port == 0 || port >= 1024 {
		return nil, m.refuse(ctx, "connect", &Refusal{
			Rule: RuleGuestPort, Slot: slot,
			Message: fmt.Sprintf("guest port %d is refused: wb-hostd connects only to ports 1 to 1023", port),
		})
	}
	inst, ref := m.currentRun(slot, generation)
	if ref != nil {
		return nil, m.refuse(ctx, "connect", ref)
	}
	conn, err := inst.ConnectGuest(ctx, generation, port)
	if err != nil {
		return nil, m.refuse(ctx, "connect", providerRefusal(slot, generation, "connect to", err))
	}
	return conn, nil
}

// currentRun returns the instance of a running slot whose current
// generation is generation.
func (m *Manager) currentRun(slot Slot, generation uint64) (Instance, *Refusal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.slots[slot]
	if rec.phase != PhaseRunning {
		return nil, phaseRefusal(slot, rec, "connect to")
	}
	if r := checkGeneration(slot, rec.generation); r != nil {
		r.Phase = rec.phase
		return nil, r
	}
	if generation != rec.generation {
		return nil, &Refusal{
			Rule: RuleGenerationStale, Slot: slot, Phase: rec.phase, Generation: rec.generation,
			Message: fmt.Sprintf("can't connect to generation %d of the %s VM: it runs generation %d", generation, slot, rec.generation),
		}
	}
	return rec.run.inst, nil
}
