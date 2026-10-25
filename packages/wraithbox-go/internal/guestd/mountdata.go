package guestd

import (
	"context"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// ErrorDomain is the ErrorInfo domain of wb-guestd's refusals
// (proto/wraithbox/guest/v1, MountData).
const ErrorDomain = "wraithbox.guest.v1"

// MountData mounts the data volume with the request's UUID at
// HomePath, or checks it when it is mounted there already
// (S06-05-disks, "Mounting the data volume"). A refusal is
// FAILED_PRECONDITION with the rule as the ErrorInfo reason. The status
// message holds only the rule: what was found is in the guest's log.
func (s *service) MountData(ctx context.Context, req *guestv1.MountDataRequest) (*guestv1.MountDataResponse, error) {
	uuid := req.GetVolumeUuid()
	if !uuidForm.MatchString(uuid) {
		s.log.LogAttrs(ctx, slog.LevelWarn, "data mount request refused",
			slog.String("rule", RuleMountRequest), slog.String("decision", "refuse"),
			slog.String("volume_uuid", cut(uuid)))
		return nil, withRule(codes.InvalidArgument, RuleMountRequest)
	}
	if r := s.dataMount.Mount(ctx, uuid, req.GetFirstMount()); r != nil {
		return nil, withRule(codes.FailedPrecondition, r.Rule)
	}
	return &guestv1.MountDataResponse{}, nil
}

// withRule builds a refusal's status: the rule as the message and as
// the ErrorInfo reason.
func withRule(code codes.Code, rule string) error {
	st := status.New(code, rule)
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: rule, Domain: ErrorDomain})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}
