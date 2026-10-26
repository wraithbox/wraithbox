package guestlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// The bounds of the project user calls (proto/wraithbox/guest/v1,
// EnsureUsers and RemoveUser; S04-08-records-and-bounds, "Host work the
// guest can cause": values in code).
const (
	// MaxUsersPerRequest is the most users in one EnsureUsers request. It
	// bounds the answer: one result per user.
	MaxUsersPerRequest = 128
	// UsersMaxRecvMsgSize is the largest EnsureUsers answer the host
	// decodes. MaxUsersPerRequest results with 64-byte rules take under
	// 10 KiB.
	UsersMaxRecvMsgSize = 16 << 10
	// RemoveMaxRecvMsgSize is the largest RemoveUser answer the host
	// decodes.
	RemoveMaxRecvMsgSize = 4 << 10
	// MaxProcessesLeft is the highest count of processes left the guest
	// reports (S06-10-leftover-processes, "Kill IDs and records").
	MaxProcessesLeft = 1001
	// UsersTimeout is how long wb-guestd has to answer one EnsureUsers
	// request, which may make MaxUsersPerRequest users.
	UsersTimeout = 60 * time.Second
	// RemoveTimeout is how long wb-guestd has to answer one RemoveUser,
	// which stops the user's processes and removes its home.
	RemoveTimeout = 60 * time.Second
)

// The host's rules of the project user calls.
const (
	// RuleUsersReplyInvalid: an answer that breaks a bound of the
	// contract, or is over the receive cap. The whole answer is dropped.
	RuleUsersReplyInvalid = "guest-users-reply-invalid"
	// RuleUsersFailed: the call failed with an error status the guest
	// sent, such as UNIMPLEMENTED from a wb-guestd without the call.
	RuleUsersFailed = "guest-users-failed"
	// RuleUsersUnanswered: no answer within the call's timeout, or the
	// call failed with UNAVAILABLE, as when the connection drops under
	// it. The link reconnects and asks again.
	RuleUsersUnanswered = "guest-users-unanswered"
	// RuleUserRefused: an accepted answer refuses the user, or has no
	// result for it. The guest's own rule is logged as guest_rule.
	RuleUserRefused = "guest-user-refused"
	// RuleUserProcessesLeft: an accepted removal answer says processes of
	// the user are left. The removal stays pending.
	RuleUserProcessesLeft = "guest-user-processes-left"
	// RuleUserRemovalRefused: an accepted removal answer refuses the
	// removal. The guest's own rule is logged as guest_rule, and the
	// removal stays pending.
	RuleUserRemovalRefused = "guest-user-removal-refused"
)

// UsersRefusal is a project user call whose whole answer the host
// doesn't take, under one of the host's rules. Its message holds only
// text the host made, and guest text escaped and cut.
type UsersRefusal struct {
	Rule string
	msg  string
}

func (r *UsersRefusal) Error() string { return "guest user call refused (" + r.Rule + "): " + r.msg }

// UserResult is an accepted result of EnsureUsers for one UID: OK, or
// refused with the guest's rule, which the host only logs, escaped.
type UserResult struct {
	OK   bool
	Rule string
}

// SplitUsers splits users into requests that each fit the link's send
// cap, which is wb-guestd's receive cap, with at most MaxUsersPerRequest
// users each.
func SplitUsers(users []*guestv1.ProjectUser) [][]*guestv1.ProjectUser {
	return splitUsers(users, guestLimits.maxMsgSize)
}

