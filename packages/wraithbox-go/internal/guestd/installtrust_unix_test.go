//go:build unix

package guestd_test

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/trust"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
)

// installTrust runs one InstallTrust from the host's end of a
// socketpair, with the hello first, as wb-hostd calls it.
func installTrust(t *testing.T, fake guestd.TrustInstallFunc, req *guestv1.InstallTrustRequest) (*guestv1.InstallTrustResponse, error) {
	t.Helper()
	const v = "1.2.3"
	host, guest := socketpair(t)
	srv := guestd.NewServer(v, slog.New(slog.DiscardHandler), guestd.WithTrustInstaller(fake))
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	defer srv.Stop()
	link, err := guestlink.Hello(t.Context(), host, v, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	defer link.Close()
	return link.Client().InstallTrust(t.Context(), req)
}

// TestC02InstallTrustRPC: the request's certificates reach the installer in
// order, a success is an empty answer, and each rule of a refusal comes
// back with its code and as the ErrorInfo reason, with nothing else in
// the message.
func TestC02InstallTrustRPC(t *testing.T) {
	certs := [][]byte{[]byte("a"), []byte("b")}
	var got [][]byte
	resp, err := installTrust(t, func(_ context.Context, ders [][]byte) *trust.Refusal {
		got = ders
		return nil
	}, &guestv1.InstallTrustRequest{CaCertificates: certs})
	if err != nil || resp == nil {
		t.Fatalf("InstallTrust = %v, %v", resp, err)
	}
	if !slices.EqualFunc(got, certs, bytes.Equal) {
		t.Errorf("installer got %q, want %q", got, certs)
	}
	for _, tc := range []struct {
		rule string
		code codes.Code
	}{
		{trust.RuleRefused, codes.InvalidArgument},
		{trust.RuleDirUnsafe, codes.FailedPrecondition},
		{trust.RuleFailed, codes.Internal},
		{"another-rule", codes.Internal},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			_, err := installTrust(t, func(context.Context, [][]byte) *trust.Refusal {
				return &trust.Refusal{Rule: tc.rule, Detail: "secret detail"}
			}, &guestv1.InstallTrustRequest{CaCertificates: certs})
			st, ok := status.FromError(err)
			if !ok || st.Code() != tc.code || st.Message() != tc.rule {
				t.Fatalf("InstallTrust error %v, want %v %s", err, tc.code, tc.rule)
			}
			wantErrorInfo(t, st, tc.rule)
		})
	}
}
