package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// Rule names the rule behind a refusal or a logged decision. A refusal
// passed on from wb-vmd keeps wb-vmd's rule.
type Rule string

// The rules of this package.
const (
	// RuleSlotUnknown: the request names a slot other than work and
	// isolated.
	RuleSlotUnknown Rule = "vm-slot-unknown"
	// RuleRunning: a start while the slot is starting, running or
	// stopping.
	RuleRunning Rule = "vm-running"
	// RuleNotRunning: a stop or a connection while the VM is stopped.
	RuleNotRunning Rule = "vm-not-running"
	// RuleStarting: a stop or a connection while the VM is starting.
	RuleStarting Rule = "vm-starting"
	// RuleStopping: a stop or a connection while the VM is stopping.
	RuleStopping Rule = "vm-stopping"
	// RuleLiveSessions: a stop while the VM has a session that is
	// starting, running, paused or ending (S06-09-session-lifecycle,
	// "Idle suspend").
	RuleLiveSessions Rule = "vm-live-sessions"
	// RuleBundleNotDirectory: the slot's bundle path is a symbolic link
	// or not a directory (S04-04-vm-processes, "The slot").
	RuleBundleNotDirectory Rule = "vm-bundle-not-directory"
	// RuleBundleCreateFailed: the bundle directory couldn't be read or
	// created.
	RuleBundleCreateFailed Rule = "vm-bundle-create-failed"
	// RuleToolsDiskCloneFailed: the guest tools disk couldn't be cloned
	// into the bundle (S06-12-guest-agent, "Guest tools disk").
	RuleToolsDiskCloneFailed Rule = "tools-disk-clone-failed"
	// RuleGenerationFailed: state.db couldn't hand out a generation.
	RuleGenerationFailed Rule = "vm-generation-failed"
	// RuleGenerationInvalid: a generation of 0. Generations start at 1,
	// because proto3 reads an absent field as 0 and wb-vmd refuses it
	// (proto/wraithbox/vmd/v1).
	RuleGenerationInvalid Rule = "vm-generation-invalid"
	// RuleLimitOtherSoftware: the start failed on the Virtualization
	// framework's limit of running VMs while Wraith Box held fewer than
	// two slots, so a VM outside Wraith Box holds one
	// (S06-07-slot-admission, "Other software").
	RuleLimitOtherSoftware Rule = "vm-limit-other-software"
	// RuleProviderFailed: the provider failed without a rule of
	// wb-vmd's, or with one that isn't a rule name.
	RuleProviderFailed Rule = "vm-provider-failed"
	// RuleGuestPort: a connection to guest port 0, or to one of 1024 or
	// above (S04-02-vm-provider, "Guest ports below 1024").
	RuleGuestPort Rule = "guest-port-invalid"
	// RuleStoppedUnexpectedly is logged, never returned: the VM reached
	// STOPPED or ERROR, or its wb-vmd ended, without a stop request.
	RuleStoppedUnexpectedly Rule = "vm-stopped-unexpectedly"
)

// vzErrorVirtualMachineLimitExceeded is VZErrorVirtualMachineLimitExceeded
// in the Virtualization framework's VZError.h. wb-vmd passes only codes
// of VZErrorDomain in VMError.code, and 0 for any other.
const vzErrorVirtualMachineLimitExceeded = 6

// maxRuleLen bounds a rule passed on from wb-vmd.
const maxRuleLen = 64

// Refusal is the error of every refusal in this package. It carries the
// rule, a message for the user (NFR06-explained-refusals) and the fields
// the rule is about. The message can hold text from wb-vmd, which wb
// escapes before it prints it.
type Refusal struct {
	Rule Rule
	Slot Slot
	// Message says what was refused, why, and how to go on.
	Message string
	// Phase is the slot's phase when the request was refused.
	Phase Phase
	// Generation is the generation the refusal is about, 0 when none.
	Generation uint64
	// VMError is the framework error behind a failed start, nil when
	// there is none.
	VMError *vmdv1.VMError
	// Running lists the VMs Wraith Box runs, for
	// vm-limit-other-software.
	Running []Status
	// Sessions lists the live sessions, for vm-live-sessions.
	Sessions []Session

	err error
}

