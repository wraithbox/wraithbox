package guestlink

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/grpc"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// The bounds of the KillUser call (proto/wraithbox/guest/v1, KillUser;
// S04-08-records-and-bounds, "Leftover-process limits": values in code).
const (
	// KillMaxRecvMsgSize is the largest KillUser answer the host decodes.
	// The answer holds a number, a 32-byte ID, a result and a count.
	KillMaxRecvMsgSize = 4 << 10
	// KillTimeout is how long wb-guestd has to answer one KillUser. Its
	// kill loop runs at most 30 s (S13-01-process-hardening). A call
	// left unanswered keeps the kill pending until its deadline, and the
	// link reconnects.
	KillTimeout = 60 * time.Second
)

// KillOutcome is a kill result that keeps the bounds of the contract,
// from a KillUser answer or a CleanupRecord. It is still guest input:
// the caller takes it only for a kill it sent (internal/hostd/kills).
type KillOutcome struct {
	KillID    uint64
	ProjectID string
	// Clean is true with KILL_RESULT_CLEAN.
	Clean bool
	// ProcessesLeft is 1 to MaxProcessesLeft when Clean is false, else 0.
	ProcessesLeft uint32
}

// CountLabel is the count of processes left as the host shows it:
// MaxProcessesLeft reads "more than 1000" (S06-10-leftover-processes,
// "A result of processes left"), and 0, a missing result, "unknown".
func CountLabel(n uint32) string {
	switch {
	case n == 0:
		return "unknown"
	case n >= MaxProcessesLeft:
		return "more than " + strconv.Itoa(MaxProcessesLeft-1)
	}
	return strconv.FormatUint(uint64(n), 10)
}

// CheckKillUser checks the bounds of a KillUser answer. It doesn't
// compare the IDs with the request's: the kill tracker does that, and
// logs a mismatch with its own rule.
func CheckKillUser(resp *guestv1.KillUserResponse) (KillOutcome, *UsersRefusal) {
	return checkKill(resp.GetKillId(), resp.GetProjectId(), resp.GetResult(), resp.GetProcessesLeft())
}

// CheckCleanupRecord checks the bounds of a cleanup record of the
// reconnect list (S06-11-reconnect-and-recovery, "The list").
func CheckCleanupRecord(rec *guestv1.CleanupRecord) (KillOutcome, *UsersRefusal) {
	return checkKill(rec.GetKillId(), rec.GetProjectId(), rec.GetResult(), rec.GetProcessesLeft())
}

// checkKill takes a result only with a known value and its bounds: CLEAN
// with a count of 0, PROCESSES_LEFT with a count of 1 to
// MaxProcessesLeft. Any other is a *UsersRefusal with
// RuleUsersReplyInvalid. A kill ID of 0 is never sent, so it is refused
// here too.
func checkKill(id uint64, projectID string, result guestv1.KillResult, n uint32) (KillOutcome, *UsersRefusal) {
	out := KillOutcome{KillID: id, ProjectID: projectID, ProcessesLeft: n}
	if id == 0 {
		return KillOutcome{}, invalidUsers("the kill ID is 0")
	}
	switch result {
	case guestv1.KillResult_KILL_RESULT_CLEAN:
		if n == 0 {
			out.Clean = true
			return out, nil
		}
	case guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT:
		if n >= 1 && n <= MaxProcessesLeft {
			return out, nil
		}
	case guestv1.KillResult_KILL_RESULT_UNSPECIFIED:
	}
	// UNSPECIFIED, a value this build doesn't know, or a count out of
	// its bounds.
	return KillOutcome{}, invalidUsers(fmt.Sprintf("kill result %d with %d processes left", result, n))
}

// KillUser sends one KillUser request and returns the answer when it
// keeps the bounds of the contract. A failed call or an answer out of
// bounds is a *UsersRefusal, logged with its rule; the kill then stays
// pending, and fails closed at its deadline (S06-10-leftover-processes,
// "Failing closed").
func (l *Link) KillUser(ctx context.Context, req *guestv1.KillUserRequest, log *slog.Logger) (KillOutcome, error) {
	callCtx, cancel := context.WithTimeout(ctx, KillTimeout)
	defer cancel()
	resp, err := l.Client().KillUser(callCtx, req, grpc.MaxCallRecvMsgSize(KillMaxRecvMsgSize))
	attrs := []slog.Attr{
		slog.Uint64("kill_id", req.GetKillId()), slog.String("project", req.GetProjectId()),
		slog.Uint64("uid", uint64(req.GetUid())),
	}
	if err != nil {
		return KillOutcome{}, logUsersRefusal(ctx, log, "guest kill answer refused", attrs, callRefusalOf(ctx, callCtx, err))
	}
	out, r := CheckKillUser(resp)
	if r != nil {
		return KillOutcome{}, logUsersRefusal(ctx, log, "guest kill answer refused", attrs, r)
	}
	return out, nil
}
