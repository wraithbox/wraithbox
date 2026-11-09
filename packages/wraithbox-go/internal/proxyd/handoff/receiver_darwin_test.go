package handoff

import (
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// The C02-streams-and-tls cases of "Stream hand-off" that are
// wb-proxyd's, over real fdchan socketpairs. Two VMs at different
// generations: work (VM A) at 3 and isolated (VM B) at 7.

const (
	genA = 3
	genB = 7
)

// twoVMs starts a receiver with VM A allowed a.example.com and
// *.wild.example.com on 443 with a wildcard budget of 1, and VM B
// allowed b.example.org on 443.
func twoVMs(t *testing.T, caps Caps) (h *harness, a, b *netd) {
	t.Helper()
	h = newHarness(t, caps)
	h.start()
	h.policy("work", 1, policyProto(1, endpoint("a.example.com", 443), endpoint("*.wild.example.com", 443)))
	h.policy("isolated", 1, policyProto(1, endpoint("b.example.org", 443)))
	a = h.addVM("work", genA)
	b = h.addVM("isolated", genB)
	return h, a, b
}

// TestStreamChecks sends VM A the refused streams of C02, each reset
// and logged with stream-handoff-refused and its check, and sees the
// handler get only the streams that pass.
func TestStreamChecks(t *testing.T) {
	h, a, b := twoVMs(t, Caps{Streams: 2, Pending: 8})
	a.report("a.example.com", wire(1))
	b.report("b.example.org", wire(2))

	dgram, _ := socketpair(t, syscall.SOCK_DGRAM)
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	inet := os.NewFile(uintptr(fd), "inet")
	t.Cleanup(func() { _ = inet.Close() })

	for _, tc := range []struct {
		name  string
		send  func(id uint64)
		check egressmsg.Check
	}{
		{"the generation of VM B", func(id uint64) { a.stream(genB, wire(1), 443, id) }, CheckGeneration},
		{"an address only VM B's copy holds", func(id uint64) { a.stream(genA, wire(2), 443, id) }, CheckNoEntry},
		{"an address with no entry", func(id uint64) { a.stream(genA, wire(9), 443, id) }, CheckNoEntry},
		{"a port the policy doesn't allow", func(id uint64) { a.stream(genA, wire(1), 80, id) }, CheckPortNotAllowed},
		{"a datagram socket", func(id uint64) { a.send(streamMsg(genA, wire(1), 443, id), dgram) }, CheckSocketType},
		{"a socket that isn't a Unix domain socket", func(id uint64) {
			a.send(streamMsg(genA, wire(1), 443, id), inet)
		}, CheckSocketType},
	} {
		t.Run(tc.name, func(*testing.T) {
			tc.send(100)
			h.expectEvent("work", RuleStreamHandoffRefused, tc.check)
			a.expectReset(100)
			h.noStream()
		})
	}
	h.expectLog(`"rule":"stream-handoff-refused"`, `"check":"generation"`, `"vm":"work"`, `"stream_id":100`)

	t.Run("streams past the cap, while VM B's pass", func(t *testing.T) {
		for id := uint64(1); id <= 2; id++ {
			a.stream(genA, wire(1), 443, id)
			s := h.expectStream()
			if s.VM != "work" || s.Generation != genA || s.ID != id || s.Name.String() != "a.example.com" || s.Port != 443 {
				t.Fatalf("stream = %+v", s)
			}
		}
		a.stream(genA, wire(1), 443, 3)
		h.expectEvent("work", RuleStreamHandoffRefused, CheckStreamCap)
		a.expectReset(3)
		b.stream(genB, wire(2), 443, 1)
		if s := h.expectStream(); s.VM != "isolated" || s.Name.String() != "b.example.org" {
			t.Fatalf("VM B's stream = %+v", s)
		}
	})
}

// TestNameReportsRefused sends VM A a name only VM B's policy allows and
// new names under a wildcard past the budget. Each is refused with
// name-report-refused and its check, and a stream to it is refused with
// no-entry.
func TestNameReportsRefused(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("b.example.org", wire(3))
	h.expectEvent("work", RuleNameReportRefused, CheckNotAllowed)
	a.report("x.wild.example.com", wire(4))
	a.report("y.wild.example.com", wire(5))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)
	h.expectLog(`"rule":"name-report-refused"`, `"check":"wildcard-budget"`, `"name":"\"y.wild.example.com\""`)
	for i, addr := range []uint32{3, 5} {
		a.stream(genA, wire(addr), 443, uint64(i+1))
		a.expectReset(uint64(i + 1))
		h.expectEvent("work", RuleStreamHandoffRefused, CheckNoEntry)
	}
	a.stream(genA, wire(4), 443, 9)
	if s := h.expectStream(); s.Name.String() != "x.wild.example.com" {
		t.Fatalf("stream = %+v", s)
	}
	if got := h.wild.written(); !slices.Equal(got, []string{"work/x.wild.example.com"}) {
		t.Fatalf("wildcard names written = %q", got)
	}
}

