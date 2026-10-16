// Package contracttest_test checks the generated contract code in
// internal/gen (B52-proto-contracts). It is outside internal/gen because
// `mise run proto:generate` replaces that tree.
package contracttest_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
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

// messages holds one value of every message in the contracts, with
// every field set, so a round trip that drops a field fails.
func messages() []proto.Message {
	return []proto.Message{
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
	}
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
	files := []protoreflect.FileDescriptor{
		vmdv1.File_wraithbox_vmd_v1_vmd_proto,
		guestv1.File_wraithbox_guest_v1_guest_proto,
	}
	for _, file := range files {
		contractMessages := file.Messages()
		for i := range contractMessages.Len() {
			if name := contractMessages.Get(i).FullName(); !covered[name] {
				t.Errorf("%s has no value in messages()", name)
			}
		}
	}
}

// requireAllFieldsSet fails when a field of m, or of a message in it, is
// unset, so the round trip covers every field.
func requireAllFieldsSet(t *testing.T, m protoreflect.Message) {
	t.Helper()
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if !m.Has(field) {
			t.Errorf("%s is unset", field.FullName())
			continue
		}
		if field.Message() != nil && !field.IsList() && !field.IsMap() {
			requireAllFieldsSet(t, m.Get(field).Message())
		}
	}
}