func (r *Refusal) Error() string { return r.Message }

// Unwrap returns the cause, such as the provider's error.
func (r *Refusal) Unwrap() error { return r.err }

// refuse logs r with its rule and returns it.
func (m *Manager) refuse(ctx context.Context, op string, r *Refusal) *Refusal {
	attrs := []slog.Attr{
		slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"),
		slog.String("op", op), slog.String("slot", string(r.Slot)),
		slog.String("phase", string(r.Phase)), slog.Uint64("generation", r.Generation),
		slog.String("reason", r.Message),
	}
	if r.VMError != nil {
		attrs = append(attrs, vmErrorAttrs(r.VMError, r.Rule))
	}
	if r.err != nil {
		attrs = append(attrs, slog.String("cause", r.err.Error()))
	}
	m.log.LogAttrs(ctx, slog.LevelWarn, "vm request refused", attrs...)
	return r
}

// vmErrorAttrs groups the four fields of a framework error with the
// rule it was classified under (S06-06-devices-and-warm-start,
// "Deciding permanence").
func vmErrorAttrs(e *vmdv1.VMError, classified Rule) slog.Attr {
	return slog.Group("vm_error",
		slog.Int64("code", e.GetCode()), slog.String("failure_reason", e.GetFailureReason()),
		slog.Int("errno", int(e.GetErrno())), slog.String("description", e.GetDescription()),
		slog.String("classified", string(classified)))
}

// cloneVMError copies e, so a caller can't change the manager's copy.
func cloneVMError(e *vmdv1.VMError) *vmdv1.VMError {
	if e == nil {
		return nil
	}
	return &vmdv1.VMError{
		Code: e.GetCode(), FailureReason: e.GetFailureReason(),
		Errno: e.GetErrno(), Description: e.GetDescription(),
	}
}

// providerRefusal turns the provider's error for op into a refusal. A
// *ProviderRefusal keeps wb-vmd's rule when it is a rule name. Any other
// error, or a rule that isn't one, is vm-provider-failed.
func providerRefusal(slot Slot, generation uint64, op string, err error) *Refusal {
	pr, ok := errors.AsType[*ProviderRefusal](err)
	if !ok {
		return &Refusal{
			Rule: RuleProviderFailed, Slot: slot, Generation: generation, err: err,
			Message: fmt.Sprintf("the VM provider of the %s VM failed to %s: %v", slot, op, err),
		}
	}
	rule := Rule(pr.Rule)
	if !isRuleName(pr.Rule) {
		rule = RuleProviderFailed
	}
	return &Refusal{
		Rule: rule, Slot: slot, Generation: generation, VMError: cloneVMError(pr.VMError), err: err,
		Message: fmt.Sprintf("wb-vmd refused to %s the %s VM (%q): %s", op, slot, pr.Rule, pr.Detail),
	}
}

// isRuleName reports whether s has the form of a rule: 1 to 64 bytes of
// lowercase ASCII letters, digits and hyphens.
func isRuleName(s string) bool {
	if s == "" || len(s) > maxRuleLen {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// limitRefusal is the refusal of a start that failed on the
// framework's limit of running VMs. Wraith Box counts only its own VMs,
// so the other one runs outside it. The manager doesn't retry, and
// doesn't stop a VM of its own (S06-07-slot-admission, "Other
// software").
func limitRefusal(base *Refusal, running []Status) *Refusal {
	ours := "Wraith Box runs no other VM."
	if len(running) > 0 {
		names := make([]string, len(running))
		for i, s := range running {
			names[i] = fmt.Sprintf("the %s VM (%s, generation %d)", s.Slot, s.Phase, s.Generation)
		}
		ours = "Wraith Box runs " + strings.Join(names, " and ") + "."
	}
	r := *base
	r.Rule = RuleLimitOtherSoftware
	r.Running = running
	r.Message = fmt.Sprintf("the %s VM can't start: macOS runs at most two macOS VMs, and a macOS VM outside Wraith Box holds a slot. %s Quit the other VM, then start the %s VM again.",
		base.Slot, ours, base.Slot)
	return &r
}