// TestMalformedClosesSocket sends each malformed message on a socket
// with a stream open. Each closes the hand-off socket and the stream,
// is logged with name-report-malformed and its check, and is reported
// to wb-hostd with HandoffSocketClosed (S07-06-name-mapping, "Malformed
// reports").
func TestMalformedClosesSocket(t *testing.T) {
	for _, tc := range []struct {
		name     string
		send     func(n *netd)
		check    egressmsg.Check
		reported egressmsg.Check
	}{
		{"a name that fails the name form rule", func(n *netd) { n.report("-x-.example.com", wire(2)) }, egressmsg.CheckNameForm, egressmsg.CheckNameForm},
		{"a name with a capital letter", func(n *netd) { n.report("A.example.com", wire(2)) }, egressmsg.CheckNameForm, egressmsg.CheckNameForm},
		{"a name with a trailing dot", func(n *netd) { n.report("a.example.com.", wire(2)) }, egressmsg.CheckNameForm, egressmsg.CheckNameForm},
		{"an address outside the synthetic range", func(n *netd) { n.report("a.example.com", 10<<24|1) }, egressmsg.CheckSyntheticRange, egressmsg.CheckSyntheticRange},
		{"a release outside the synthetic range", func(n *netd) { n.release(10<<24 | 1) }, egressmsg.CheckSyntheticRange, egressmsg.CheckSyntheticRange},
		{"a stream with no descriptor", func(n *netd) { n.send(streamMsg(genA, wire(1), 443, 5), nil) }, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a report with a descriptor", func(n *netd) {
			f, _ := socketpair(n.t, syscall.SOCK_STREAM)
			n.send(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
				NameReport: &egressv1.NameReport{Name: "a.example.com", Address: wire(2)},
			}}, f)
		}, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a stream with two descriptors", func(n *netd) { n.rawSend(n.t, streamRecord(n.t), 2) }, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a stream with three descriptors", func(n *netd) { n.rawSend(n.t, streamRecord(n.t), 3) }, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a stream in a message over 4 KiB", func(n *netd) {
			n.rawSend(n.t, append(streamRecord(n.t), make([]byte, egressmsg.MaxRecord)...), 1)
		}, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a record that doesn't decode", func(n *netd) { n.rawSend(n.t, []byte{0xff, 0xff, 0xff}, 0) }, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
		{"a message of no kind", func(n *netd) { n.rawSend(n.t, []byte{0x78, 0x01}, 0) }, egressmsg.CheckMessageForm, egressmsg.CheckMessageForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, _ := twoVMs(t, DefaultCaps())
			a.report("a.example.com", wire(1))
			open := a.stream(genA, wire(1), 443, 1)
			h.expectStream()
			tc.send(a)
			h.expectEvent("work", RuleNameReportMalformed, tc.check)
			closed := h.recvHostd().GetHandoffSocketClosed()
			if closed.GetVm() != "work" || closed.GetGeneration() != genA || closed.GetRule() != RuleNameReportMalformed ||
				closed.GetCheck() != string(tc.reported) {
				t.Fatalf("HandoffSocketClosed = %v", closed)
			}
			expectEOF(t, open)
			a.expectEnded()
			h.noStream()
		})
	}
}

