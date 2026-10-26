package guestlink

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// FuzzHelloResponse feeds arbitrary bytes through proto.Unmarshal into
// the checks Hello applies to wb-guestd's answer (C11-host-daemons,
// "Every vsock RPC handler in `wb-hostd`"). A refused answer never yields
// a version. An accepted one is 1 to 64 bytes of printable ASCII and the
// host's own version, and its package list is the host's own.
func FuzzHelloResponse(f *testing.F) {
	seed := func(v string) []byte {
		b, err := proto.Marshal(&guestv1.HelloResponse{GuestdVersion: v, ServedPackages: ownPackages()})
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	f.Add([]byte{})
	f.Add(seed(own))
	f.Add(seed("1.2.3-" + strings.Repeat("a", 58))) // 64 bytes
	f.Add(seed("1.2.3-" + strings.Repeat("a", 59))) // 65 bytes
	unknown := protowire.AppendTag(seed(own), 99, protowire.BytesType)
	f.Add(protowire.AppendBytes(unknown, []byte("unknown field")))

	f.Fuzz(func(t *testing.T, data []byte) {
		resp := &guestv1.HelloResponse{}
		if err := proto.Unmarshal(data, resp); err != nil {
			return // gRPC refuses an answer that doesn't decode (RuleInvalid)
		}
		v, r := check(own, resp)
		if r != nil {
			requireRefusal(t, v, r)
			return
		}
		requireAccepted(t, v, resp)
	})
}

// FuzzMountDataResponse feeds arbitrary bytes as wb-guestd's MountData
// answer and as the status of its refusal, error details included, into
// what MountData does with them (C11-host-daemons, "Fuzzing"). An answer
// that decodes is a success. A status is only ever a rule of MountRules
// or RuleReplyInvalid, and its message holds no raw control byte.
func FuzzMountDataResponse(f *testing.F) {
	refusal := func(code codes.Code, domain, reason string) []byte {
		st, err := status.New(code, reason).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: domain})
		if err != nil {
			f.Fatal(err)
		}
		b, err := proto.Marshal(st.Proto())
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	f.Add([]byte{}, refusal(codes.FailedPrecondition, guestDomain, MountRules[0]))
	f.Add([]byte{0x0a, 0x00}, refusal(codes.FailedPrecondition, guestDomain, "data-volume-gone"))
	f.Add([]byte{}, refusal(codes.FailedPrecondition, guestDomain, strings.Repeat("a", 65)))
	f.Add([]byte{}, refusal(codes.FailedPrecondition, "other", MountRules[1]))
	f.Add([]byte{}, refusal(codes.Unimplemented, guestDomain, MountRules[2]))

	f.Fuzz(func(t *testing.T, answer, st []byte) {
		// A success decodes into the empty MountDataResponse, unknown
		// fields and all, and carries nothing the host reads.
		_ = proto.Unmarshal(answer, &guestv1.MountDataResponse{})
		sp := &spb.Status{}
		if err := proto.Unmarshal(st, sp); err != nil {
			return // gRPC refuses trailers that don't decode (RuleReplyInvalid)
		}
		r := mountRefusalOf(status.FromProto(sp))
		if r.Rule != RuleReplyInvalid && !slices.Contains(MountRules, r.Rule) {
			t.Fatalf("refusal with rule %q", r.Rule)
		}
		if strings.ContainsFunc(r.Error(), func(c rune) bool { return c < 0x20 || c == 0x7f }) {
			t.Fatalf("refusal message %q holds a raw control byte", r.Error())
		}
	})
}

