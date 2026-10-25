package guestlink

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

const testUUID = "0F8E98A2-BC26-4E5D-92E5-FD76D3BB8802"

// mountGuest passes Hello, then answers MountData with err, or with
// resp when err is nil. block makes it never answer.
type mountGuest struct {
	fakeGuest

	resp  *guestv1.MountDataResponse
	err   error
	block bool
	got   chan *guestv1.MountDataRequest
}

func (g *mountGuest) MountData(ctx context.Context, req *guestv1.MountDataRequest) (*guestv1.MountDataResponse, error) {
	if g.got != nil {
		g.got <- req
	}
	if g.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if g.err != nil {
		return nil, g.err
	}
	return g.resp, nil
}

// refusing answers MountData with a FAILED_PRECONDITION status whose
// one detail is an ErrorInfo with reason in domain.
func refusing(code codes.Code, domain, reason string) *mountGuest {
	st, err := status.New(code, reason).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: domain})
	if err != nil {
		panic(err)
	}
	return &mountGuest{err: st.Err()}
}

// linked runs g as a guest and returns a link to it after Hello.
func linked(t *testing.T, g *mountGuest) *Link {
	t.Helper()
	g.fakeGuest = *answer(own, pkg)
	l, err := Hello(t.Context(), serve(t, g), own, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// TestMountDataAccepts: an answer without error is a mount, and the
// request carries the UUID and first_mount.
func TestMountDataAccepts(t *testing.T) {
	g := &mountGuest{resp: &guestv1.MountDataResponse{}, got: make(chan *guestv1.MountDataRequest, 1)}
	l := linked(t, g)
	var logs logCapture
	if err := l.MountData(t.Context(), testUUID, true, logs.logger()); err != nil {
		t.Fatalf("MountData: %v", err)
	}
	if req := <-g.got; req.GetVolumeUuid() != testUUID || !req.GetFirstMount() {
		t.Fatalf("request %v", req)
	}
	if rec := logs.last(t); rec["decision"] != "accept" {
		t.Fatalf("log %v, want an accept", rec)
	}
}

// TestMountDataGuestRules: each rule of MountRules the guest refuses
// with comes out as that rule, logged.
func TestMountDataGuestRules(t *testing.T) {
	for _, rule := range MountRules {
		t.Run(rule, func(t *testing.T) {
			l := linked(t, refusing(codes.FailedPrecondition, guestDomain, rule))
			var logs logCapture
			requireMountRefused(t, l.MountData(t.Context(), testUUID, false, logs.logger()), rule)
			if rec := logs.last(t); rec["rule"] != rule || rec["decision"] != "refuse" {
				t.Fatalf("log %v, want a refusal with rule %s", rec, rule)
			}
		})
	}
}

// TestMountDataInvalid: every answer other than a success or a refusal
// with a listed rule is RuleReplyInvalid (SEC11-root-gains-nothing).
func TestMountDataInvalid(t *testing.T) {
	big := &guestv1.MountDataResponse{}
	big.ProtoReflect().SetUnknown(protowire.AppendBytes(
		protowire.AppendTag(nil, 99, protowire.BytesType), make([]byte, MountMaxRecvMsgSize)))
	twoDetails, err := status.New(codes.FailedPrecondition, "x").WithDetails(
		&errdetails.ErrorInfo{Reason: MountRules[0], Domain: guestDomain},
		&errdetails.ErrorInfo{Reason: MountRules[1], Domain: guestDomain})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		guest *mountGuest
	}{
		{"unknown rule", refusing(codes.FailedPrecondition, guestDomain, "data-volume-gone")},
		{"65-byte rule", refusing(codes.FailedPrecondition, guestDomain, strings.Repeat("a", 65))},
		{"control byte", refusing(codes.FailedPrecondition, guestDomain, "data-mount-root\x1b")},
		{"uppercase", refusing(codes.FailedPrecondition, guestDomain, "DATA-MOUNT-ROOT")},
		{"empty rule", refusing(codes.FailedPrecondition, guestDomain, "")},
		{"host rule", refusing(codes.FailedPrecondition, guestDomain, RuleMountTimeout)},
		{"other domain", refusing(codes.FailedPrecondition, "wraithbox.hostd.v1", MountRules[0])},
		{"other code", refusing(codes.Internal, guestDomain, MountRules[0])},
		{"guest deadline status", refusing(codes.DeadlineExceeded, guestDomain, MountRules[0])},
		{"no detail", &mountGuest{err: status.Error(codes.FailedPrecondition, MountRules[0])}},
		{"two details", &mountGuest{err: twoDetails.Err()}},
		{"other detail", &mountGuest{err: mustDetail(t, &errdetails.DebugInfo{Detail: MountRules[0]})}},
		{"answer over 4 KiB", &mountGuest{resp: big}},
		{"status over 4 KiB", &mountGuest{err: status.Error(codes.FailedPrecondition, strings.Repeat("x", 5<<10))}},
		{"unimplemented", &mountGuest{err: status.Error(codes.Unimplemented, "unknown method")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := linked(t, c.guest)
			var logs logCapture
			requireMountRefused(t, l.MountData(t.Context(), testUUID, false, logs.logger()), RuleReplyInvalid)
			if rec := logs.last(t); rec["rule"] != RuleReplyInvalid {
				t.Fatalf("log %v, want a refusal with rule %s", rec, RuleReplyInvalid)
			}
		})
	}
}

// TestMountDataUnimplemented: a guest without MountData, an older
// wb-guestd, is RuleReplyInvalid.
func TestMountDataUnimplemented(t *testing.T) {
	g := &struct{ fakeGuest }{*answer(own, pkg)}
	l, err := Hello(t.Context(), serve(t, g), own, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	requireMountRefused(t, l.MountData(t.Context(), testUUID, false, slog.New(slog.DiscardHandler)), RuleReplyInvalid)
}

// TestMountDataUnavailable: a call that fails with UNAVAILABLE is
// RuleMountUnavailable, which the link answers with a reconnect, and
// not a refusal of the volume.
func TestMountDataUnavailable(t *testing.T) {
	l := linked(t, &mountGuest{err: status.Error(codes.Unavailable, "transport is closing")})
	requireMountRefused(t, l.MountData(t.Context(), testUUID, false, slog.New(slog.DiscardHandler)), RuleMountUnavailable)
}

// TestMountDataTimeout: no answer is RuleMountTimeout when the deadline
// passes.
func TestMountDataTimeout(t *testing.T) {
	if MountTimeout != 45*time.Second {
		t.Fatalf("MountTimeout = %v, want 45s", MountTimeout)
	}
	l := linked(t, &mountGuest{block: true})
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	requireMountRefused(t, l.MountData(ctx, testUUID, false, slog.New(slog.DiscardHandler)), RuleMountTimeout)
}

// TestMountDataCanceled: when the host cancels, the error wraps
// context.Canceled and isn't a refusal.
func TestMountDataCanceled(t *testing.T) {
	l := linked(t, &mountGuest{block: true})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := l.MountData(ctx, testUUID, false, slog.New(slog.DiscardHandler))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MountData = %v, want context.Canceled", err)
	}
	if _, ok := errors.AsType[*MountRefusal](err); ok {
		t.Fatal("a cancel by the host is a refusal of the guest")
	}
}

// TestMountRefusalHidesGuestText: guest text in a refusal is escaped and
// cut.
func TestMountRefusalHidesGuestText(t *testing.T) {
	evil := "\x1b]0;evil\x07" + strings.Repeat("z", 300)
	st, err := status.New(codes.FailedPrecondition, evil).WithDetails(&errdetails.ErrorInfo{Reason: evil, Domain: guestDomain})
	if err != nil {
		t.Fatal(err)
	}
	r := mountRefusalOf(st)
	if r.Rule != RuleReplyInvalid {
		t.Fatalf("rule %s", r.Rule)
	}
	if strings.ContainsAny(r.Error(), "\x1b\x07") || len(r.Error()) > 600 {
		t.Fatalf("refusal message %q holds raw or uncut guest text", r.Error())
	}
}

func mustDetail(t *testing.T, d *errdetails.DebugInfo) error {
	t.Helper()
	st, err := status.New(codes.FailedPrecondition, "x").WithDetails(d)
	if err != nil {
		t.Fatal(err)
	}
	return st.Err()
}

func requireMountRefused(t *testing.T, err error, rule string) {
	t.Helper()
	r, ok := errors.AsType[*MountRefusal](err)
	if !ok {
		t.Fatalf("MountData = %v (%T), want a *MountRefusal", err, err)
	}
	if r.Rule != rule {
		t.Fatalf("rule %s (%v), want %s", r.Rule, err, rule)
	}
}