// streamRecord is a well-formed stream message for VM A.
func streamRecord(t *testing.T) []byte {
	t.Helper()
	b, err := egressmsg.Marshal(streamMsg(genA, wire(1), 443, 7))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rawSend sends a record with n descriptors past fdchan.Send.
func (n *netd) rawSend(t *testing.T, record []byte, fds int) {
	t.Helper()
	var oob []byte
	if fds > 0 {
		s, _ := socketpair(t, syscall.SOCK_STREAM)
		list := make([]int, fds)
		for i := range list {
			list[i] = int(s.Fd())
		}
		oob = syscall.UnixRights(list...)
	}
	if err := syscall.Sendmsg(n.raw, record, oob, nil, 0); err != nil {
		t.Fatal(err)
	}
}

// TestVersionRecordFirst: a hand-off socket whose first record isn't
// wb-netd's version record of this build is malformed, reported as
// message-form.
func TestVersionRecordFirst(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record []byte
	}{
		{"another version", hostversion.Record("9.9.9")},
		{"a name report", func() []byte {
			b, _ := egressmsg.Marshal(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
				NameReport: &egressv1.NameReport{Name: "a.example.com", Address: wire(1)},
			}})
			return b
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, DefaultCaps())
			h.start()
			nd := h.handoff("work", 1)
			// wb-hostd gives wb-netd its end after the ack, so wb-netd's
			// first record never comes before it.
			if ack := h.recvHostd().GetHandoffSocketAck(); ack.GetVm() != "work" {
				t.Fatalf("no HandoffSocketAck first: %v", ack)
			}
			if err := nd.ch.Send(tc.record, nil); err != nil {
				t.Fatal(err)
			}
			h.expectEvent("work", RuleNameReportMalformed, CheckVersion)
			closed := h.recvHostd().GetHandoffSocketClosed()
			if closed.GetCheck() != string(egressmsg.CheckMessageForm) {
				t.Fatalf("HandoffSocketClosed = %v", closed)
			}
			nd.expectEnded()
		})
	}
}

// TestStreamWaitsBehindWildcardReport sends a stream right behind a
// report that waits on wb-hostd for the wildcard budget. The stream
// waits too, and passes once the report is accepted
// (S07-05-stream-hand-off, "Message form").
func TestStreamWaitsBehindWildcardReport(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	gate := make(chan struct{})
	h.wild.mu.Lock()
	h.wild.gate = gate
	h.wild.mu.Unlock()
	a.report("x.wild.example.com", wire(1))
	a.stream(genA, wire(1), 443, 1)
	time.Sleep(20 * time.Millisecond)
	h.noStream()
	close(gate)
	if s := h.expectStream(); s.Name.String() != "x.wild.example.com" {
		t.Fatalf("stream = %+v", s)
	}
}

// TestWildcardWriteBlocked blocks the ProxydWildcardName write for more
// than 100 ms. The name is refused with wildcard-budget, and a stream to
// it with no-entry.
func TestWildcardWriteBlocked(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	h.wild.mu.Lock()
	h.wild.delay = WildcardWriteTimeout + 50*time.Millisecond
	h.wild.mu.Unlock()
	a.report("x.wild.example.com", wire(1))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)
	a.stream(genA, wire(1), 443, 1)
	a.expectReset(1)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckNoEntry)
	h.noStream()
}

