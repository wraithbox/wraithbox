package hostservice

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// RuleCAUnavailable: a CA call to a wb-hostd that has no CA record.
const RuleCAUnavailable = "hostd-ca-unavailable"

// CARecords is each VM's record of inspection CAs, as `wb vm reset-ca`
// uses it. *carecord.Keeper implements it.
type CARecords interface {
	List(ctx context.Context, slot string) ([]state.CA, error)
	Reset(ctx context.Context, slot string, confirmed []string, command string) ([]state.CA, error)
}

// ListCAs returns a VM's inspection CAs, oldest first, for the list
// `wb vm reset-ca` shows before it asks (S05-03-management-commands,
// "`wb vm reset-ca`").
func (s *Server) ListCAs(ctx context.Context, req *hostdv1.ListCAsRequest) (*hostdv1.ListCAsResponse, error) {
	slot, err := s.admit(ctx, "ListCAs", req.GetVm())
	if err != nil {
		return nil, err
	}
	if s.CAs == nil {
		return nil, s.refuse(ctx, "ListCAs", codes.FailedPrecondition, RuleCAUnavailable, "wb-hostd has no CA record")
	}
	cas, err := s.CAs.List(ctx, string(slot))
	if err != nil {
		return nil, s.fromError(ctx, "ListCAs", err)
	}
	res := &hostdv1.ListCAsResponse{}
	for _, ca := range cas {
		res.Cas = append(res.Cas, &hostdv1.InspectionCA{
			Sha256: ca.SHA256, NotBeforeUnix: ca.NotBefore.Unix(), NotAfterUnix: ca.NotAfter.Unix(), Signing: ca.Signing,
		})
	}
	return res, nil
}

// ResetCA drops a stopped VM's inspection CAs (S09-06-ca-keys,
// "Recovery"; S05-03-management-commands, "`wb vm reset-ca`"). It holds
// the VM's start lock while it runs, so it is refused with vm-running
// unless the VM is stopped, and a start meanwhile is refused with
// vm-resetting. The CA record refuses it with ca-record-changed unless
// the request's fingerprints are the record's CAs.
//
// wb asks for it only after a person confirmed the CAs in wb's own
// terminal (SEC14-no-fake-approvals). No guest RPC reaches it.
func (s *Server) ResetCA(ctx context.Context, req *hostdv1.ResetCARequest) (*hostdv1.ResetCAResponse, error) {
	slot, err := s.admit(ctx, "ResetCA", req.GetVm())
	if err != nil {
		return nil, err
	}
	if s.CAs == nil {
		return nil, s.refuse(ctx, "ResetCA", codes.FailedPrecondition, RuleCAUnavailable, "wb-hostd has no CA record")
	}
	command := "wb vm reset-ca " + string(slot)
	what := fmt.Sprintf("reset the %s VM's inspection CA", slot)
	var dropped []state.CA
	err = s.VMs.WhileStopped(ctx, slot, command, what, func(ctx context.Context) error {
		var err error
		dropped, err = s.CAs.Reset(ctx, string(slot), req.GetSha256(), command)
		return err
	})
	if err != nil {
		return nil, s.fromError(ctx, "ResetCA", err)
	}
	res := &hostdv1.ResetCAResponse{}
	for _, ca := range dropped {
		res.DroppedSha256 = append(res.DroppedSha256, ca.SHA256)
	}
	return res, nil
}
