package guestd

import (
	"context"

	"google.golang.org/grpc/codes"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/trust"
)

// trustInstaller writes the trust files: trust.Installer, or a fake in
// tests.
type trustInstaller interface {
	Install(ctx context.Context, ders [][]byte) *trust.Refusal
}

// trustCodes is the gRPC code of each rule an install refuses with.
var trustCodes = map[string]codes.Code{
	trust.RuleRefused:   codes.InvalidArgument,
	trust.RuleDirUnsafe: codes.FailedPrecondition,
	trust.RuleFailed:    codes.Internal,
}

// InstallTrust makes the request's CAs the guest's Wraith Box trust, in
// place of the ones before (S09-08-guest-trust; internal/guestd/trust
// checks the request, writes the files and logs the decision). Success
// is an empty answer after every rename. A refusal holds only the rule,
// as the message and the ErrorInfo reason: what was found is in the
// guest's log.
func (s *service) InstallTrust(ctx context.Context, req *guestv1.InstallTrustRequest) (*guestv1.InstallTrustResponse, error) {
	if r := s.trust.Install(ctx, req.GetCaCertificates()); r != nil {
		code, ok := trustCodes[r.Rule]
		if !ok {
			code = codes.Internal
		}
		return nil, withRule(code, r.Rule)
	}
	return &guestv1.InstallTrustResponse{}, nil
}
