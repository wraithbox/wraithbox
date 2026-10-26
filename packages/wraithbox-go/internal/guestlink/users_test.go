package guestlink

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// usersGuest passes Hello, then answers EnsureUsers and RemoveUser with
// the scripted answers, or with err when it is set.
type usersGuest struct {
	fakeGuest

	ensure *guestv1.EnsureUsersResponse
	remove *guestv1.RemoveUserResponse
	err    error
	block  bool
}

func (g *usersGuest) EnsureUsers(ctx context.Context, _ *guestv1.EnsureUsersRequest) (*guestv1.EnsureUsersResponse, error) {
	if g.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return g.ensure, g.err
}

func (g *usersGuest) RemoveUser(context.Context, *guestv1.RemoveUserRequest) (*guestv1.RemoveUserResponse, error) {
	return g.remove, g.err
}

func usersLinked(t *testing.T, g *usersGuest) *Link {
	t.Helper()
	g.fakeGuest = *answer(own, pkg)
	l, err := Hello(t.Context(), serve(t, g), own, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func testUser(uid uint32) *guestv1.ProjectUser {
	return &guestv1.ProjectUser{
		ProjectId: strings.Repeat("a", 32), UserName: "wbp10000", Uid: uid, Gid: uid, GroupName: "wbp10000",
		UserGeneratedUid: testUUID, GroupGeneratedUid: testUUID, HomePath: "/private/var/wbhomes/wbp10000",
	}
}

func ok(uid uint32) *guestv1.EnsureUserResult {
	return &guestv1.EnsureUserResult{Uid: uid, Result: guestv1.EnsureResult_ENSURE_RESULT_OK}
}

func refused(uid uint32, rule string) *guestv1.EnsureUserResult {
	return &guestv1.EnsureUserResult{Uid: uid, Result: guestv1.EnsureResult_ENSURE_RESULT_REFUSED, Rule: rule}
}

func results(r ...*guestv1.EnsureUserResult) *guestv1.EnsureUsersResponse {
	return &guestv1.EnsureUsersResponse{Results: r}
}

// TestEnsureUsersAccepts: OK and refused results for sent UIDs are
// taken, a UID without a result has none, and each refusal is logged
// with the host's rule and the guest's.
func TestEnsureUsersAccepts(t *testing.T) {
	g := &usersGuest{ensure: results(ok(10000), refused(10001, "user-exists"))}
	l := usersLinked(t, g)
	var logs logCapture
	got, err := l.EnsureUsers(t.Context(), []*guestv1.ProjectUser{testUser(10000), testUser(10001), testUser(10002)}, logs.logger())
	if err != nil {
		t.Fatalf("EnsureUsers: %v", err)
	}
	if !got[10000].OK || got[10001].OK || got[10001].Rule != "user-exists" {
		t.Fatalf("results %v", got)
	}
	if _, has := got[10002]; has {
		t.Fatalf("a result for 10002, which had none: %v", got)
	}
	if rec := logs.last(t); rec["rule"] != RuleUserRefused || rec["guest_rule"] != "(no result)" || rec["uid"] != float64(10002) {
		t.Fatalf("log %v, want 10002 refused for no result", rec)
	}
	if !strings.Contains(logs.buf.String(), `"msg":"guest users answered","users":3,"took":`) {
		t.Fatalf("log %s, want the answer with its users and took", logs.buf.String())
	}
}

// TestEnsureUsersInvalid: every answer outside the contract is dropped
// whole with RuleUsersReplyInvalid and logged (SEC11-root-gains-nothing).
func TestEnsureUsersInvalid(t *testing.T) {
	big := results(ok(10000))
	big.ProtoReflect().SetUnknown(protowire.AppendBytes(
		protowire.AppendTag(nil, 99, protowire.BytesType), make([]byte, UsersMaxRecvMsgSize)))
	cases := []struct {
		name string
		resp *guestv1.EnsureUsersResponse
	}{
		{"unknown UID", results(ok(10000), ok(10005))},
		{"duplicate UID", results(ok(10000), refused(10000, "user-exists"))},
		{"more results than users", results(ok(10000), ok(10001), ok(10000))},
		{"unspecified", results(&guestv1.EnsureUserResult{Uid: 10000})},
		{"unknown result", results(&guestv1.EnsureUserResult{Uid: 10000, Result: 7})},
		{"OK with a rule", results(&guestv1.EnsureUserResult{Uid: 10000, Result: guestv1.EnsureResult_ENSURE_RESULT_OK, Rule: "x"})},
		{"refused without a rule", results(refused(10000, ""))},
		{"65-byte rule", results(refused(10000, strings.Repeat("a", 65)))},
		{"uppercase rule", results(refused(10000, "User-Exists"))},
		{"control byte", results(refused(10000, "user\x1b[2J"))},
		{"answer over 16 KiB", big},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := usersLinked(t, &usersGuest{ensure: c.resp})
			var logs logCapture
			got, err := l.EnsureUsers(t.Context(), []*guestv1.ProjectUser{testUser(10000), testUser(10001)}, logs.logger())
			requireUsersRefused(t, err, RuleUsersReplyInvalid)
			if got != nil {
				t.Fatalf("results %v from a dropped answer", got)
			}
			if rec := logs.last(t); rec["rule"] != RuleUsersReplyInvalid || rec["decision"] != "refuse" {
				t.Fatalf("log %v", rec)
			}
		})
	}
}

