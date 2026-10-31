package hostservice

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/kills"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// RuleShellStartTimeout: the VM or its guest wasn't ready for the shell
// within the start wait. The refusal names the last thing it waited
// for.
const RuleShellStartTimeout = "shell-start-timeout"

// RuleShellClientGone: wb's stream ended while the shell waited for its
// VM or guest, so the shell didn't start.
const RuleShellClientGone = "shell-client-gone"

// waiting is a step of the admission that isn't ready yet, which the
// shell waits for.
type waiting struct {
	rule string
	msg  string
}

func (w *waiting) Error() string { return w.msg }

// passingRules are the refusals of the guest links that pass on their
// own: the link comes up, the mount and the project user are answered,
// and the kills finish.
var passingRules = map[string]bool{
	guestconn.RuleSessionNoLink:       true,
	guestconn.RuleSessionMountPending: true,
	guestconn.RuleSessionKillsPending: true,
	guestconn.RuleSessionUserPending:  true,
	kills.RuleKillPending:             true,
}

// passes reports whether err is a step the shell waits for.
func passes(err error) bool {
	if _, ok := errors.AsType[*waiting](err); ok {
		return true
	}
	if r, ok := errors.AsType[*guestconn.SessionRefusal](err); ok {
		return passingRules[r.Rule]
	}
	return false
}

// admit waits until slot's VM runs, its guest can start a session, and
// projectID's user is confirmed, and returns the run's generation and
// the user. It starts a stopped VM, as a session start does
// (S06-04-vms-and-placement). Each step that refuses for good refuses
// the shell with its rule and message. One that is still pending past
// the start wait refuses it with RuleShellStartTimeout.
func (s *ShellServer) admit(ctx context.Context, slot vm.Slot, projectID string) (uint64, state.ProjectUser, error) {
	wait, poll := s.StartWait, s.StartPoll
	if wait <= 0 {
		wait = DefaultStartWait
	}
	if poll <= 0 {
		poll = DefaultStartPoll
	}
	deadline := s.after(wait)
	for {
		generation, u, err := s.tryAdmit(ctx, slot, projectID)
		if err == nil || !passes(err) {
			return generation, u, err
		}
		select {
		case <-ctx.Done():
			return 0, state.ProjectUser{}, &projectRefusal{code: codes.Canceled, rule: RuleShellClientGone, msg: fmt.Sprintf(
				"wb went while the shell waited for the %s VM: %s", slot, ctx.Err())}
		case <-deadline:
			return 0, state.ProjectUser{}, &projectRefusal{code: codes.Unavailable, rule: RuleShellStartTimeout, msg: fmt.Sprintf(
				"the %s VM wasn't ready for the shell within %s: %s Try again.", slot, wait, err.Error())}
		case <-s.after(poll):
		}
	}
}

// tryAdmit runs each step of the admission once.
func (s *ShellServer) tryAdmit(ctx context.Context, slot vm.Slot, projectID string) (uint64, state.ProjectUser, error) {
	generation, err := s.running(ctx, slot)
	if err != nil {
		return 0, state.ProjectUser{}, err
	}
	// Nothing runs as a project user before the data mount of this link
	// succeeded and the first-connection kills were sent
	// (S06-05-disks, "Mounting the data volume"; SEC08-proj-isolation).
	if err := s.Links.CheckSessionStart(slot, generation); err != nil {
		return 0, state.ProjectUser{}, err
	}
	if s.Shells.Killing(slot, generation, projectID) {
		return 0, state.ProjectUser{}, &waiting{rule: kills.RuleKillPending, msg: fmt.Sprintf(
			"wb-hostd is stopping the project's processes in the %s VM.", slot)}
	}
	u, err := s.Links.EnsureProjectUser(ctx, slot, generation, projectID)
	return generation, u, err
}

// running returns the generation of slot's running VM. It starts a
// stopped one, and waits for one that is starting. A VM that is stopping
// refuses the shell: it would be gone under it.
func (s *ShellServer) running(ctx context.Context, slot vm.Slot) (uint64, error) {
	st := s.VMs.Status(slot)
	switch st.Phase {
	case vm.PhaseRunning:
		return st.Generation, nil
	case vm.PhaseStarting:
		return 0, &waiting{rule: string(vm.RuleStarting), msg: fmt.Sprintf("the %s VM is starting.", slot)}
	case vm.PhaseStopping:
		return 0, &projectRefusal{code: codes.FailedPrecondition, rule: string(vm.RuleStopping), msg: fmt.Sprintf(
			"the %s VM is stopping. Wait until it has stopped, and run wb shell again.", slot)}
	case vm.PhaseStopped:
	}
	// A start runs to its end when wb goes, as StartVM's does.
	generation, err := s.VMs.StartRun(context.WithoutCancel(ctx), slot)
	if err != nil {
		if r, ok := errors.AsType[*vm.Refusal](err); ok && r.Rule == vm.RuleRunning {
			// Another start won the race: wait for it.
			return 0, &waiting{rule: string(vm.RuleStarting), msg: fmt.Sprintf("the %s VM is starting.", slot)}
		}
		return 0, err
	}
	return generation, nil
}