// FuzzProjectUserReplies feeds arbitrary bytes through proto.Unmarshal
// into the checks of wb-guestd's EnsureUsers and RemoveUser answers
// (C11-host-daemons, "Fuzzing"; S06-03-image-updates). An accepted
// EnsureUsers answer only names UIDs the host sent, once each, with OK
// and no rule or a refusal with a rule in the form. An accepted
// RemoveUser answer is for the request's project and UID and keeps its
// result's bounds. A dropped answer yields nothing.
func FuzzProjectUserReplies(f *testing.F) {
	sent := []*guestv1.ProjectUser{{Uid: 10000}, {Uid: 10001}, {Uid: 10002}}
	req := &guestv1.RemoveUserRequest{ProjectId: strings.Repeat("a", 32), Uid: 10000}
	seed := func(m proto.Message) []byte {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	f.Add([]byte{}, []byte{})
	f.Add(seed(&guestv1.EnsureUsersResponse{Results: []*guestv1.EnsureUserResult{
		{Uid: 10000, Result: guestv1.EnsureResult_ENSURE_RESULT_OK},
		{Uid: 10001, Result: guestv1.EnsureResult_ENSURE_RESULT_REFUSED, Rule: "user-exists"},
	}}), seed(&guestv1.RemoveUserResponse{
		ProjectId: req.GetProjectId(), Uid: 10000, Result: guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT, ProcessesLeft: 2,
	}))
	f.Add(seed(&guestv1.EnsureUsersResponse{Results: []*guestv1.EnsureUserResult{
		{Uid: 10000, Result: guestv1.EnsureResult_ENSURE_RESULT_OK},
		{Uid: 10000, Result: guestv1.EnsureResult_ENSURE_RESULT_OK},
	}}), seed(&guestv1.RemoveUserResponse{
		ProjectId: req.GetProjectId(), Uid: 10000, Result: guestv1.RemoveResult_REMOVE_RESULT_REFUSED, Rule: "home-not-owned",
	}))

	f.Fuzz(func(t *testing.T, ensure, remove []byte) {
		if resp := (&guestv1.EnsureUsersResponse{}); proto.Unmarshal(ensure, resp) == nil {
			requireEnsureChecked(t, sent, resp)
		}
		if resp := (&guestv1.RemoveUserResponse{}); proto.Unmarshal(remove, resp) == nil {
			requireRemoveChecked(t, req, resp)
		}
	})
}

func requireEnsureChecked(t *testing.T, sent []*guestv1.ProjectUser, resp *guestv1.EnsureUsersResponse) {
	t.Helper()
	got, r := CheckEnsureUsers(sent, resp)
	if r != nil {
		if got != nil || r.Rule != RuleUsersReplyInvalid {
			t.Fatalf("dropped answer (%s) yielded %v", r.Rule, got)
		}
		return
	}
	if len(got) != len(resp.GetResults()) {
		t.Fatalf("%d results taken from %d", len(got), len(resp.GetResults()))
	}
	for uid, res := range got {
		if !slices.ContainsFunc(sent, func(u *guestv1.ProjectUser) bool { return u.GetUid() == uid }) {
			t.Fatalf("accepted a result for UID %d, which wasn't sent", uid)
		}
		if res.OK == (res.Rule != "") || (!res.OK && !validRule(res.Rule)) {
			t.Fatalf("accepted result %+v", res)
		}
	}
}

func requireRemoveChecked(t *testing.T, req *guestv1.RemoveUserRequest, resp *guestv1.RemoveUserResponse) {
	t.Helper()
	out, r := CheckRemoveUser(req, resp)
	if r != nil {
		if out != (RemoveOutcome{}) || r.Rule != RuleUsersReplyInvalid {
			t.Fatalf("dropped answer (%s) yielded %+v", r.Rule, out)
		}
		return
	}
	if resp.GetProjectId() != req.GetProjectId() || resp.GetUid() != req.GetUid() {
		t.Fatalf("accepted an answer for %q UID %d", resp.GetProjectId(), resp.GetUid())
	}
	ok := false
	switch out.Result {
	case guestv1.RemoveResult_REMOVE_RESULT_REMOVED:
		ok = out.ProcessesLeft == 0 && out.Rule == ""
	case guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT:
		ok = out.ProcessesLeft >= 1 && out.ProcessesLeft <= MaxProcessesLeft && out.Rule == ""
	case guestv1.RemoveResult_REMOVE_RESULT_REFUSED:
		ok = out.ProcessesLeft == 0 && validRule(out.Rule)
	case guestv1.RemoveResult_REMOVE_RESULT_UNSPECIFIED:
	}
	if !ok {
		t.Fatalf("accepted outcome %+v", out)
	}
}

// requireRefusal: a refusal names its rule and yields no version.
func requireRefusal(t *testing.T, v string, r *Refusal) {
	t.Helper()
	if v != "" {
		t.Fatalf("refused (%s) but yielded version %q", r.Rule, v)
	}
	if r.Rule == "" {
		t.Fatal("refusal without a rule")
	}
}

// requireAccepted: an accepted answer has a version of 1 to 64 printable
// ASCII bytes that is the host's own, and the host's package list.
func requireAccepted(t *testing.T, v string, resp *guestv1.HelloResponse) {
	t.Helper()
	if len(v) < 1 || len(v) > 64 {
		t.Fatalf("accepted version of %d bytes", len(v))
	}
	for _, b := range []byte(v) {
		if b < 0x20 || b > 0x7e {
			t.Fatalf("accepted version %q has byte %#x", v, b)
		}
	}
	if v != own {
		t.Fatalf("accepted version %q, want only the host's %q", v, own)
	}
	if !slices.Equal(resp.GetServedPackages(), ownPackages()) {
		t.Fatalf("accepted packages %q", resp.GetServedPackages())
	}
}
