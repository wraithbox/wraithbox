package hostservice_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

type fakeVMs struct {
	slots []vm.Slot
	err   error
}

func (f *fakeVMs) ResetData(_ context.Context, slot vm.Slot) (datadisk.ResetResult, error) {
	f.slots = append(f.slots, slot)
	if f.err != nil {
		return datadisk.ResetResult{}, f.err
	}
	return datadisk.ResetResult{OldVolumeUUID: "A", NewVolumeUUID: "B"}, nil
}

func ruleOf(t *testing.T, err error) (codes.Code, string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err %v isn't a status", err)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if info.GetDomain() != hostservice.Domain {
				t.Errorf("domain %s", info.GetDomain())
			}
			return st.Code(), info.GetReason()
		}
	}
	t.Fatalf("status %v has no ErrorInfo", st)
	return 0, ""
}

func TestResetVMData(t *testing.T) {
	vms := &fakeVMs{}
	s := &hostservice.Server{VMs: vms}
	res, err := s.ResetVMData(t.Context(), &hostdv1.ResetVMDataRequest{Vm: hostdv1.VM_VM_ISOLATED})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetOldVolumeUuid() != "A" || res.GetNewVolumeUuid() != "B" || len(vms.slots) != 1 || vms.slots[0] != vm.SlotIsolated {
		t.Errorf("response %v, calls %v", res, vms.slots)
	}
}

// TestResetVMDataRefusals: a request without a VM, and a refusal of the
// manager, each arrive with the rule as the ErrorInfo reason.
func TestResetVMDataRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		vm   hostdv1.VM
		err  error
		code codes.Code
		rule string
	}{
		{"unspecified", hostdv1.VM_VM_UNSPECIFIED, nil, codes.InvalidArgument, hostservice.RuleVMUnspecified},
		{"out of the enum", hostdv1.VM(7), nil, codes.InvalidArgument, hostservice.RuleVMUnspecified},
		{"running", hostdv1.VM_VM_WORK, &vm.Refusal{Rule: vm.RuleRunning, Message: "stop it first"}, codes.FailedPrecondition, string(vm.RuleRunning)},
		{"data disk rule", hostdv1.VM_VM_WORK, &vm.Refusal{Rule: "diskfmt-backing-path", Message: "x"}, codes.FailedPrecondition, "diskfmt-backing-path"},
		{"not a refusal", hostdv1.VM_VM_WORK, errors.New("boom"), codes.Internal, hostservice.RuleInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vms := &fakeVMs{err: tc.err}
			_, err := (&hostservice.Server{VMs: vms}).ResetVMData(t.Context(), &hostdv1.ResetVMDataRequest{Vm: tc.vm})
			code, rule := ruleOf(t, err)
			if code != tc.code || rule != tc.rule {
				t.Errorf("code %s rule %s, want %s %s", code, rule, tc.code, tc.rule)
			}
			if tc.err == nil && len(vms.slots) != 0 {
				t.Error("the manager was called")
			}
		})
	}
}