// TestEnsureUsersCallFailures: an error status is RuleUsersFailed, and
// UNAVAILABLE or no answer is RuleUsersUnanswered, after which the link
// reconnects.
func TestEnsureUsersCallFailures(t *testing.T) {
	cases := []struct {
		name string
		g    *usersGuest
		rule string
	}{
		{"unimplemented", &usersGuest{err: status.Error(codes.Unimplemented, "unknown method")}, RuleUsersFailed},
		{"internal", &usersGuest{err: status.Error(codes.Internal, "\x1b[2J")}, RuleUsersFailed},
		{"unavailable", &usersGuest{err: status.Error(codes.Unavailable, "closing")}, RuleUsersUnanswered},
		{"guest deadline status", &usersGuest{err: status.Error(codes.DeadlineExceeded, "late")}, RuleUsersFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := usersLinked(t, c.g)
			_, err := l.EnsureUsers(t.Context(), []*guestv1.ProjectUser{testUser(10000)}, slog.New(slog.DiscardHandler))
			requireUsersRefused(t, err, c.rule)
			if IsUnanswered(err) != (c.rule == RuleUsersUnanswered) {
				t.Fatalf("IsUnanswered = %t", IsUnanswered(err))
			}
			if strings.ContainsRune(err.Error(), 0x1b) {
				t.Fatalf("error %q holds a raw control byte", err)
			}
		})
	}
}

// TestEnsureUsersBounds: the host never sends an empty request or one
// over MaxUsersPerRequest.
func TestEnsureUsersBounds(t *testing.T) {
	l := usersLinked(t, &usersGuest{ensure: results()})
	for _, n := range []uint32{0, MaxUsersPerRequest + 1} {
		var users []*guestv1.ProjectUser
		for i := range n {
			users = append(users, testUser(10000+i))
		}
		if _, err := l.EnsureUsers(t.Context(), users, slog.New(slog.DiscardHandler)); err == nil {
			t.Fatalf("%d users sent", n)
		}
	}
}

// TestSplitUsers: every request fits the budget and MaxUsersPerRequest,
// and the requests together hold every user once, in order.
func TestSplitUsers(t *testing.T) {
	var users []*guestv1.ProjectUser
	for i := range uint32(1000) {
		users = append(users, testUser(10000+i))
	}
	if got := SplitUsers(nil); len(got) != 0 {
		t.Fatalf("SplitUsers(nil) = %d requests", len(got))
	}
	for _, budget := range []int{guestLimits.maxMsgSize, 4 << 10, 1} {
		requireSplit(t, users, splitUsers(users, budget), budget)
	}
	if n := len(SplitUsers(users)); n < 2 {
		t.Fatalf("1000 users in %d request, want them split", n)
	}
}

// requireSplit: each part has at most MaxUsersPerRequest users and fits
// budget unless it is one user, and the parts are users in order.
func requireSplit(t *testing.T, users []*guestv1.ProjectUser, parts [][]*guestv1.ProjectUser, budget int) {
	t.Helper()
	var joined []*guestv1.ProjectUser
	for _, p := range parts {
		req, err := proto.Marshal(&guestv1.EnsureUsersRequest{Users: p})
		if err != nil {
			t.Fatal(err)
		}
		if len(p) > MaxUsersPerRequest || (len(p) > 1 && len(req) > budget) {
			t.Fatalf("budget %d: a request of %d users and %d bytes", budget, len(p), len(req))
		}
		joined = append(joined, p...)
	}
	if !slices.Equal(joined, users) {
		t.Fatalf("budget %d: the parts hold %d users, not the %d in order", budget, len(joined), len(users))
	}
}

