package vmhandoff

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// FuzzHandoffMessage feeds arbitrary record bytes into the receiver's
// decode and checks, with request 7 of generation 3 open for port 1023
// (C11-host-daemons, "Hand-off receiver"). The record comes from wb-vmd,
// which runs Virtualization framework code on the guest's behalf, so it
// is untrusted input. An accepted record names the open request, the
// current generation, a direction from the host, and the requested
// port, and closes the request. Every refusal has a rule. A record of
// fewer than 6 bytes can't hold the request ID, the generation and the
// direction, so it is never accepted: the empty record decodes with
// request ID and generation 0, which proto3 gives an absent field.
func FuzzHandoffMessage(f *testing.F) {
	f.Add([]byte{})
	f.Add(marshal(f, good()))
	for _, change := range []func(*vmdv1.HandoffMessage){
		func(m *vmdv1.HandoffMessage) { m.RequestId = 0 },
		func(m *vmdv1.HandoffMessage) { m.Generation = 0 },
		func(m *vmdv1.HandoffMessage) { m.Generation = 2 },
		func(m *vmdv1.HandoffMessage) { m.Direction = 0 },
		func(m *vmdv1.HandoffMessage) { m.DestinationPort = 1024 },
	} {
		m := good()
		change(m)
		f.Add(marshal(f, m))
	}
	f.Add(protowire.AppendVarint(protowire.AppendTag(marshal(f, good()), 9, protowire.VarintType), 1))
	f.Add(make([]byte, MaxRecord+1))

	f.Fuzz(func(t *testing.T, record []byte) {
		tb, want := openTable(t)
		msg, got, ref := tb.check(record)
		if ref != nil {
			if ref.Rule == "" {
				t.Fatal("refusal without a rule")
			}
			if got != nil && tb.open[7] != nil {
				t.Fatal("a refused record named the open request but left it open")
			}
			return
		}
		requireAccepted(t, tb, record, msg, got, want)
	})
}

func requireAccepted(t *testing.T, tb *table, record []byte, msg *vmdv1.HandoffMessage, got, want *request) {
	t.Helper()
	switch {
	case len(record) < 6:
		t.Fatalf("accepted a record of %d bytes", len(record))
	case got != want:
		t.Fatal("accepted for another request")
	case msg.GetRequestId() != 7 || msg.GetGeneration() != 3:
		t.Fatalf("accepted request %d of generation %d", msg.GetRequestId(), msg.GetGeneration())
	case msg.GetDirection() != vmdv1.Direction_DIRECTION_HOST_TO_GUEST:
		t.Fatalf("accepted direction %v", msg.GetDirection())
	case msg.GetDestinationPort() != 1023:
		t.Fatalf("accepted port %d", msg.GetDestinationPort())
	case tb.open[7] != nil:
		t.Fatal("the accepted request is still open")
	}
}
