package hostservice

import (
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// statusOf is slot's state with its guest link's. The link counts only
// while the VM runs, and only for the VM's current generation: a link
// state of an older run reads as connecting.
func (s *Server) statusOf(slot vm.Slot, v hostdv1.VM) *hostdv1.VMStatus {
	st := s.VMs.Status(slot)
	out := &hostdv1.VMStatus{
		Vm: v, Phase: phaseOf(st.Phase), Generation: st.Generation,
		LinkState: hostdv1.LinkState_LINK_STATE_DOWN, LastError: vmErrorOf(st.LastError),
	}
	if st.Phase != vm.PhaseRunning || s.Links == nil {
		return out
	}
	link := s.Links.Status(slot)
	if link.Generation != st.Generation {
		out.LinkState = hostdv1.LinkState_LINK_STATE_CONNECTING
		return out
	}
	out.LinkState = linkStateOf(link.State)
	switch link.State {
	case guestconn.StateLinked:
		out.GuestVersion = link.GuestVersion
	case guestconn.StateRefused:
		out.LinkRefusal = linkRefusalOf(link.Refusal)
	case guestconn.StateDown, guestconn.StateConnecting:
	}
	return out
}

func phaseOf(p vm.Phase) hostdv1.VMPhase {
	switch p {
	case vm.PhaseStopped:
		return hostdv1.VMPhase_VM_PHASE_STOPPED
	case vm.PhaseStarting:
		return hostdv1.VMPhase_VM_PHASE_STARTING
	case vm.PhaseRunning:
		return hostdv1.VMPhase_VM_PHASE_RUNNING
	case vm.PhaseStopping:
		return hostdv1.VMPhase_VM_PHASE_STOPPING
	}
	return hostdv1.VMPhase_VM_PHASE_UNSPECIFIED
}

func linkStateOf(s guestconn.State) hostdv1.LinkState {
	switch s {
	case guestconn.StateDown:
		return hostdv1.LinkState_LINK_STATE_DOWN
	case guestconn.StateConnecting:
		return hostdv1.LinkState_LINK_STATE_CONNECTING
	case guestconn.StateLinked:
		return hostdv1.LinkState_LINK_STATE_LINKED
	case guestconn.StateRefused:
		return hostdv1.LinkState_LINK_STATE_REFUSED
	}
	return hostdv1.LinkState_LINK_STATE_UNSPECIFIED
}

// linkRefusalOf passes the refused hello on. guestconn already quoted
// and cut the guest's version (version.Label).
func linkRefusalOf(r *guestconn.HelloRefusal) *hostdv1.LinkRefusal {
	if r == nil {
		return nil
	}
	return &hostdv1.LinkRefusal{
		Rule: r.Rule, GuestVersion: r.GuestVersion, HostdVersion: r.OwnVersion, Message: r.Message,
	}
}

// vmErrorOf copies the manager's copy of a framework error, whose text
// fields the manager cut.
func vmErrorOf(e *vmdv1.VMError) *hostdv1.VMError {
	if e == nil {
		return nil
	}
	return &hostdv1.VMError{
		Code: e.GetCode(), FailureReason: e.GetFailureReason(), Errno: e.GetErrno(), Description: e.GetDescription(),
	}
}
