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
