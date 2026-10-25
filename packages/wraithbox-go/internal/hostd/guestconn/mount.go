package guestconn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// MountHook asks the guest of a linked VM to mount its data volume
// (S06-05-disks, "Mounting the data volume"). The link calls it after
// each accepted hello, at the first contact of a run and after every
// reconnect, before any other call. It returns nil when the guest
// mounted the volume, a *guestlink.MountRefusal or a *MountError when
// the mount is refused, and any other error when the call failed. It
// must return: the link doesn't watch for a drop while it runs.
// SendMount makes wb-hostd's.
type MountHook func(ctx context.Context, slot vm.Slot, generation uint64, link *guestlink.Link) error

// MountRecords is the part of state.db the mount hook reads and writes.
// *state.DB implements it.
type MountRecords interface {
	DataDiskRecord(ctx context.Context, slot string) (state.DataDisk, bool, error)
	MarkDataDiskMounted(ctx context.Context, slot, volumeUUID string, at time.Time) error
}

// RuleMountRecord: wb-hostd has no formatted data disk recorded for the
// VM, so it has no volume UUID to ask for, or it couldn't read the
// record.
const RuleMountRecord = "guest-mount-record"

// MountError is a mount wb-hostd refused before it asked the guest.
type MountError struct {
	Rule    string
	Message string
}

func (e *MountError) Error() string { return e.Message + " (" + e.Rule + ")" }

// SendMount returns wb-hostd's MountHook. It asks the guest to mount
// the volume with the UUID records holds for the slot, with first_mount
// set until a mount of that volume has succeeded, and records the first
// success at now.
func SendMount(records MountRecords, now func() time.Time, log *slog.Logger) MountHook {
	return func(ctx context.Context, slot vm.Slot, _ uint64, link *guestlink.Link) error {
		name := vmbundle.Name(string(slot))
		rec, found, err := records.DataDiskRecord(ctx, name)
		switch {
		case err != nil:
			return &MountError{Rule: RuleMountRecord, Message: fmt.Sprintf("the %s VM's data disk record can't be read: %v", slot, err)}
		case !found || !rec.Formatted:
			return &MountError{Rule: RuleMountRecord, Message: fmt.Sprintf("the %s VM has no formatted data disk recorded", slot)}
		}
		first := rec.FirstMounted.IsZero()
		if err := link.MountData(ctx, rec.VolumeUUID, first, log.With(slog.String("slot", string(slot)))); err != nil {
			return err
		}
		if first {
			// The guest claims the root again on the next mount when this
			// fails, which the check after it passes either way.
			if err := records.MarkDataDiskMounted(ctx, name, rec.VolumeUUID, now()); err != nil {
				log.LogAttrs(ctx, slog.LevelError, "data disk first mount not recorded",
					slog.String("slot", string(slot)), slog.String("error", err.Error()))
			}
		}
		return nil
	}
}

// MountState is the state of a VM's data mount.
type MountState string

// The states of the data mount, kept per VM generation.
const (
	// MountPending: no mount of this generation has been answered yet.
	MountPending MountState = "pending"
	// MountMounted: the guest mounted the data volume.
	MountMounted MountState = "mounted"
	// MountRefused: the last mount of this generation was refused. Every
	// session start is refused until a later one succeeds.
	MountRefused MountState = "refused"
)

// DataMount is the result of a VM generation's last data mount, for
// `wb vm status` and a session start to explain
// (NFR06-explained-refusals).
type DataMount struct {
	State MountState
	// Rule is the rule of a refusal, else "".
	Rule string
	// Message says what was refused and what to do, else "".
	Message string
}

// mount calls the mount hook, keeps its result in the status, and
// reports whether the link is kept: a call left unanswered, or a link
// that dropped under it, reconnects, and the next link asks again. Any
// other refusal keeps the link, and the VM keeps running.
func (r *run) mount(l *guestlink.Link) bool {
	hook := r.ls.cfg.MountHook
	if hook == nil {
		return true
	}
	err := hook(r.ctx, r.slot, r.generation, l)
	if err == nil {
		r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.DataMount = DataMount{State: MountMounted} })
		return true
	}
	if r.ctx.Err() != nil {
		return true
	}
	select {
	case <-l.Down():
		r.dropped()
		return false
	default:
	}
	rule := mountRule(err)
	r.vc.log.LogAttrs(r.ctx, slog.LevelWarn, "data mount refused",
		slog.Uint64("generation", r.generation), slog.String("rule", rule),
		slog.String("decision", "refuse"), slog.String("reason", err.Error()))
	dm := DataMount{State: MountRefused, Rule: rule, Message: mountMessage(r.slot, rule)}
	r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.DataMount = dm })
	return !reconnects(rule)
}

