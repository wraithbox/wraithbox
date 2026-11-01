// Package contracttest_test checks the generated contract code in
// internal/gen (B52-proto-contracts). It is outside internal/gen because
// `mise run proto:generate` replaces that tree.
package contracttest_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// vmState is a VMState with every field set, the nested VMError too.
func vmState() *vmdv1.VMState {
	return &vmdv1.VMState{
		Generation: 7,
		State:      vmdv1.State_STATE_ERROR,
		Error: &vmdv1.VMError{
			Code:          12,
			FailureReason: "Permission denied",
			Errno:         13,
			Description:   "The virtual machine failed to restore.",
		},
	}
}

// vmStatus is a VMStatus with every field set, its nested messages
// included.
func vmStatus() *hostdv1.VMStatus {
	return &hostdv1.VMStatus{
		Vm: hostdv1.VM_VM_WORK, Phase: hostdv1.VMPhase_VM_PHASE_RUNNING, Generation: 7,
		LinkState: hostdv1.LinkState_LINK_STATE_REFUSED, GuestVersion: "1.2.3",
		LinkRefusal: &hostdv1.LinkRefusal{
			Rule: "peer-version-mismatch", GuestVersion: `"1.2.4"`, HostdVersion: `"1.2.3"`, Message: "stop and start",
		},
		LastError: &hostdv1.VMError{Code: 6, FailureReason: "limit", Errno: 1, Description: "too many VMs"},
		DataMount: &hostdv1.DataMount{
			State: hostdv1.DataMountState_DATA_MOUNT_STATE_REFUSED, Rule: "data-mount-flags", Message: "stop and reset",
		},
		AuditRefusal: &hostdv1.AuditRefusal{Rule: "audit-free-space", Message: "free space", UntilUnix: 1700000000},
	}
}

// setup is a terminal Setup with every field set.
func setup() *terminalv1.Setup {
	return &terminalv1.Setup{
		Interactive:    true,
		SeparateStderr: true,
		Size:           &terminalv1.WindowSize{Rows: 50, Cols: 132},
		Env:            []*terminalv1.EnvVar{{Name: "TERM", Value: "xterm-ghostty"}},
	}
}

// clientFrames holds one ClientFrame for each member of its oneof.
func clientFrames() []*terminalv1.ClientFrame {
	return []*terminalv1.ClientFrame{
		{Frame: &terminalv1.ClientFrame_Input{Input: []byte("ls\r")}},
		{Frame: &terminalv1.ClientFrame_Resize{Resize: &terminalv1.WindowSize{Rows: 24, Cols: 80}}},
		{Frame: &terminalv1.ClientFrame_Signal{Signal: terminalv1.Signal_SIGNAL_INT}},
		{Frame: &terminalv1.ClientFrame_Stop{Stop: &terminalv1.Stop{}}},
		{Frame: &terminalv1.ClientFrame_Resume{Resume: &terminalv1.Resume{}}},
		{Frame: &terminalv1.ClientFrame_StdinClosed{StdinClosed: &terminalv1.StdinClosed{}}},
		{Frame: &terminalv1.ClientFrame_StdoutClosed{StdoutClosed: &terminalv1.StdoutClosed{}}},
		{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}},
	}
}

// serverFrames holds one ServerFrame for each member of its oneof, and
// of Exit's.
func serverFrames() []*terminalv1.ServerFrame {
	return []*terminalv1.ServerFrame{
		{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{Stream: terminalv1.Stream_STREAM_STDERR, Data: []byte("\x1b[1mok")}}},
		{Frame: &terminalv1.ServerFrame_Stopped{Stopped: &terminalv1.Stopped{}}},
		{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 3}}}},
		{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Signal{Signal: 9}}}},
		{Frame: &terminalv1.ServerFrame_Failure{Failure: &terminalv1.Failure{Rule: "guest-lost", Message: "The guest stopped answering."}}},
	}
}