// TestAddressConflict reports an address held for another name, once
// with no stream open and once with one open. The old entry goes, and
// the open stream is reset (S07-06-name-mapping, "Address conflicts").
func TestAddressConflict(t *testing.T) {
	h := newHarness(t, DefaultCaps())
	h.start()
	h.policy("work", 1, policyProto(0, endpoint("a.example.com", 443), endpoint("c.example.com", 443)))
	a := h.addVM("work", genA)

	a.report("a.example.com", wire(1))
	a.report("c.example.com", wire(1))
	h.expectEvent("work", RuleNameReportRefused, CheckAddressConflict)
	a.stream(genA, wire(1), 443, 1)
	a.expectReset(1)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckNoEntry)

	a.report("a.example.com", wire(2))
	open := a.stream(genA, wire(2), 443, 2)
	s := h.expectStream()
	a.report("c.example.com", wire(2))
	h.expectEvent("work", RuleNameReportRefused, CheckAddressConflict)
	h.expectEvent("work", RuleAddressConflict, CheckAddressConflict)
	a.expectReset(s.ID)
	expectEOF(t, open)
	a.stream(genA, wire(2), 443, 3)
	a.expectReset(3)

	a.report("c.example.com", wire(2))
	a.stream(genA, wire(2), 443, 4)
	if s := h.expectStream(); s.Name.String() != "c.example.com" {
		t.Fatalf("after a later report, stream = %+v", s)
	}
}

// TestReplayLeavesCopy replays the live entries and sees the copy and
// the wildcard count unchanged (S07-06-name-mapping, "Replay").
func TestReplayLeavesCopy(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("a.example.com", wire(1))
	a.report("x.wild.example.com", wire(2))
	a.barrier()
	before := h.copyOf("work")
	replay := h.addVM("work", genA)
	a.expectEnded()
	replay.report("a.example.com", wire(1))
	replay.report("x.wild.example.com", wire(2))
	replay.barrier()
	if after := h.copyOf("work"); !after.equal(before) {
		t.Fatalf("replay changed the copy: %+v, was %+v", after, before)
	}
	if got := h.wild.written(); len(got) != 1 {
		t.Fatalf("the replay wrote wildcard names again: %q", got)
	}
}

// copySnapshot is a copy of a VM's mapping.
type copySnapshot struct {
	byAddr   map[string]string
	wildcard []string
}

func (c copySnapshot) equal(o copySnapshot) bool {
	if len(c.byAddr) != len(o.byAddr) || !slices.Equal(c.wildcard, o.wildcard) {
		return false
	}
	for k, v := range c.byAddr {
		if o.byAddr[k] != v {
			return false
		}
	}
	return true
}

func (h *harness) copyOf(vm string) copySnapshot {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	m := h.r.vms[vm].copy
	c := copySnapshot{byAddr: map[string]string{}}
	for a, n := range m.byAddr {
		c.byAddr[a.String()] = n.String()
	}
	for n := range m.wildcard {
		c.wildcard = append(c.wildcard, n.String())
	}
	slices.Sort(c.wildcard)
	return c
}

// TestRelease releases an address the copy doesn't hold, which is
// logged and ignored, and one with a stream open, which keeps the
// stream and refuses a new one (S07-06-name-mapping, "Releases").
func TestRelease(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.release(wire(50))
	a.barrier()
	h.expectLog(`"rule":"release-unheld"`, `"address":"198.18.0.50"`)
	a.report("a.example.com", wire(1))
	open := a.stream(genA, wire(1), 443, 1)
	h.expectStream()
	a.release(wire(1))
	a.stream(genA, wire(1), 443, 2)
	a.expectReset(2)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckNoEntry)
	expectOpen(t, open)
}

