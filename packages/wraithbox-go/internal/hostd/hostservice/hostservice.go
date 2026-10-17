// Package hostservice is wb-hostd's side of wraithbox.hostd.v1, the
// contract wb's management commands call over the local IPC endpoint
// (S04-architecture, "Inter-process contracts";
// S05-03-management-commands). Each refusal is a gRPC status whose
// message is the text for the user and whose google.rpc.ErrorInfo
// reason is the rule (proto/wraithbox/hostd/v1).
package hostservice

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// Domain is the ErrorInfo domain of every refusal.
const Domain = "wraithbox.hostd.v1"

// The rules of this package.
const (
	// RuleVMUnspecified: a request that names no VM, or a value the enum
	// doesn't have.
	RuleVMUnspecified = "hostd-vm-unspecified"
	// RuleInternal: an error that isn't a refusal.
	RuleInternal = "hostd-internal"
)

// VMs is the VM manager, as the service uses it. *vm.Manager
// implements it.
type VMs interface {
	ResetData(ctx context.Context, slot vm.Slot) (datadisk.ResetResult, error)
}

// Server serves wraithbox.hostd.v1.HostService.
type Server struct {
	hostdv1.UnimplementedHostServiceServer

	VMs VMs
	// Log gets the refusals. nil takes slog.Default().
	Log *slog.Logger
}

var _ hostdv1.HostServiceServer = (*Server)(nil)

// ResetVMData gives a stopped VM a new data disk
// (S05-03-management-commands, "`wb vm reset-data`"). The VM manager
// refuses it unless the VM is stopped, and in refusing mode.
func (s *Server) ResetVMData(ctx context.Context, req *hostdv1.ResetVMDataRequest) (*hostdv1.ResetVMDataResponse, error) {
	slot, ok := SlotOf(req.GetVm())
	if !ok {
		return nil, s.refuse(ctx, "ResetVMData", codes.InvalidArgument, RuleVMUnspecified,
			"the request names no VM: name work or isolated")
	}
	res, err := s.VMs.ResetData(ctx, slot)
	if err != nil {
		return nil, s.fromError(ctx, "ResetVMData", err)
	}
	return &hostdv1.ResetVMDataResponse{OldVolumeUuid: res.OldVolumeUUID, NewVolumeUuid: res.NewVolumeUUID}, nil
}

// SlotOf maps the contract's VM to a slot.
func SlotOf(v hostdv1.VM) (vm.Slot, bool) {
	switch v {
	case hostdv1.VM_VM_WORK:
		return vm.SlotWork, true
	case hostdv1.VM_VM_ISOLATED:
		return vm.SlotIsolated, true
	case hostdv1.VM_VM_UNSPECIFIED:
		return "", false
	}
	return "", false
}

// fromError turns the manager's refusal into a status with its rule.
// The manager logged the refusal.
func (s *Server) fromError(ctx context.Context, method string, err error) error {
	if r, ok := errors.AsType[*vm.Refusal](err); ok {
		return withRule(codes.FailedPrecondition, string(r.Rule), r.Message)
	}
	return s.refuse(ctx, method, codes.Internal, RuleInternal, "wb-hostd failed: "+err.Error())
}

func (s *Server) refuse(ctx context.Context, method string, code codes.Code, rule, msg string) error {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	log.LogAttrs(ctx, slog.LevelWarn, "host request refused",
		slog.String("rule", rule), slog.String("decision", "refuse"),
		slog.String("method", method), slog.String("reason", msg))
	return withRule(code, rule, msg)
}

// withRule builds the status of a refusal: msg for the user, and the
// rule as the ErrorInfo reason.
func withRule(code codes.Code, rule, msg string) error {
	st := status.New(code, msg)
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: rule, Domain: Domain})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}