// terminalMessages holds a value of every message of the terminal
// stream and of the two Shell calls that carry it, each oneof member
// set in one of them.
func terminalMessages() []proto.Message {
	msgs := []proto.Message{
		setup(),
		&terminalv1.WindowSize{Rows: 1, Cols: 65535},
		&terminalv1.EnvVar{Name: "LANG", Value: "en_US.UTF-8"},
		&terminalv1.Stop{},
		&terminalv1.Resume{},
		&terminalv1.StdinClosed{},
		&terminalv1.StdoutClosed{},
		&terminalv1.Hangup{},
		&terminalv1.Output{Stream: terminalv1.Stream_STREAM_STDOUT, Data: []byte("hi")},
		&terminalv1.Stopped{},
		&terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 255}},
		&terminalv1.Failure{Rule: "relay-frame-bound", Message: "An output frame was over 32 KiB."},
		&guestv1.ShellStart{ShellId: "myrepo-20261021-1412-k3f9", User: "wbp1", Setup: setup()},
		&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Start{Start: &guestv1.ShellStart{ShellId: "a-20261021-1412-k3f9", User: "wbp1", Setup: setup()}}},
		&hostdv1.ShellOpen{ProjectId: "0123456789abcdef0123456789abcdef", Setup: setup()},
		&hostdv1.ShellOpened{ShellId: "myrepo-20261021-1412-k3f9", ProjectName: "myrepo", GuestUser: "wbp1"},
		&hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Open{Open: &hostdv1.ShellOpen{ProjectId: "0123456789abcdef0123456789abcdef", Setup: setup()}}},
		&hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Opened{Opened: &hostdv1.ShellOpened{ShellId: "s", ProjectName: "p", GuestUser: "u"}}},
	}
	for _, f := range clientFrames() {
		msgs = append(msgs, f,
			&guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Frame{Frame: f}},
			&hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Frame{Frame: f}})
	}
	for _, f := range serverFrames() {
		msgs = append(msgs, f,
			&guestv1.ShellResponse{Frame: f},
			&hostdv1.ShellResponse{Msg: &hostdv1.ShellResponse_Frame{Frame: f}})
	}
	return msgs
}

// messages holds one value of every message in the contracts, with
// every field set, so a round trip that drops a field fails. Of a
// oneof, each value sets one member, and TestEveryOneofMemberCovered
// checks that every member is set in some value.
func messages() []proto.Message {
	return append([]proto.Message{
		&vmdv1.StartRequest{Generation: 1, CpuCount: 4, MemoryMib: 8192},
		&vmdv1.StartResponse{},
		&vmdv1.StopRequest{Generation: 2, Mode: vmdv1.StopMode_STOP_MODE_FORCE},
		&vmdv1.StopResponse{},
		&vmdv1.GetStateRequest{},
		&vmdv1.GetStateResponse{State: vmState()},
		&vmdv1.WatchStateRequest{},
		&vmdv1.WatchStateResponse{State: vmState()},
		vmState(),
		vmState().GetError(),
		&vmdv1.ConnectGuestRequest{Generation: 3, RequestId: 41, GuestPort: 700},
		&vmdv1.ConnectGuestResponse{},
		&vmdv1.HandoffMessage{
			RequestId:       41,
			Generation:      3,
			Direction:       vmdv1.Direction_DIRECTION_HOST_TO_GUEST,
			SourcePort:      1_000_000,
			DestinationPort: 700,
		},
		&guestv1.HelloRequest{},
		&guestv1.HelloResponse{
			GuestdVersion:  "1.2.3-rc.1+build.5",
			ServedPackages: []string{"wraithbox.guest.v1"},
		},
		&guestv1.SetTimeRequest{UnixNanos: 1_792_591_200_123_456_789, Reason: guestv1.TimeReason_TIME_REASON_PERIODIC},
		&guestv1.SetTimeResponse{},
		&guestv1.MountDataRequest{VolumeUuid: "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0", FirstMount: true},
		&guestv1.MountDataResponse{},
		&hostdv1.ResetVMDataRequest{Vm: hostdv1.VM_VM_ISOLATED},
		&hostdv1.ResetVMDataResponse{OldVolumeUuid: "A", NewVolumeUuid: "B"},
		&hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK},
		&hostdv1.StartVMResponse{Generation: 7},
		&hostdv1.StopVMRequest{Vm: hostdv1.VM_VM_ISOLATED},
		&hostdv1.StopVMResponse{},
		&hostdv1.GetVMStatusRequest{Vm: hostdv1.VM_VM_WORK},
		&hostdv1.GetVMStatusResponse{Status: vmStatus()},
		vmStatus(),
		vmStatus().GetLinkRefusal(),
		vmStatus().GetLastError(),
		vmStatus().GetDataMount(),
		vmStatus().GetAuditRefusal(),
	}, append(terminalMessages(), userMessages()...)...)
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, msg := range messages() {
		name := string(msg.ProtoReflect().Descriptor().FullName())
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireAllFieldsSet(t, msg.ProtoReflect())
			wire, err := proto.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := msg.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(wire, got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !proto.Equal(msg, got) {
				t.Fatalf("round trip changed the message:\nsent %v\ngot  %v", msg, got)
			}
		})
	}
}

