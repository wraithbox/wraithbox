package vmhandoff

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// FuzzHandoffMessage feeds arbitrary record bytes into the receiver's
// decode and checks, with request 7 of generation 3 open for port 1023
// and generation 3's packet transport expected (C11-host-daemons,
// "Hand-off receiver"). The record comes from wb-vmd, which runs
// Virtualization framework code on the guest's behalf, so it is
// untrusted input. Every refusal has a rule.
//
// An accepted KIND_CONNECTION record names the open request, the
// current generation, a direction from the host, and the requested
// port, and closes the request. It can't be under 8 bytes: it holds the
// request ID, the generation, the direction and the kind, and proto3
// gives an absent field the value 0.
//
// An accepted KIND_NETWORK record is of the current generation with no
// request ID, direction or port, closes the transport request, and the
// same record checked again is refused: a generation gets one
// (S07-02-packet-path, "Packet transport").
func FuzzHandoffMessage(f *testing.F) {
	f.Add([]byte{})
	f.Add(marshal(f, good()))
	f.Add(marshal(f, goodNetwork()))
	for _, change := range []func(*vmdv1.HandoffMessage){
		func(m *vmdv1.HandoffMessage) { m.RequestId = 0 },
		func(m *vmdv1.HandoffMessage) { m.Generation = 0 },
		func(m *vmdv1.HandoffMessage) { m.Generation = 2 },
		func(m *vmdv1.HandoffMessage) { m.Direction = 0 },
		func(m *vmdv1.HandoffMessage) { m.DestinationPort = 1024 },
		func(m *vmdv1.HandoffMessage) { m.Kind = 0 },
		func(m *vmdv1.HandoffMessage) { m.Kind = vmdv1.Kind_KIND_NETWORK },
	} {
		m := good()
		change(m)
		f.Add(marshal(f, m))
	}
	f.Add(protowire.AppendVarint(protowire.AppendTag(marshal(f, good()), 9, protowire.VarintType), 1))
	f.Add(make([]byte, MaxRecord+1))

	f.Fuzz(func(t *testing.T, record []byte) {
		tb, want := openTable(t)
		network, _, ref := tb.expectNetwork(3)
		if ref != nil {
			t.Fatal(ref)
		}
		msg, got, ref := tb.check(record)
		switch {
		case ref != nil:
			requireRefused(t, tb, ref, got)
		case msg.GetKind() == vmdv1.Kind_KIND_NETWORK:
			requireNetworkAccepted(t, tb, record, msg, got, network)
		default:
			requireAccepted(t, tb, record, msg, got, want)
		}
	})
}

func requireRefused(t *testing.T, tb *table, ref *Refusal, got *request) {
	t.Helper()
	if ref.Rule == "" {
		t.Fatal("refusal without a rule")
	}
	if got != nil && (tb.open[7] == got || tb.network == got) {
		t.Fatal("a refused record named an open request but left it open")
	}
}

func requireAccepted(t *testing.T, tb *table, record []byte, msg *vmdv1.HandoffMessage, got, want *request) {
	t.Helper()
	switch {
	case len(record) < 8:
		t.Fatalf("accepted a record of %d bytes", len(record))
	case msg.GetKind() != vmdv1.Kind_KIND_CONNECTION:
		t.Fatalf("accepted kind %v", msg.GetKind())
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

func requireNetworkAccepted(t *testing.T, tb *table, record []byte, msg *vmdv1.HandoffMessage, got, want *request) {
	t.Helper()
	switch {
	case got != want:
		t.Fatal("accepted for another request")
	case msg.GetGeneration() != 3:
		t.Fatalf("accepted the transport of generation %d", msg.GetGeneration())
	case msg.GetRequestId() != 0 || msg.GetDirection() != 0 || msg.GetSourcePort() != 0 || msg.GetDestinationPort() != 0:
		t.Fatalf("accepted a transport record with fields set: %v", msg)
	case tb.network != nil || tb.open[7] == nil:
		t.Fatal("the transport request is still open, or the connection request was closed")
	}
	if _, _, ref := tb.check(record); ref == nil || ref.Rule != RuleNetwork {
		t.Fatalf("the same record again = %v, want %s", ref, RuleNetwork)
	}
}
