package vmhandoff

import (
	"strings"
	"syscall"
	"testing"

	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// readAck reads the one HandoffAck the receiver sent back to wb-vmd's
// end, without waiting, and fails when it carries a descriptor or
// another record follows.
func (f *fixture) readAck(t *testing.T) *vmdv1.HandoffAck {
	t.Helper()
	buf := make([]byte, MaxRecord+1)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	n, controlLen, flags, _, err := syscall.Recvmsg(f.send, buf, oob, syscall.MSG_DONTWAIT)
	if err != nil {
		t.Fatalf("no acknowledgment: %v", err)
	}
	if controlLen != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
		t.Fatalf("acknowledgment with %d bytes of control data, flags %#x", controlLen, flags)
	}
	ack := &vmdv1.HandoffAck{}
	if err := proto.Unmarshal(buf[:n], ack); err != nil {
		t.Fatalf("acknowledgment doesn't decode: %v", err)
	}
	f.requireNoAck(t)
	return ack
}

// requireNoAck fails when a record is queued for wb-vmd's end.
func (f *fixture) requireNoAck(t *testing.T) {
	t.Helper()
	buf := make([]byte, MaxRecord+1)
	if n, _, _, _, err := syscall.Recvmsg(f.send, buf, nil, syscall.MSG_DONTWAIT); err == nil {
		t.Fatalf("unexpected record of %d bytes for wb-vmd", n)
	}
}

func requireAck(t *testing.T, got *vmdv1.HandoffAck, kind vmdv1.Kind, id, generation uint64, accepted bool) {
	t.Helper()
	if got.GetKind() != kind || got.GetRequestId() != id || got.GetGeneration() != generation || got.GetAccepted() != accepted {
		t.Fatalf("acknowledgment %v, want kind %v, request %d, generation %d, accepted %v", got, kind, id, generation, accepted)
	}
}

// TestAckAccepted is C11-host-daemons, "Hand-off receiver": an accepted
// connection and an accepted packet transport are each answered with a
// HandoffAck that names the record, once Receive has the descriptor
// (S07-05-stream-hand-off, "Transport").
func TestAckAccepted(t *testing.T) {
	f := newFixture(t)
	if _, err := f.rx.Expect(7, 3, 1023); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rx.ExpectNetwork(3); err != nil {
		t.Fatal(err)
	}
	c, guest := streamPair(t)
	defer func() { _ = syscall.Close(guest) }()
	f.sendRaw(t, marshal(t, good()), c)
	f.requireNoAck(t)
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	requireAck(t, f.readAck(t), vmdv1.Kind_KIND_CONNECTION, 7, 3, true)

	host, vm := dgramPair(t)
	defer func() { _ = syscall.Close(vm) }()
	f.sendRaw(t, networkRecord(t, nil), host)
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	requireAck(t, f.readAck(t), vmdv1.Kind_KIND_NETWORK, 0, 3, true)
}

// TestAckRefused: a record that decodes and is refused is answered with
// accepted false, so wb-vmd closes its copy at once. A record that
// doesn't decode names nothing and gets no acknowledgment.
func TestAckRefused(t *testing.T) {
	cases := []struct {
		name   string
		record func(*testing.T) []byte
		fds    func(*testing.T) []int
		want   *vmdv1.HandoffAck
	}{
		{
			"other port", changed(func(m *vmdv1.HandoffMessage) { m.DestinationPort = 22 }), oneStream,
			&vmdv1.HandoffAck{Kind: vmdv1.Kind_KIND_CONNECTION, RequestId: 7, Generation: 3},
		},
		{
			"request not open", changed(func(m *vmdv1.HandoffMessage) { m.RequestId = 8 }), oneStream,
			&vmdv1.HandoffAck{Kind: vmdv1.Kind_KIND_CONNECTION, RequestId: 8, Generation: 3},
		},
		{
			"older generation", changed(func(m *vmdv1.HandoffMessage) { m.Generation = 2 }), oneStream,
			&vmdv1.HandoffAck{Kind: vmdv1.Kind_KIND_CONNECTION, RequestId: 7, Generation: 2},
		},
		{
			"a pipe", goodRecord, pipeEnd,
			&vmdv1.HandoffAck{Kind: vmdv1.Kind_KIND_CONNECTION, RequestId: 7, Generation: 3},
		},
		{
			"no kind", changed(func(m *vmdv1.HandoffMessage) { m.Kind = 0 }), oneStream,
			&vmdv1.HandoffAck{RequestId: 7, Generation: 3},
		},
		{"not protobuf", bytesOf([]byte{0xff, 0xff}), oneStream, nil},
		{"over 256 bytes", bytesOf(make([]byte, MaxRecord+1)), oneStream, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.rx.Expect(7, 3, 1023); err != nil {
				t.Fatal(err)
			}
			f.sendRaw(t, c.record(t), c.fds(t)...)
			if err := f.rx.Receive(); ruleOf(err) == "" {
				t.Fatalf("Receive = %v, want a refusal", err)
			}
			if c.want == nil {
				f.requireNoAck(t)
				return
			}
			requireAck(t, f.readAck(t), c.want.GetKind(), c.want.GetRequestId(), c.want.GetGeneration(), false)
		})
	}
}

// TestAckSendFailed: an acknowledgment that can't be sent, here because
// wb-vmd's end is closed, is logged with handoff-ack-send-failed, and
// the record's decision stands.
func TestAckSendFailed(t *testing.T) {
	f := newFixture(t)
	if _, err := f.rx.Expect(7, 3, 1023); err != nil {
		t.Fatal(err)
	}
	c, guest := streamPair(t)
	defer func() { _ = syscall.Close(guest) }()
	f.sendRaw(t, marshal(t, good()), c)
	_ = syscall.Close(f.send)
	f.send = -1
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	log := f.log.String()
	if !strings.Contains(log, `"rule":"`+string(RuleAckSendFailed)+`"`) || !strings.Contains(log, `"decision":"accept"`) {
		t.Fatalf("log %s has no accept and no %s", log, RuleAckSendFailed)
	}
}
