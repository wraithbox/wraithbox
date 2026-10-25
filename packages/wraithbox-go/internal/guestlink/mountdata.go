package guestlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

const (
	// MountTimeout is how long wb-guestd has to answer MountData: its
	// wait of 30 s for the volume, and the unmount and the mount after it
	// (S06-05-disks, "The mount").
	MountTimeout = 45 * time.Second

	// MountMaxRecvMsgSize is the largest MountData answer the host
	// decodes. A success is an empty message. A refusal's status is in
	// the trailers, which the connection caps at 4 KiB too
	// (maxHeaderListSize).
	MountMaxRecvMsgSize = 4 << 10

	// maxRuleLen is the longest rule the host reads from a refusal.
	maxRuleLen = 64

	// guestDomain is the ErrorInfo domain of wb-guestd's refusals.
	guestDomain = "wraithbox.guest.v1"
)

// The host's rules of a MountData answer it doesn't take.
const (
	// RuleReplyInvalid: the answer is an error other than a refusal with
	// one of MountRules, or didn't decode, or is over the receive cap.
	RuleReplyInvalid = "guest-reply-invalid"
	// RuleMountTimeout: no answer within MountTimeout.
	RuleMountTimeout = "guest-mount-timeout"
	// RuleMountUnavailable: the call failed with UNAVAILABLE, as when the
	// connection drops under it. The link reconnects and asks again, as
	// after a time call that fails so.
	RuleMountUnavailable = "guest-mount-unavailable"
)

// MountRules are the rules a refusal of MountData may carry, as its
// ErrorInfo reason (proto/wraithbox/guest/v1, MountData). Any other is
// RuleReplyInvalid.
var MountRules = []string{
	"data-volume-missing",
	"data-volume-encrypted",
	"data-volume-mount-failed",
	"data-mount-point",
	"data-mount-flags",
	"data-mount-volume",
	"data-mount-root",
}

// ruleForm is the form of a rule: lowercase letters, digits and dashes.
var ruleForm = regexp.MustCompile(`^[a-z0-9-]+$`)

// MountRefusal is the error of a refused MountData: a rule of
// MountRules the guest sent, or one of the host's: RuleReplyInvalid,
// RuleMountTimeout or RuleMountUnavailable. Its message holds only text
// the host made, and guest text escaped and cut.
type MountRefusal struct {
	Rule string
	msg  string
}

func (r *MountRefusal) Error() string { return "guest data mount refused (" + r.Rule + "): " + r.msg }

// MountData asks wb-guestd to mount the data volume with uuid at the
// home path, or to check it there (S06-05-disks, "Mounting the data
// volume"). first asks it to make the mount root owned by root:wheel,
// for the volume's first mount. It returns nil when the guest mounted
// it, a *MountRefusal, logged with its rule, when the answer is a
// refusal or one the host doesn't take, and an error that wraps
// context.Canceled when the host cancels ctx first.
func (l *Link) MountData(ctx context.Context, uuid string, first bool, log *slog.Logger) error {
	callCtx, cancel := context.WithTimeout(ctx, MountTimeout)
	defer cancel()
	_, err := l.Client().MountData(callCtx, &guestv1.MountDataRequest{VolumeUuid: uuid, FirstMount: first},
		grpc.MaxCallRecvMsgSize(MountMaxRecvMsgSize))
	attrs := []slog.Attr{slog.String("volume_uuid", uuid), slog.Bool("first_mount", first)}
	if err == nil {
		log.LogAttrs(ctx, slog.LevelInfo, "guest data mount accepted", append(attrs, slog.String("decision", "accept"))...)
		return nil
	}
	if errors.Is(callCtx.Err(), context.Canceled) {
		log.LogAttrs(ctx, slog.LevelInfo, "guest data mount abandoned", append(attrs, slog.String("decision", "close"))...)
		return fmt.Errorf("guestlink: data mount abandoned by the host: %w", callCtx.Err())
	}
	var r *MountRefusal
	if timedOut(callCtx, err) {
		r = &MountRefusal{Rule: RuleMountTimeout, msg: "no answer within " + MountTimeout.String()}
	} else if st := status.Convert(err); st.Code() == codes.Unavailable {
		r = &MountRefusal{Rule: RuleMountUnavailable, msg: "the call failed: " + guestText(st.Message())}
	} else {
		r = mountRefusalOf(st)
	}
	log.LogAttrs(ctx, slog.LevelWarn, "guest data mount refused",
		append(attrs, slog.String("rule", r.Rule), slog.String("decision", "refuse"), slog.String("reason", r.msg))...)
	return r
}

// timedOut reports whether the call failed by its deadline on the host's
// clock. The guest's server ends the call at the deadline it was sent,
// and that answer can arrive before the host's own timer has marked
// callCtx done. A DEADLINE_EXCEEDED before the deadline is the guest's
// own, which mountRefusalOf takes as guest input.
func timedOut(callCtx context.Context, err error) bool {
	if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return true
	}
	deadline, ok := callCtx.Deadline()
	return ok && status.Code(err) == codes.DeadlineExceeded && !time.Now().Before(deadline)
}

// mountRefusalOf reads a MountData error status. Only FAILED_PRECONDITION
// with exactly one detail, an ErrorInfo in wb-guestd's domain whose
// reason is a rule of MountRules, is the guest's refusal, under that
// rule. Every other status is RuleReplyInvalid: a code the guest sent is
// guest input, whichever code it is.
func mountRefusalOf(st *status.Status) *MountRefusal {
	msg := st.Code().String() + ": " + guestText(st.Message())
	invalid := func(why string) *MountRefusal { return &MountRefusal{Rule: RuleReplyInvalid, msg: why + "; " + msg} }
	if st.Code() != codes.FailedPrecondition {
		return invalid("not a refusal")
	}
	details := st.Details()
	if len(details) != 1 {
		return invalid(fmt.Sprintf("%d details, want 1", len(details)))
	}
	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok {
		return invalid("the detail isn't an ErrorInfo")
	}
	rule := info.GetReason()
	switch {
	case info.GetDomain() != guestDomain:
		return invalid("domain " + guestText(info.GetDomain()))
	case len(rule) > maxRuleLen || !ruleForm.MatchString(rule):
		return invalid("rule " + guestText(rule) + " isn't of the form [a-z0-9-]{1,64}")
	case !slices.Contains(MountRules, rule):
		return invalid("unknown rule " + guestText(rule))
	}
	return &MountRefusal{Rule: rule, msg: msg}
}