// reconnects reports whether a mount refused with rule drops the link,
// so the next link asks again: no answer, or a call that failed with
// UNAVAILABLE. Neither says anything about the volume.
func reconnects(rule string) bool {
	return rule == guestlink.RuleMountTimeout || rule == guestlink.RuleMountUnavailable
}

// mountRule names the rule of a failed mount hook.
func mountRule(err error) string {
	if r, ok := errors.AsType[*guestlink.MountRefusal](err); ok {
		return r.Rule
	}
	if e, ok := errors.AsType[*MountError](err); ok {
		return e.Rule
	}
	return guestlink.RuleReplyInvalid
}

// mountMessage says what a refused mount means and what to do. It holds
// only text wb-hostd made: the rule is one of a fixed list. Only a
// refusal about the volume advises wb vm reset-data, which deletes the
// homes (NFR06-explained-refusals).
func mountMessage(slot vm.Slot, rule string) string {
	switch {
	case reconnects(rule):
		return fmt.Sprintf("wb-guestd in the %s VM didn't answer the data mount (%s), so no session starts in it yet. "+
			"wb-hostd asks again when the link reconnects. If this stays, stop the VM with wb vm stop %s and start it again.",
			slot, rule, slot)
	case rule == RuleMountRecord:
		return fmt.Sprintf("wb-hostd has no usable data disk record for the %s VM (%s), so no session starts in it. "+
			"The wb-hostd log has the cause. Stop the VM with wb vm stop %s and start it again.",
			slot, rule, slot)
	}
	return fmt.Sprintf("The %s VM's data volume was refused (%s), so no session starts in it. "+
		"Stop the VM with wb vm stop %s, then run wb vm reset-data %s, which deletes every project's home on that disk.",
		slot, rule, slot, slot)
}

// ErrSessionRefused is wrapped by every refusal of CheckSessionStart.
var ErrSessionRefused = errors.New("guestconn: session start refused")

// SessionRefusal is a refused session start, with the rule and what to
// do.
type SessionRefusal struct {
	Rule    string
	Message string
}

func (e *SessionRefusal) Error() string { return e.Message + " (" + e.Rule + ")" }

// Unwrap returns ErrSessionRefused.
func (*SessionRefusal) Unwrap() error { return ErrSessionRefused }

// The rules of CheckSessionStart besides a refused mount's own.
const (
	// RuleSessionNoLink: the VM's run of generation has no link.
	RuleSessionNoLink = "session-no-link"
	// RuleSessionMountPending: the data mount of the generation hasn't
	// been answered yet.
	RuleSessionMountPending = "session-mount-pending"
)

// CheckSessionStart refuses a session start in slot's run of generation
// unless its link is up and its data mount succeeded
// (S06-05-disks, "Mounting the data volume"; SEC08-proj-isolation). A
// refused mount refuses with the mount's rule and message, which says
// what to do (NFR06-explained-refusals).
func (ls *Links) CheckSessionStart(slot vm.Slot, generation uint64) error {
	s := ls.Status(slot)
	var r *SessionRefusal
	switch {
	case s.Generation != generation || s.State != StateLinked:
		r = &SessionRefusal{Rule: RuleSessionNoLink, Message: fmt.Sprintf(
			"wb-hostd has no link to wb-guestd in the %s VM. Wait for it, or stop and start the VM.", slot)}
	case s.DataMount.State == MountRefused:
		r = &SessionRefusal{Rule: s.DataMount.Rule, Message: s.DataMount.Message}
	case s.DataMount.State != MountMounted:
		r = &SessionRefusal{Rule: RuleSessionMountPending, Message: fmt.Sprintf(
			"the %s VM's data volume isn't mounted yet. Try again in a minute.", slot)}
	default:
		return nil
	}
	ls.vm(slot).log.LogAttrs(context.Background(), slog.LevelWarn, "session start refused",
		slog.Uint64("generation", generation), slog.String("rule", r.Rule), slog.String("decision", "refuse"))
	return r
}