// TestPolicyChangeRecheck narrows VM A's policy with a stream open. The
// stream is reset with policy-recomputed before the PolicyAck, a stream
// with a registered check that fails is reset too, and a later stream
// to the name is refused with not-allowed.
func TestPolicyChangeRecheck(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("a.example.com", wire(1))
	a.report("x.wild.example.com", wire(2))
	gone := a.stream(genA, wire(1), 443, 1)
	h.expectStream()
	checked := a.stream(genA, wire(2), 443, 2)
	s := h.expectStream()
	budget4 := func(p *policy.Policy) egressmsg.Check {
		if p.WildcardBudget == 4 {
			return "test-check"
		}
		return ""
	}
	if check := s.AddCheck(budget4); check != "" {
		t.Fatalf("AddCheck at hand-off = %q, want \"\"", check)
	}
	kept := a.stream(genA, wire(2), 443, 3)
	keptStream := h.expectStream()

	h.policy("work", 2, policyProto(5, endpoint("*.wild.example.com", 443)))
	a.expectReset(1)
	h.expectEvent("work", RulePolicyRecomputed, CheckNotAllowed)
	expectEOF(t, gone)
	expectOpen(t, checked)
	expectOpen(t, kept)

	h.policy("work", 3, policyProto(4, endpoint("*.wild.example.com", 443)))
	a.expectReset(2)
	h.expectEvent("work", RulePolicyRecomputed, "test-check")
	expectEOF(t, checked)
	expectOpen(t, kept)
	// A check registered after a recompute runs against the policy that
	// recompute applied, not the one at hand-off.
	if got := keptStream.Policy().WildcardBudget; got != 4 {
		t.Fatalf("Policy() budget = %d, want 4", got)
	}
	// A check that fails when it is added resets the stream itself, and
	// logs it once. A later Reset doesn't end it again.
	if check := keptStream.AddCheck(budget4); check != "test-check" {
		t.Fatalf("AddCheck after the recompute = %q, want test-check", check)
	}
	a.expectReset(3)
	h.expectEvent("work", RulePolicyRecomputed, "test-check")
	expectEOF(t, kept)
	select {
	case <-keptStream.Done():
	default:
		t.Fatal("Done isn't closed after the stream ended")
	}
	if keptStream.Reset() || keptStream.Close() {
		t.Fatal("Reset or Close of an ended stream reported that it ended it")
	}

	a.stream(genA, wire(1), 443, 4)
	a.expectReset(4)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckNotAllowed)
}

// TestPolicySequence refuses a PolicyChange whose sequence isn't above
// the last, and ends the control channel.
func TestPolicySequence(t *testing.T) {
	h, _, _ := twoVMs(t, DefaultCaps())
	h.sendPayload(&egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: "work", Sequence: 1},
	}}, policyProto(1))
	h.expectEvent("work", RuleControlRefused, CheckSequence)
	h.expectServeEnded()
}

// TestHandoffSocketRefused sends wb-proxyd hand-off sockets it refuses
// with handoff-socket-refused, and a datagram with two descriptors. Each
// refusal ends the control channel, and with it every hand-off socket.
func TestHandoffSocketRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		send  func(h *harness)
		vm    string
		rule  string
		check egressmsg.Check
	}{
		{"an older generation", func(h *harness) { h.handoffRaw("work", genA-1, syscall.SOCK_DGRAM) }, "work", RuleHandoffSocketRefused, CheckGenerationOlder},
		{"a VM that isn't a VM slot", func(h *harness) { h.handoffRaw("nope", 1, syscall.SOCK_DGRAM) }, "", RuleHandoffSocketRefused, egressmsg.CheckVMSlot},
		{"the build VM", func(h *harness) { h.handoffRaw("build", 1, syscall.SOCK_DGRAM) }, "", RuleHandoffSocketRefused, egressmsg.CheckVMSlot},
		{"a stream socket", func(h *harness) { h.handoffRaw("work", genA+1, syscall.SOCK_STREAM) }, "work", RuleHandoffSocketRefused, CheckSocketType},
		{"no descriptor", func(h *harness) { h.send(handoffSocket("work", genA+1), nil) }, "", RuleHandoffSocketRefused, egressmsg.CheckMessageForm},
		{"two descriptors", func(h *harness) {
			b, err := egressmsg.Marshal(handoffSocket("work", genA+1))
			if err != nil {
				h.t.Fatal(err)
			}
			x, y := socketpair(h.t, syscall.SOCK_DGRAM)
			if err := syscall.Sendmsg(h.raw, b, syscall.UnixRights(int(x.Fd()), int(y.Fd())), nil, 0); err != nil {
				h.t.Fatal(err)
			}
		}, "", RuleControlRefused, egressmsg.CheckMessageForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, DefaultCaps())
			h.start()
			h.policy("work", 1, policyProto(0, endpoint("a.example.com", 443)))
			a := h.addVM("work", genA)
			tc.send(h)
			h.expectEvent(tc.vm, tc.rule, tc.check)
			h.expectServeEnded()
			a.expectEnded()
		})
	}
}

