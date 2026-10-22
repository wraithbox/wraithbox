// Package hostservice is wb-hostd's side of wraithbox.hostd.v1, the
// contract wb's management commands call over the local IPC endpoint
// (S04-architecture, "Inter-process contracts";
// S05-03-management-commands). Each refusal is a gRPC status whose
// message is the text for the user and whose google.rpc.ErrorInfo
// reason is the rule (proto/wraithbox/hostd/v1).
//
// wb-hostd serves it only on its local IPC endpoint with the
// credentials of internal/hostversion/grpccreds, so the peer and the
// build version of every connection are checked before a call reaches
// this package (SEC12-least-privilege, internal/platform/localipc). The
// daemon's main, which wires that up, comes with I287.
package hostservice

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion/grpccreds"
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
	StartRun(ctx context.Context, slot vm.Slot) (uint64, error)
	Stop(ctx context.Context, slot vm.Slot) error
	Status(slot vm.Slot) vm.Status
	ResetData(ctx context.Context, slot vm.Slot) (datadisk.ResetResult, error)
}

// Links is the state of each VM's guest link. *guestconn.Links
// implements it.
type Links interface {
	Status(slot vm.Slot) guestconn.Status
}

// Server serves wraithbox.hostd.v1.HostService.
type Server struct {
	hostdv1.UnimplementedHostServiceServer

	VMs VMs
	// Links has the guest link states. nil reads every link as down.
	Links Links
	// Refusing is set in refusing mode. Every call of this service is
	// then refused with its rule (S17-05-upgrades-and-migrations,
	// "Refusing mode"): only wb status is answered, and that isn't part
	// of this service.
	Refusing *startup.Refusing
	// Log gets the refusals. nil takes slog.Default().
	Log *slog.Logger
}

var _ hostdv1.HostServiceServer = (*Server)(nil)

// StartVM cold-starts a stopped VM (S05-03-management-commands,
// "`wb vm`"). It returns once the provider has started the VM, with the
// generation of the run.
func (s *Server) StartVM(ctx context.Context, req *hostdv1.StartVMRequest) (*hostdv1.StartVMResponse, error) {
	slot, err := s.admit(ctx, "StartVM", req.GetVm())
	if err != nil {
		return nil, err
	}
	// A start that began runs to its end, or to the manager's own
	// deadlines, when wb goes away, as a stop and a reset do. wb then
	// says the VM may be starting (S05-03-management-commands, "`wb vm`").
	generation, err := s.VMs.StartRun(context.WithoutCancel(ctx), slot)
	if err != nil {
		return nil, s.fromError(ctx, "StartVM", err)
	}
	return &hostdv1.StartVMResponse{Generation: generation}, nil
}

// StopVM stops a running VM, a requested stop and then a forced one
// (S05-03-management-commands, "`wb vm`"). A refusal for live sessions
// names each in its message (S06-09-session-lifecycle, "Idle suspend").
func (s *Server) StopVM(ctx context.Context, req *hostdv1.StopVMRequest) (*hostdv1.StopVMResponse, error) {
	slot, err := s.admit(ctx, "StopVM", req.GetVm())
	if err != nil {
		return nil, err
	}
	if err := s.VMs.Stop(ctx, slot); err != nil {
		return nil, s.fromError(ctx, "StopVM", err)
	}
	return &hostdv1.StopVMResponse{}, nil
}

// GetVMStatus returns a VM's state and its guest link's.
func (s *Server) GetVMStatus(ctx context.Context, req *hostdv1.GetVMStatusRequest) (*hostdv1.GetVMStatusResponse, error) {
	slot, err := s.admit(ctx, "GetVMStatus", req.GetVm())
	if err != nil {
		return nil, err
	}
	return &hostdv1.GetVMStatusResponse{Status: s.statusOf(slot, req.GetVm())}, nil
}

// ResetVMData gives a stopped VM a new data disk
// (S05-03-management-commands, "`wb vm reset-data`"). The VM manager
// refuses it unless the VM is stopped.
func (s *Server) ResetVMData(ctx context.Context, req *hostdv1.ResetVMDataRequest) (*hostdv1.ResetVMDataResponse, error) {
	slot, err := s.admit(ctx, "ResetVMData", req.GetVm())
	if err != nil {
		return nil, err
	}
	res, err := s.VMs.ResetData(ctx, slot)
	if err != nil {
		return nil, s.fromError(ctx, "ResetVMData", err)
	}
	return &hostdv1.ResetVMDataResponse{OldVolumeUuid: res.OldVolumeUUID, NewVolumeUuid: res.NewVolumeUUID}, nil
}

// admit refuses every call in refusing mode, with the rule that put
// wb-hostd in it, and a call that names no VM. Else it returns the
// call's slot.
func (s *Server) admit(ctx context.Context, method string, v hostdv1.VM) (vm.Slot, error) {
	if r := s.Refusing; r != nil {
		return "", s.refuse(ctx, method, codes.FailedPrecondition, r.Rule,
			"wb-hostd is in refusing mode, and answers only wb status: "+r.Detail)
	}
	slot, ok := SlotOf(v)
	if !ok {
		return "", s.refuse(ctx, method, codes.InvalidArgument, RuleVMUnspecified,
			"the request names no VM: name work or isolated")
	}
	// wb polls GetVMStatus while it waits for a start, so its accepts
	// are logged at debug level only.
	level := slog.LevelInfo
	if method == "GetVMStatus" {
		level = slog.LevelDebug
	}
	attrs := append([]slog.Attr{
		slog.String("decision", "accept"), slog.String("method", method), slog.String("slot", string(slot)),
	}, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, level, "host request", attrs...)
	return slot, nil
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
	attrs := append([]slog.Attr{
		slog.String("rule", rule), slog.String("decision", "refuse"),
		slog.String("method", method), slog.String("reason", msg),
	}, peerAttrs(ctx)...)
	s.logger().LogAttrs(ctx, slog.LevelWarn, "host request refused", attrs...)
	return withRule(code, rule, msg)
}

func (s *Server) logger() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// peerAttrs names the calling wb by the user ID and process ID that
// localipc checked on its connection (SEC12-least-privilege). A call
// over a connection without that check has none.
func peerAttrs(ctx context.Context) []slog.Attr {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}
	info, ok := p.AuthInfo.(grpccreds.AuthInfo)
	if !ok {
		return nil
	}
	return []slog.Attr{slog.Int("peer_uid", info.Peer.UID), slog.Int("peer_pid", info.Peer.PID)}
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