func splitUsers(users []*guestv1.ProjectUser, budget int) [][]*guestv1.ProjectUser {
	var out [][]*guestv1.ProjectUser
	var cur []*guestv1.ProjectUser
	size := 0
	for _, u := range users {
		// A repeated field's element costs its tag, its length and its
		// bytes: under 1 + 3 + n for any user under 2 MiB.
		n := proto.Size(u) + 4
		if len(cur) > 0 && (len(cur) == MaxUsersPerRequest || size+n > budget) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, u)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// EnsureUsers sends one EnsureUsers request of users, at most
// MaxUsersPerRequest of them, and returns the results it accepts, by
// UID. A UID without a result has none: the caller refuses it. A whole
// answer the host doesn't take is a *UsersRefusal, logged with its rule
// (SEC11-root-gains-nothing). Each accepted refusal is logged with the
// guest's rule, escaped. An accepted answer is logged with the number of
// users and how long the guest took.
func (l *Link) EnsureUsers(ctx context.Context, users []*guestv1.ProjectUser, log *slog.Logger) (map[uint32]UserResult, error) {
	if len(users) == 0 || len(users) > MaxUsersPerRequest {
		return nil, fmt.Errorf("guestlink: %d users in one request, want 1 to %d", len(users), MaxUsersPerRequest)
	}
	callCtx, cancel := context.WithTimeout(ctx, UsersTimeout)
	defer cancel()
	start := time.Now()
	resp, err := l.Client().EnsureUsers(callCtx, &guestv1.EnsureUsersRequest{Users: users},
		grpc.MaxCallRecvMsgSize(UsersMaxRecvMsgSize))
	attrs := []slog.Attr{slog.Int("users", len(users)), slog.Duration("took", time.Since(start))}
	if err != nil {
		return nil, logUsersRefusal(ctx, log, "guest users refused", attrs, callRefusalOf(ctx, callCtx, err))
	}
	results, r := CheckEnsureUsers(users, resp)
	if r != nil {
		return nil, logUsersRefusal(ctx, log, "guest users refused", attrs, r)
	}
	log.LogAttrs(ctx, slog.LevelInfo, "guest users answered", append(attrs, slog.String("decision", "accept"))...)
	for _, u := range users {
		logResult(ctx, log, u.GetUid(), results)
	}
	return results, nil
}

// logResult logs the result for uid: accepted when it is OK, and refused
// with RuleUserRefused otherwise.
func logResult(ctx context.Context, log *slog.Logger, uid uint32, results map[uint32]UserResult) {
	res, ok := results[uid]
	if ok && res.OK {
		log.LogAttrs(ctx, slog.LevelInfo, "guest user ensured", slog.Uint64("uid", uint64(uid)), slog.String("decision", "accept"))
		return
	}
	// CheckEnsureUsers took the rule only in the form [a-z0-9-]{1,64}.
	guestRule := res.Rule
	if !ok {
		guestRule = "(no result)"
	}
	log.LogAttrs(ctx, slog.LevelWarn, "guest user refused", slog.Uint64("uid", uint64(uid)),
		slog.String("rule", RuleUserRefused), slog.String("guest_rule", guestRule), slog.String("decision", "refuse"))
}

// CheckEnsureUsers checks an EnsureUsers answer against the request's
// users. It takes the answer only when every result is for a UID of the
// request, at most one per UID, and keeps its result's bounds: OK with
// no rule, or REFUSED with a rule of 1 to 64 bytes of [a-z0-9-]. Any
// other answer is a *UsersRefusal with RuleUsersReplyInvalid, and no
// result of it is taken.
func CheckEnsureUsers(sent []*guestv1.ProjectUser, resp *guestv1.EnsureUsersResponse) (map[uint32]UserResult, *UsersRefusal) {
	want := make(map[uint32]bool, len(sent))
	for _, u := range sent {
		want[u.GetUid()] = true
	}
	results := resp.GetResults()
	if len(results) > len(sent) {
		return nil, invalidUsers(fmt.Sprintf("%d results for %d users", len(results), len(sent)))
	}
	out := make(map[uint32]UserResult, len(results))
	for i, res := range results {
		uid := res.GetUid()
		if !want[uid] {
			return nil, invalidUsers(fmt.Sprintf("result %d is for UID %d, which wasn't sent", i, uid))
		}
		if _, dup := out[uid]; dup {
			return nil, invalidUsers(fmt.Sprintf("two results for UID %d", uid))
		}
		r, ok := userResult(res)
		if !ok {
			return nil, invalidUsers(fmt.Sprintf("UID %d has the result %d with the rule %s",
				uid, res.GetResult(), guestText(res.GetRule())))
		}
		out[uid] = r
	}
	return out, nil
}

// userResult reads one result, and reports whether it keeps its bounds.
func userResult(res *guestv1.EnsureUserResult) (UserResult, bool) {
	rule := res.GetRule()
	switch res.GetResult() {
	case guestv1.EnsureResult_ENSURE_RESULT_OK:
		return UserResult{OK: true}, rule == ""
	case guestv1.EnsureResult_ENSURE_RESULT_REFUSED:
		return UserResult{Rule: rule}, validRule(rule)
	case guestv1.EnsureResult_ENSURE_RESULT_UNSPECIFIED:
	}
	// UNSPECIFIED, and any value this build doesn't know.
	return UserResult{}, false
}

// RemoveOutcome is an accepted RemoveUser answer.
type RemoveOutcome struct {
	Result guestv1.RemoveResult
	// ProcessesLeft is 1 to MaxProcessesLeft with PROCESSES_LEFT, else 0.
	ProcessesLeft uint32
	// Rule is the guest's rule with REFUSED, else "".
	Rule string
}

// RemoveUser sends one RemoveUser request and returns the answer it
// accepts. Only REMOVED clears the pending removal, which the caller
// does. A whole answer the host doesn't take is a *UsersRefusal, logged
// with its rule. An accepted answer is logged with its result.
func (l *Link) RemoveUser(ctx context.Context, req *guestv1.RemoveUserRequest, log *slog.Logger) (RemoveOutcome, error) {
	callCtx, cancel := context.WithTimeout(ctx, RemoveTimeout)
	defer cancel()
	resp, err := l.Client().RemoveUser(callCtx, req, grpc.MaxCallRecvMsgSize(RemoveMaxRecvMsgSize))
	attrs := []slog.Attr{slog.String("project", req.GetProjectId()), slog.Uint64("uid", uint64(req.GetUid()))}
	if err != nil {
		return RemoveOutcome{}, logUsersRefusal(ctx, log, "guest user removal refused", attrs, callRefusalOf(ctx, callCtx, err))
	}
	out, r := CheckRemoveUser(req, resp)
	if r != nil {
		return RemoveOutcome{}, logUsersRefusal(ctx, log, "guest user removal refused", attrs, r)
	}
	attrs = append(attrs, slog.String("result", out.Result.String()))
	if out.Result == guestv1.RemoveResult_REMOVE_RESULT_REMOVED {
		log.LogAttrs(ctx, slog.LevelInfo, "guest user removed", append(attrs, slog.String("decision", "accept"))...)
		return out, nil
	}
	if out.Result == guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT {
		attrs = append(attrs, slog.String("rule", RuleUserProcessesLeft), slog.Uint64("processes_left", uint64(out.ProcessesLeft)))
	} else {
		// CheckRemoveUser took the rule only in the form [a-z0-9-]{1,64}.
		attrs = append(attrs, slog.String("rule", RuleUserRemovalRefused), slog.String("guest_rule", out.Rule))
	}
	log.LogAttrs(ctx, slog.LevelWarn, "guest user not removed", append(attrs, slog.String("decision", "keep"))...)
	return out, nil
}

// CheckRemoveUser checks a RemoveUser answer against its request. It
// takes the answer only for the request's project ID and UID, with a
// known result and its bounds: REMOVED with no count and no rule,
// PROCESSES_LEFT with a count of 1 to MaxProcessesLeft and no rule, and
// REFUSED with no count and a rule of 1 to 64 bytes of [a-z0-9-]. Any
// other answer is a *UsersRefusal with RuleUsersReplyInvalid.
func CheckRemoveUser(req *guestv1.RemoveUserRequest, resp *guestv1.RemoveUserResponse) (RemoveOutcome, *UsersRefusal) {
	switch {
	case resp.GetProjectId() != req.GetProjectId():
		return RemoveOutcome{}, invalidUsers("the answer is for project " + guestText(resp.GetProjectId()))
	case resp.GetUid() != req.GetUid():
		return RemoveOutcome{}, invalidUsers(fmt.Sprintf("the answer is for UID %d", resp.GetUid()))
	}
	n, rule := resp.GetProcessesLeft(), resp.GetRule()
	out := RemoveOutcome{Result: resp.GetResult(), ProcessesLeft: n, Rule: rule}
	switch resp.GetResult() {
	case guestv1.RemoveResult_REMOVE_RESULT_REMOVED:
		if n == 0 && rule == "" {
			return out, nil
		}
	case guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT:
		if n >= 1 && n <= MaxProcessesLeft && rule == "" {
			return out, nil
		}
	case guestv1.RemoveResult_REMOVE_RESULT_REFUSED:
		if n == 0 && validRule(rule) {
			return out, nil
		}
	case guestv1.RemoveResult_REMOVE_RESULT_UNSPECIFIED:
	}
	return RemoveOutcome{}, invalidUsers(fmt.Sprintf("result %d with %d processes left and the rule %s",
		resp.GetResult(), n, guestText(rule)))
}

func invalidUsers(msg string) *UsersRefusal {
	return &UsersRefusal{Rule: RuleUsersReplyInvalid, msg: msg}
}

// validRule reports whether rule is in the form of a rule: 1 to 64
// bytes of [a-z0-9-].
func validRule(rule string) bool { return len(rule) <= maxRuleLen && ruleForm.MatchString(rule) }

// callRefusalOf names the rule of a failed call. The status code comes
// from the guest's server or from grpc-go, and the host can't tell
// which. It only picks between a reconnect and a refusal, and both leave
// the users refused and the removals pending.
func callRefusalOf(ctx, callCtx context.Context, err error) *UsersRefusal {
	st := status.Convert(err)
	msg := st.Code().String() + ": " + guestText(st.Message())
	switch {
	case ctx.Err() != nil:
		return &UsersRefusal{Rule: RuleUsersUnanswered, msg: "abandoned by the host: " + msg}
	case timedOut(callCtx, err), st.Code() == codes.Unavailable:
		return &UsersRefusal{Rule: RuleUsersUnanswered, msg: msg}
	case st.Code() == codes.ResourceExhausted:
		// grpc-go's own code for an answer over the receive cap.
		return invalidUsers(msg)
	}
	return &UsersRefusal{Rule: RuleUsersFailed, msg: msg}
}

func logUsersRefusal(ctx context.Context, log *slog.Logger, what string, attrs []slog.Attr, r *UsersRefusal) error {
	log.LogAttrs(ctx, slog.LevelWarn, what,
		append(attrs, slog.String("rule", r.Rule), slog.String("decision", "refuse"), slog.String("reason", r.msg))...)
	return r
}

// IsUnanswered reports whether err is a project user call left without
// an answer, after which the link reconnects.
func IsUnanswered(err error) bool {
	r, ok := errors.AsType[*UsersRefusal](err)
	return ok && r.Rule == RuleUsersUnanswered
}