// handoffRaw sends a HandoffSocket with one end of a fresh socketpair of
// the type.
func (h *harness) handoffRaw(vm string, gen uint64, sockType int) {
	h.t.Helper()
	x, _ := socketpair(h.t, sockType)
	h.send(handoffSocket(vm, gen), x)
}

// TestEqualGenerationReplaces sends a second hand-off socket with the
// generation held. It replaces the old one, whose socket and streams
// close, and the copy carries over (S07-05-stream-hand-off, "From
// `wb-hostd`").
func TestEqualGenerationReplaces(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("a.example.com", wire(1))
	open := a.stream(genA, wire(1), 443, 1)
	h.expectStream()
	a2 := h.addVM("work", genA)
	expectEOF(t, open)
	a.expectEnded()
	a2.stream(genA, wire(1), 443, 2)
	if s := h.expectStream(); s.Generation != genA {
		t.Fatalf("stream on the new socket = %+v", s)
	}
	newer := h.addVM("work", genA+1)
	newer.stream(genA, wire(1), 443, 3)
	newer.expectReset(3)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckGeneration)
}

// TestControlOrder refuses a message before ProxydStart and ends the
// control channel.
func TestControlOrder(t *testing.T) {
	h := newHarness(t, DefaultCaps())
	h.sendPayload(&egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: "work", Sequence: 1},
	}}, policyProto(1))
	h.expectEvent("", RuleControlRefused, egressmsg.CheckMessageForm)
	h.expectServeEnded()
}

// TestCAMessageRefused: the receiver has no CA handler yet, so a CA
// message after ProxydStart is refused with control-message-refused and
// ends the control channel, and with it every hand-off socket
// (SEC05-default-deny).
func TestCAMessageRefused(t *testing.T) {
	h := newHarness(t, DefaultCaps())
	h.start()
	h.policy("work", 1, policyProto(0, endpoint("a.example.com", 443)))
	a := h.addVM("work", genA)
	h.send(&egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_CaAccept{
		CaAccept: &egressv1.CaAccept{Vm: "work", Generation: 1},
	}}, nil)
	h.expectEvent("", RuleControlRefused, egressmsg.CheckMessageForm)
	h.expectServeEnded()
	a.expectEnded()
}

// TestStartCountsWildcardNames: names in ProxydStart count toward the
// budget, so a report of one passes without a write and a new name past
// the budget is refused.
func TestStartCountsWildcardNames(t *testing.T) {
	h := newHarness(t, DefaultCaps())
	h.start(&egressv1.WildcardNames{Vm: "work", Names: []string{"x.wild.example.com"}})
	h.policy("work", 1, policyProto(1, endpoint("*.wild.example.com", 443)))
	a := h.addVM("work", genA)
	a.report("x.wild.example.com", wire(1))
	a.report("y.wild.example.com", wire(2))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)
	a.stream(genA, wire(1), 443, 1)
	h.expectStream()
	if got := h.wild.written(); len(got) != 0 {
		t.Fatalf("wildcard names written = %q", got)
	}
}

// TestNoPolicyRefuses: before the first PolicyChange for a VM, every
// report is refused with not-allowed (SEC05-default-deny).
func TestNoPolicyRefuses(t *testing.T) {
	h := newHarness(t, DefaultCaps())
	h.start()
	a := h.addVM("work", genA)
	a.report("a.example.com", wire(1))
	h.expectEvent("work", RuleNameReportRefused, CheckNotAllowed)
}