func removeReq() *guestv1.RemoveUserRequest {
	return &guestv1.RemoveUserRequest{ProjectId: strings.Repeat("a", 32), Uid: 10000, Gid: 10000, HomePath: "/private/var/wbhomes/wbp10000"}
}

func removeResp(result guestv1.RemoveResult, n uint32, rule string) *guestv1.RemoveUserResponse {
	return &guestv1.RemoveUserResponse{ProjectId: strings.Repeat("a", 32), Uid: 10000, Result: result, ProcessesLeft: n, Rule: rule}
}

// TestRemoveUserAccepts: each result in its bounds is taken, and the
// ones that keep the removal are logged with their rule.
func TestRemoveUserAccepts(t *testing.T) {
	cases := []struct {
		resp *guestv1.RemoveUserResponse
		rule string
	}{
		{removeResp(guestv1.RemoveResult_REMOVE_RESULT_REMOVED, 0, ""), ""},
		{removeResp(guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, 3, ""), RuleUserProcessesLeft},
		{removeResp(guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, MaxProcessesLeft, ""), RuleUserProcessesLeft},
		{removeResp(guestv1.RemoveResult_REMOVE_RESULT_REFUSED, 0, "home-not-owned"), RuleUserRemovalRefused},
	}
	for _, c := range cases {
		t.Run(c.resp.GetResult().String(), func(t *testing.T) {
			l := usersLinked(t, &usersGuest{remove: c.resp})
			var logs logCapture
			out, err := l.RemoveUser(t.Context(), removeReq(), logs.logger())
			if err != nil {
				t.Fatalf("RemoveUser: %v", err)
			}
			if out.Result != c.resp.GetResult() {
				t.Fatalf("result %v", out.Result)
			}
			rec := logs.last(t)
			if c.rule == "" && rec["decision"] != "accept" || c.rule != "" && (rec["rule"] != c.rule || rec["decision"] != "keep") {
				t.Fatalf("log %v, want rule %q", rec, c.rule)
			}
		})
	}
}

// TestRemoveUserInvalid: an answer for another project or UID, or out
// of its result's bounds, is dropped with RuleUsersReplyInvalid, so the
// removal stays pending.
func TestRemoveUserInvalid(t *testing.T) {
	other := removeResp(guestv1.RemoveResult_REMOVE_RESULT_REMOVED, 0, "")
	other.ProjectId = strings.Repeat("b", 32)
	otherUID := removeResp(guestv1.RemoveResult_REMOVE_RESULT_REMOVED, 0, "")
	otherUID.Uid = 10001
	cases := map[string]*guestv1.RemoveUserResponse{
		"other project":            other,
		"other UID":                otherUID,
		"unspecified":              removeResp(guestv1.RemoveResult_REMOVE_RESULT_UNSPECIFIED, 0, ""),
		"unknown result":           removeResp(9, 0, ""),
		"removed with a count":     removeResp(guestv1.RemoveResult_REMOVE_RESULT_REMOVED, 1, ""),
		"removed with a rule":      removeResp(guestv1.RemoveResult_REMOVE_RESULT_REMOVED, 0, "x"),
		"processes left of zero":   removeResp(guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, 0, ""),
		"processes left over cap":  removeResp(guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, MaxProcessesLeft+1, ""),
		"processes left with rule": removeResp(guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, 1, "x"),
		"refused without a rule":   removeResp(guestv1.RemoveResult_REMOVE_RESULT_REFUSED, 0, ""),
		"refused with a count":     removeResp(guestv1.RemoveResult_REMOVE_RESULT_REFUSED, 1, "x"),
		"refused with a bad rule":  removeResp(guestv1.RemoveResult_REMOVE_RESULT_REFUSED, 0, "a\nb"),
		"answer over 4 KiB":        removeResp(guestv1.RemoveResult_REMOVE_RESULT_REFUSED, 0, strings.Repeat("a", 5<<10)),
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			l := usersLinked(t, &usersGuest{remove: resp})
			var logs logCapture
			_, err := l.RemoveUser(t.Context(), removeReq(), logs.logger())
			requireUsersRefused(t, err, RuleUsersReplyInvalid)
			if rec := logs.last(t); rec["rule"] != RuleUsersReplyInvalid {
				t.Fatalf("log %v", rec)
			}
		})
	}
}

func requireUsersRefused(t *testing.T, err error, rule string) {
	t.Helper()
	r, isRefusal := errors.AsType[*UsersRefusal](err)
	if !isRefusal {
		t.Fatalf("err = %v (%T), want a *UsersRefusal", err, err)
	}
	if r.Rule != rule {
		t.Fatalf("rule %s (%v), want %s", r.Rule, err, rule)
	}
}
