package vmcmd

import (
	"context"
	"fmt"
	"time"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
)

// The rules of wb's own decisions in `wb vm start`, `stop` and
// `suspend`.
const (
	// RuleStartUnknown: the answer to a start didn't arrive. wb-hostd
	// runs a start it began to its end, so the VM may be starting.
	RuleStartUnknown = "vm-start-unknown"
	// RuleStopUnknown: the answer to a stop didn't arrive. wb-hostd runs
	// a stop it began to its end, so the VM may be stopping.
	RuleStopUnknown = "vm-stop-unknown"
	// RuleLinkTimeout: the VM runs, and its guest link wasn't up by the
	// deadline. The VM is left running.
	RuleLinkTimeout = "vm-link-timeout"
	// RuleLinkRefused: wb-hostd refused the guest's hello without naming
	// a rule.
	RuleLinkRefused = "vm-link-refused"
	// RuleStoppedUnexpectedly: the run wb started ended before its guest
	// link came up.
	RuleStoppedUnexpectedly = "vm-stopped-unexpectedly"
	// RuleSuspendNeedsWarmStart: `wb vm suspend` needs warm start
	// (S06-06-devices-and-warm-start), which isn't built.
	RuleSuspendNeedsWarmStart = "vm-suspend-needs-warm-start"
)

// The deadlines of `wb vm` (S05-03-management-commands, "`wb vm`").
const (
	// startTimeout bounds StartVM. A VM's first start formats its data
	// disk, as a reset does, so it gets the reset's bound.
	startTimeout = resetTimeout
	// DefaultLinkTimeout bounds the wait for the guest link once the VM
	// runs. A cold boot to wb-guestd's listener takes 13.5 s at p95
	// (X18-vsock-handoff), and a reconnect waits up to 5 s between tries
	// (S06-11-reconnect-and-recovery, "Reconnect"). 60 s leaves a margin
	// of more than three times the p95.
	DefaultLinkTimeout = 60 * time.Second
	// stopTimeout bounds StopVM: wb-hostd's requested stop (30 s), its
	// forced stop (10 s), and closing wb-vmd (internal/hostd/vm).
	stopTimeout = 2 * time.Minute
	// statusTimeout bounds each GetVMStatus.
	statusTimeout = 10 * time.Second
	// pollInterval is how often the wait for the guest link asks.
	pollInterval = 250 * time.Millisecond
)

// start is `wb vm start <vm>`: it asks wb-hostd to start the VM, then
// waits until its guest link is accepted or refused, or the deadline
// passes. A VM whose link isn't up is left running for `wb vm stop`.
func start(ctx context.Context, env Env, args []string) int {
	slot, v, ok := parseVM(args)
	if !ok {
		return usage(env, "wb vm start takes one VM, work or isolated")
	}
	hostd, code := connect(ctx, env)
	if hostd == nil {
		return code
	}
	defer func() { _ = hostd.Close() }()
	callCtx, cancel := context.WithTimeout(ctx, startTimeout)
	res, err := hostd.StartVM(callCtx, v)
	cancel()
	if mayHaveRun(err) {
		return fail(env, RuleStartUnknown, fmt.Sprintf(
			"the answer from wb-hostd didn't arrive (%s), and the %s VM may be starting: check it with wb vm status %s", err, slot, slot))
	}
	if err != nil {
		rule, msg := failureOf(err)
		return fail(env, rule, msg)
	}
	return waitLinked(ctx, env, hostd, slot, v, res.GetGeneration())
}

// waitLinked asks for the VM's state until the run of generation has
// its guest link accepted or refused, the run ended, or the deadline
// passed.
func waitLinked(ctx context.Context, env Env, hostd Hostd, slot string, v hostdv1.VM, generation uint64) int {
	timeout := env.LinkTimeout
	if timeout <= 0 {
		timeout = DefaultLinkTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		s, err := hostd.GetVMStatus(ctx, v)
		if err != nil && ctx.Err() == nil {
			rule, msg := failureOf(err)
			return fail(env, rule, msg)
		}
		if err == nil {
			if code, done := linkOutcome(env, slot, generation, s); done {
				return code
			}
		}
		select {
		case <-ctx.Done():
			return fail(env, RuleLinkTimeout, fmt.Sprintf(
				"the %s VM runs generation %d, but its guest link isn't up after %s. The VM is left running: wb vm status %s shows it, and wb vm stop %s stops it",
				slot, generation, timeout, slot, slot))
		case <-tick.C:
		}
	}
}

// linkOutcome reads one state of the VM wb started, and reports whether
// the wait is over, with its exit status.
func linkOutcome(env Env, slot string, generation uint64, s *hostdv1.VMStatus) (int, bool) {
	if s.GetGeneration() != generation || s.GetPhase() != hostdv1.VMPhase_VM_PHASE_RUNNING {
		msg := fmt.Sprintf("the %s VM's run of generation %d ended before its guest link came up", slot, generation)
		if e := s.GetLastError(); e != nil {
			msg += ". The last error: " + lastErrorText(e)
		}
		return fail(env, RuleStoppedUnexpectedly, msg), true
	}
	switch s.GetLinkState() {
	case hostdv1.LinkState_LINK_STATE_LINKED:
		fmt.Fprintf(env.Stdout, "The %s VM runs generation %d, and its guest is linked: wb-guestd version %s.\n",
			slot, generation, guestText(s.GetGuestVersion()))
		return exitOK, true
	case hostdv1.LinkState_LINK_STATE_REFUSED:
		r := s.GetLinkRefusal()
		rule := r.GetRule()
		if rule == "" {
			rule = RuleLinkRefused
		}
		return fail(env, rule, fmt.Sprintf("the %s VM runs generation %d and is left running, but wb-hostd refused its guest: %s",
			slot, generation, linkRefusalText(r))), true
	case hostdv1.LinkState_LINK_STATE_UNSPECIFIED, hostdv1.LinkState_LINK_STATE_DOWN, hostdv1.LinkState_LINK_STATE_CONNECTING:
	}
	return 0, false
}

// stop is `wb vm stop <vm>`: wb-hostd asks the guest to shut down, and
// stops the VM at once after a deadline. A refusal for live sessions
// names each one.
func stop(ctx context.Context, env Env, args []string) int {
	slot, v, ok := parseVM(args)
	if !ok {
		return usage(env, "wb vm stop takes one VM, work or isolated")
	}
	hostd, code := connect(ctx, env)
	if hostd == nil {
		return code
	}
	defer func() { _ = hostd.Close() }()
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	err := hostd.StopVM(ctx, v)
	if mayHaveRun(err) {
		return fail(env, RuleStopUnknown, fmt.Sprintf(
			"the answer from wb-hostd didn't arrive (%s), and the %s VM may still be stopping: check it with wb vm status %s", err, slot, slot))
	}
	if err != nil {
		rule, msg := failureOf(err)
		return fail(env, rule, msg)
	}
	fmt.Fprintf(env.Stdout, "The %s VM is stopped.\n", slot)
	return exitOK
}

// suspend is `wb vm suspend <vm>`. Suspend saves the VM's state for a
// warm start (S06-06-devices-and-warm-start), which isn't built, so wb
// refuses it without asking wb-hostd.
func suspend(env Env, args []string) int {
	slot, _, ok := parseVM(args)
	if !ok {
		return usage(env, "wb vm suspend takes one VM, work or isolated")
	}
	return fail(env, RuleSuspendNeedsWarmStart, fmt.Sprintf(
		"wb vm suspend needs warm start, which this version of Wraith Box doesn't have: wb vm stop %s stops the VM instead", slot))
}
