//go:build unix

package guestd_test

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/shell"
)

// TestShellRefusals: a refused Shell is INVALID_ARGUMENT for a request
// out of form and FAILED_PRECONDITION otherwise, with the rule as the
// ErrorInfo reason. Without a MountData, no shell starts (I416).
func TestShellRefusals(t *testing.T) {
	cases := []struct {
		name  string
		mount bool
		id    string
		code  codes.Code
		rule  string
	}{
		{"no data mount", false, "proj-20261030-1200-ab12", codes.FailedPrecondition, guestd.RuleMountPending},
		{"unknown user", true, "proj-20261030-1200-ab12", codes.FailedPrecondition, shell.RuleUserUnknown},
		{"shell id", true, "proj", codes.InvalidArgument, shell.RuleStartInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link := usersLink(t, c.mount)
			stream, err := link.Client().Shell(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Start{Start: &guestv1.ShellStart{
				ShellId: c.id, User: "wb-p-nobody", Setup: &terminalv1.Setup{},
			}}}); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			st := status.Convert(err)
			if st.Code() != c.code {
				t.Fatalf("Shell = %v, want %s", err, c.code)
			}
			wantErrorInfo(t, st, c.rule)
		})
	}
}