// TestEveryMessageCovered fails when a contract gains a message that
// messages() doesn't hold.
func TestEveryMessageCovered(t *testing.T) {
	t.Parallel()
	covered := map[protoreflect.FullName]bool{}
	for _, msg := range messages() {
		covered[msg.ProtoReflect().Descriptor().FullName()] = true
	}
	for _, file := range files() {
		contractMessages := file.Messages()
		for i := range contractMessages.Len() {
			if name := contractMessages.Get(i).FullName(); !covered[name] {
				t.Errorf("%s has no value in messages()", name)
			}
		}
	}
}

// files is every contract file.
func files() []protoreflect.FileDescriptor {
	return []protoreflect.FileDescriptor{
		vmdv1.File_wraithbox_vmd_v1_vmd_proto,
		guestv1.File_wraithbox_guest_v1_guest_proto,
		hostdv1.File_wraithbox_hostd_v1_hostd_proto,
		hostdv1.File_wraithbox_hostd_v1_shell_proto,
		terminalv1.File_wraithbox_terminal_v1_terminal_proto,
	}
}

// TestEveryOneofMemberCovered fails when a member of a oneof is set in
// no value of messages(), so the round trip covers every member.
func TestEveryOneofMemberCovered(t *testing.T) {
	t.Parallel()
	set := map[protoreflect.FullName]bool{}
	for _, msg := range messages() {
		markSet(set, msg.ProtoReflect())
	}
	for _, name := range oneofMembers() {
		if !set[name] {
			t.Errorf("%s is set in no value of messages()", name)
		}
	}
}

// oneofMembers returns the fields of every oneof in the contracts.
func oneofMembers() []protoreflect.FullName {
	var names []protoreflect.FullName
	for _, file := range files() {
		msgs := file.Messages()
		for i := range msgs.Len() {
			oneofs := msgs.Get(i).Oneofs()
			for j := range oneofs.Len() {
				fields := oneofs.Get(j).Fields()
				for k := range fields.Len() {
					names = append(names, fields.Get(k).FullName())
				}
			}
		}
	}
	return names
}

// markSet records in set every field set in m or in a message in it.
func markSet(set map[protoreflect.FullName]bool, m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		set[fd.FullName()] = true
		if fd.Message() != nil && !fd.IsList() && !fd.IsMap() {
			markSet(set, v.Message())
		}
		return true
	})
}

// requireAllFieldsSet fails when a field of m, or of a message in it, is
// unset, so the round trip covers every field. Of a oneof, it needs one
// member set, and checks that one.
func requireAllFieldsSet(t *testing.T, m protoreflect.Message) {
	t.Helper()
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if otherMember(t, m, field) {
			continue
		}
		if !m.Has(field) {
			t.Errorf("%s is unset", field.FullName())
			continue
		}
		if field.Message() != nil && !field.IsList() && !field.IsMap() {
			requireAllFieldsSet(t, m.Get(field).Message())
		}
	}
}

// otherMember reports whether field is a member of a oneof whose set
// member is another one. It fails when no member of the oneof is set.
func otherMember(t *testing.T, m protoreflect.Message, field protoreflect.FieldDescriptor) bool {
	t.Helper()
	oneof := field.ContainingOneof()
	if oneof == nil || oneof.IsSynthetic() {
		return false
	}
	which := m.WhichOneof(oneof)
	if which == nil {
		t.Errorf("%s has no member set", oneof.FullName())
	}
	return which != field
}
