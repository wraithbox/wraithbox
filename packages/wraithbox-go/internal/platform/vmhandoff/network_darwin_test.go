package vmhandoff

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
)

// dgramPair returns a connected AF_UNIX datagram socketpair: the
// transport's host end and the end that stands in for the VM.
func dgramPair(t *testing.T) (host, vm int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fds[0], fds[1]
}

func networkRecord(t *testing.T, change func(*vmdv1.HandoffMessage)) []byte {
	m := goodNetwork()
	if change != nil {
		change(m)
	}
	return marshal(t, m)
}

// TestReceiveNetwork: a good KIND_NETWORK record gives the expected
// generation a Transport of the descriptor that was sent, and the
// receiver keeps no other copy (C11-host-daemons, "Hand-off receiver").
func TestReceiveNetwork(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.ExpectNetwork(3)
	if err != nil {
		t.Fatal(err)
	}
	host, vm := dgramPair(t)
	defer func() { _ = syscall.Close(vm) }()
	f.sendRaw(t, networkRecord(t, nil), host)
	before := openFDs()
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	transport, err := p.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The held descriptor is the sent one: a frame the VM end sends
	// reaches it.
	if _, err := syscall.Write(vm, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	if n, err := syscall.Read(int(transport.f.Fd()), buf); err != nil || string(buf[:n]) != "frame" {
		t.Fatalf("transport read %q, %v", buf[:n], err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if after := openFDs(); !maps.Equal(before, after) {
		t.Errorf("descriptors open after the transport closed: %v, before: %v", keys(after), keys(before))
	}
	if !strings.Contains(f.log.String(), `"msg":"packet transport handed off","decision":"accept","kind":"KIND_NETWORK"`) {
		t.Errorf("log %s has no accepted transport", f.log.String())
	}
}

// TestNetworkRefusals: over a real socketpair, a KIND_NETWORK record
// with the wrong descriptor, fields or generation is refused with
// handoff-network-refused, logged with it, and leaves no descriptor
// open. A refusal that reaches the expected generation fails its Wait
// at once.
func TestNetworkRefusals(t *testing.T) {
	cases := []struct {
		name        string
		change      func(*vmdv1.HandoffMessage)
		fds         func(*testing.T) []int
		rule        Rule
		failsWaiter bool
	}{
		{"a stream socket", nil, oneStream, RuleNetwork, true},
		{"a pipe", nil, pipeEnd, RuleNetwork, true},
		{"no descriptor", nil, noFDs, RuleFDCount, true},
		{"a request ID", func(m *vmdv1.HandoffMessage) { m.RequestId = 7 }, dgramEnd, RuleNetwork, true},
		{"a port", func(m *vmdv1.HandoffMessage) { m.DestinationPort = 1023 }, dgramEnd, RuleNetwork, true},
		{"older generation", func(m *vmdv1.HandoffMessage) { m.Generation = 2 }, dgramEnd, RuleNetwork, false},
		{"newer generation", func(m *vmdv1.HandoffMessage) { m.Generation = 4 }, dgramEnd, RuleNetwork, false},
		{"no kind", func(m *vmdv1.HandoffMessage) { m.Kind = 0 }, dgramEnd, RuleKind, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			p, err := f.rx.ExpectNetwork(3)
			if err != nil {
				t.Fatal(err)
			}
			f.sendRaw(t, networkRecord(t, c.change), c.fds(t)...)
			before := openFDs()
			err = f.rx.Receive()
			if after := openFDs(); !maps.Equal(before, after) {
				t.Errorf("descriptors open after the refusal: %v, before: %v", keys(after), keys(before))
			}
			if ruleOf(err) != string(c.rule) {
				t.Fatalf("Receive = %v, want %s", err, c.rule)
			}
			if !strings.Contains(f.log.String(), `"rule":"`+string(c.rule)+`"`) {
				t.Errorf("log %s has no rule %s", f.log.String(), c.rule)
			}
			checkWaitFails(t, p, c.failsWaiter, c.rule)
		})
	}
}

func checkWaitFails(t *testing.T, p *PendingNetwork, failsWaiter bool, rule Rule) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := p.Wait(ctx)
	switch {
	case failsWaiter && ruleOf(err) != string(rule):
		t.Errorf("Wait = %v, want %s at once", err, rule)
	case !failsWaiter && ctx.Err() == nil:
		t.Errorf("Wait = %v, want it still waiting", err)
	}
}

// TestNetworkOnce: a generation's second KIND_NETWORK record is refused
// and closed, and the first transport keeps working.
func TestNetworkOnce(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.ExpectNetwork(3)
	if err != nil {
		t.Fatal(err)
	}
	host, vm := dgramPair(t)
	defer func() { _ = syscall.Close(vm) }()
	f.sendRaw(t, networkRecord(t, nil), host)
	if err := f.rx.Receive(); err != nil {
		t.Fatal(err)
	}
	transport, err := p.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	f.sendRaw(t, networkRecord(t, nil), dgramEnd(t)...)
	before := openFDs()
	if err := f.rx.Receive(); ruleOf(err) != string(RuleNetwork) {
		t.Fatalf("second record: Receive = %v, want %s", err, RuleNetwork)
	}
	if after := openFDs(); !maps.Equal(before, after) {
		t.Errorf("descriptors open after the refusal: %v, before: %v", keys(after), keys(before))
	}
	if _, err := f.rx.ExpectNetwork(3); ruleOf(err) != string(RuleRequest) {
		t.Fatalf("ExpectNetwork(3) again = %v, want %s", err, RuleRequest)
	}
}

// TestNetworkCancelAndClose: a cancelled transport request refuses its
// late record, a newer generation fails the older request, and Close
// fails the open one.
func TestNetworkCancelAndClose(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.ExpectNetwork(3)
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	f.sendRaw(t, networkRecord(t, nil), dgramEnd(t)...)
	if err := f.rx.Receive(); ruleOf(err) != string(RuleNetwork) {
		t.Fatalf("late record: Receive = %v, want %s", err, RuleNetwork)
	}
	old, err := f.rx.ExpectNetwork(4)
	if err != nil {
		t.Fatal(err)
	}
	open, err := f.rx.ExpectNetwork(5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Wait(t.Context()); ruleOf(err) != string(RuleGeneration) {
		t.Fatalf("older generation: Wait = %v, want %s", err, RuleGeneration)
	}
	if err := f.rx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := open.Wait(t.Context()); ruleOf(err) != string(RuleClosed) {
		t.Fatalf("after Close: Wait = %v, want %s", err, RuleClosed)
	}
}

// TestTransportHasNoReadOrWrite: the only method of the type that holds
// the packet transport is Close (S07-02-packet-path, "Packet transport").
func TestTransportHasNoReadOrWrite(t *testing.T) {
	typ := reflect.TypeFor[*Transport]()
	var names []string
	for m := range typ.Methods() {
		names = append(names, m.Name)
	}
	if !slices.Equal(names, []string{"Close"}) {
		t.Fatalf("*Transport has methods %v, want only Close", names)
	}
}

// TestNetworkAfterCancel: once a start cancels its request for the
// packet transport, the generation can't ask again, and the record
// wb-vmd sent before it learned of that is refused with
// handoff-network-refused, closed, and logged at info as late. A second
// record of the generation is refused at warn.
func TestNetworkAfterCancel(t *testing.T) {
	f := newFixture(t)
	p, err := f.rx.ExpectNetwork(3)
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	if _, err := f.rx.ExpectNetwork(3); ruleOf(err) != string(RuleRequest) {
		t.Fatalf("ExpectNetwork after Cancel = %v, want %s", err, RuleRequest)
	}
	for i, level := range []string{"INFO", "WARN"} {
		host, vm := dgramPair(t)
		f.sendRaw(t, networkRecord(t, nil), host)
		_ = syscall.Close(vm)
		f.log.Reset()
		before := openFDs()
		if err := f.rx.Receive(); ruleOf(err) != string(RuleNetwork) {
			t.Fatalf("record %d: Receive = %v, want %s", i, err, RuleNetwork)
		}
		if after := openFDs(); !maps.Equal(before, after) {
			t.Errorf("record %d: descriptors open after the refusal: %v, before: %v", i, keys(after), keys(before))
		}
		if want := `"level":"` + level + `","msg":"hand-off refused","rule":"handoff-network-refused"`; !strings.Contains(f.log.String(), want) {
			t.Errorf("record %d: log %s has no %s", i, f.log.String(), want)
		}
	}
}
