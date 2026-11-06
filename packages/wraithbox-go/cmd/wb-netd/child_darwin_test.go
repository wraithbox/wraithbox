package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"google.golang.org/protobuf/proto"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

const (
	testVM  = "work"
	testGen = 4
)

// lockedBuffer is the child's standard error, read while it runs.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// child is a test binary running as wb-netd, with wb-hostd's ends of its
// channels and the guest's end of its packet transport.
type child struct {
	t          *testing.T
	stderr     *lockedBuffer
	done       chan error
	control    *nt.Peer
	controlCh  *fdchan.Channel
	accounting *nt.Peer
	guest      *net.UnixConn
}

// spawn starts the test binary as wb-netd with files as descriptors 3
// on, and env on top of the child markers. It closes files in the
// parent once the child holds them.
func spawn(t *testing.T, files []*os.File, env ...string) (stderr *lockedBuffer, done chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	// The start interface's environment is empty: only the markers.
	cmd.Env = append([]string{childEnv + "=1", childVersion + "=" + nt.Own}, env...)
	cmd.ExtraFiles = files
	stderr = &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		_ = f.Close()
	}
	done = make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return stderr, done
}

// socketpair returns both ends of an AF_UNIX socketpair of typ.
func socketpair(t *testing.T, typ int) (a, b *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, typ, 0)
	if err != nil {
		t.Fatal(err)
	}
	return os.NewFile(uintptr(fds[0]), "a"), os.NewFile(uintptr(fds[1]), "b")
}

// channel returns wb-hostd's end of a new channel as a peer, and the
// child's end.
func channel(t *testing.T) (*nt.Peer, *os.File) {
	t.Helper()
	p, _, theirs := channelEnds(t)
	return p, theirs
}

// channelEnds is channel, with wb-hostd's end as a channel too.
func channelEnds(t *testing.T) (*nt.Peer, *fdchan.Channel, *os.File) {
	t.Helper()
	ours, theirs, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fdchan.New(ours, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return nt.NewPeer(t, ch), ch, theirs
}

// startChild starts a child with every descriptor of the start
// interface, and exchanges no record yet.
func startChild(t *testing.T, env ...string) *child {
	t.Helper()
	c := &child{t: t}
	var control, accounting, events *os.File
	c.control, c.controlCh, control = channelEnds(t)
	c.accounting, accounting = channel(t)
	_, events = channel(t)
	guest, link := socketpair(t, syscall.SOCK_DGRAM)
	conn, err := net.FileConn(guest)
	_ = guest.Close()
	if err != nil {
		t.Fatal(err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		t.Fatalf("the guest's end is a %T", conn)
	}
	c.guest = uc
	t.Cleanup(func() { _ = c.guest.Close() })
	c.stderr, c.done = spawn(t, []*os.File{control, link, accounting, events}, env...)
	return c
}

// wait returns the child's exit status and its standard error.
func (c *child) wait() (int, string) {
	c.t.Helper()
	select {
	case err := <-c.done:
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode(), c.stderr.String()
		}
		if err != nil {
			c.t.Fatal(err)
		}
		return 0, c.stderr.String()
	case <-time.After(30 * time.Second):
		c.t.Fatalf("wb-netd didn't exit\n%s", c.stderr.String())
	}
	return 0, ""
}

// withPayload sends m with the read end of a pipe that holds payload.
func withPayload(t *testing.T, p *nt.Peer, m proto.Message, payload proto.Message) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p.Send(m, r)
	_ = r.Close()
	go func() {
		_ = egressmsg.WritePayload(w, payload)
		_ = w.Close()
	}()
}

// testLink is the link of nt's frames.
func testLink() egressmsg.Link {
	return egressmsg.Link{
		GuestMAC: nt.GuestMAC, GatewayMAC: nt.GatewayMAC,
		Subnet: nt.Subnet, Gateway: nt.Gateway, Leased: nt.Leased,
	}
}

// start exchanges the version records and sends the start message and
// a policy that allows api.example.com on 443.
func (c *child) start() {
	c.t.Helper()
	c.control.Hello()
	withPayload(c.t, c.control, &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{
		NetdStart: egressmsg.NewNetdStart(testVM, testGen, testLink()),
	}}, &egressv1.NetdStartPayload{})
	c.accounting.Hello()
	p := &policy.Policy{Generation: 1, WildcardBudget: 10, Endpoints: []policy.Endpoint{
		{Source: policy.Source{Kind: policy.SourceGlobal}, RuleName: "api", Protocol: policy.ProtocolL4, Host: match.ExactHost(nt.Name("api.example.com")), Ports: []uint16{443}},
	}}
	withPayload(c.t, c.control, &egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: testVM, Sequence: 1},
	}}, policy.ToProto(p))
	m, err := c.control.Recv(10 * time.Second)
	if err != nil {
		c.t.Fatalf("no policy ack: %v\n%s", err, c.stderr.String())
	}
	ack, ref := egressmsg.DecodeNetdToHostd(m.Record, m.File != nil)
	if ref != nil || ack.GetPolicyAck().GetSequence() != 1 {
		c.t.Fatalf("policy ack %v, %v\n%s", ack, ref, c.stderr.String())
	}
}

// handoff gives the child a hand-off socket, and returns wb-proxyd's end.
func (c *child) handoff() *nt.Peer {
	c.t.Helper()
	proxyd, theirs := channel(c.t)
	c.control.Send(&egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_HandoffSocket{
		HandoffSocket: &egressv1.HandoffSocket{Vm: testVM, Generation: testGen},
	}}, theirs)
	_ = theirs.Close()
	proxyd.Hello()
	return proxyd
}

// send writes a frame as the guest.
func (c *child) send(frame []byte) {
	c.t.Helper()
	if _, err := c.guest.Write(frame); err != nil {
		c.t.Fatal(err)
	}
}

// receive returns the next frame to the guest that keep accepts.
func (c *child) receive(keep func([]byte) bool) []byte {
	c.t.Helper()
	buf := make([]byte, 1<<16)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := c.guest.SetReadDeadline(deadline); err != nil {
			c.t.Fatal(err)
		}
		n, err := c.guest.Read(buf)
		if err != nil {
			c.t.Fatalf("no frame for the guest: %v\n%s", err, c.stderr.String())
		}
		if keep(buf[:n]) {
			return buf[:n]
		}
	}
}

// udpPayload returns the payload of an IPv4 UDP frame from port src, or
// nil.
func udpPayload(frame []byte, src uint16) []byte {
	if len(frame) < header.EthernetMinimumSize+header.IPv4MinimumSize ||
		header.Ethernet(frame).Type() != header.IPv4ProtocolNumber {
		return nil
	}
	ip := header.IPv4(frame[header.EthernetMinimumSize:])
	if ip.TransportProtocol() != header.UDPProtocolNumber {
		return nil
	}
	u := header.UDP(ip.Payload())
	if u.SourcePort() != src {
		return nil
	}
	return u.Payload()
}

// TestChildServes runs a confined wb-netd through its life
// (C11-host-daemons, "`wb-netd`"): a flood of frames before the start
// is drained, DHCP leases the start message's address, an allowed name
// gets a synthetic address only after its accounting record and its
// name report, and wb-netd exits 0 when wb-hostd closes the control
// channel.
func TestChildServes(t *testing.T) {
	c := startChild(t)
	flood(t, c.guest, 300)
	c.start()
	proxyd := c.handoff()

	zero := netip.IPv4Unspecified()
	broadcast := netip.MustParseAddr("255.255.255.255")
	c.send(nt.Ethernet(nt.Broadcast, nt.GuestMAC, uint16(header.IPv4ProtocolNumber),
		nt.IPv4(zero, broadcast, uint8(header.UDPProtocolNumber),
			nt.UDP(68, 67, nt.DHCP(0x1234, netip.Addr{}, nt.GuestMAC, nt.DHCPOpt(53, 1))))))
	offer := udpPayload(c.receive(func(f []byte) bool { return udpPayload(f, 67) != nil }), 67)
	if got := nt.DHCPAddr(offer, nt.DHCPOffYIAddr); got != nt.Leased {
		t.Fatalf("DHCP offered %s, want %s", got, nt.Leased)
	}

	c.send(dnsQuery(t, "api.example.com."))
	answer := udpPayload(c.receive(func(f []byte) bool { return udpPayload(f, 53) != nil }), 53)
	var m dnsmessage.Message
	if err := m.Unpack(answer); err != nil || len(m.Answers) != 1 {
		t.Fatalf("answer %+v, %v\n%s", m, err, c.stderr.String())
	}
	a, ok := m.Answers[0].Body.(*dnsmessage.AResource)
	if !ok || !netd.DefaultSynthetic.Contains(netip.AddrFrom4(a.A)) {
		t.Fatalf("answer %+v", m.Answers[0])
	}
	// The accounting record and the name report were written before the
	// answer, so each waits in its channel now.
	if _, err := c.accounting.Recv(100 * time.Millisecond); err != nil {
		t.Fatalf("no accounting record before the answer: %v", err)
	}
	if _, err := proxyd.Recv(100 * time.Millisecond); err != nil {
		t.Fatalf("no name report before the answer: %v", err)
	}

	_ = c.controlCh.Close()
	code, stderr := c.wait()
	if code != 0 || !strings.Contains(stderr, `"rule":"netd-stopped"`) ||
		!strings.Contains(stderr, `"rule":"link-not-started"`) {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
}

// flood sends n frames as the guest before the start, each retried
// while the packet socket is full. Without the drain the socket stays
// full and the flood fails.
func flood(t *testing.T, guest *net.UnixConn, n int) {
	t.Helper()
	frame := nt.Ethernet(nt.GatewayMAC, nt.GuestMAC, 0x86dd, make([]byte, 1000))
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; i < n; {
		_, err := guest.Write(frame)
		switch {
		case err == nil:
			i++
		case errors.Is(err, syscall.ENOBUFS) && time.Now().Before(deadline):
			time.Sleep(time.Millisecond)
		default:
			t.Fatalf("frame %d of the flood: %v", i, err)
		}
	}
}

// dnsQuery is an A query for host from the guest's port 5353 to the
// gateway, as a frame.
func dnsQuery(t *testing.T, host string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 42, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(host), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return nt.FromGuest(uint16(header.IPv4ProtocolNumber),
		nt.IPv4(nt.Leased, nt.Gateway, uint8(header.UDPProtocolNumber), nt.UDP(5353, 53, msg)))
}

// TestChildRefused checks the start interface's refusals: arguments,
// and a descriptor that is missing or of the wrong kind, each with its
// rule and exit status 1. A descriptor refusal comes after the
// confinement, which then denies a read outside the profile.
func TestChildRefused(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		files func(t *testing.T) []*os.File
		env   []string
		rule  string
		probe string
	}{
		"arguments": {
			files: func(*testing.T) []*os.File { return nil },
			env:   []string{childArgs + "=--debug", childOutside + "=" + outside},
			rule:  ruleArguments, probe: outsideAllowed,
		},
		"no descriptors": {
			files: func(*testing.T) []*os.File { return nil },
			env:   []string{childOutside + "=" + outside},
			rule:  ruleDescriptorInvalid, probe: outsideDenied,
		},
		"stream transport": {
			files: func(t *testing.T) []*os.File {
				_, control := channel(t)
				_, accounting := channel(t)
				_, events := channel(t)
				a, b := socketpair(t, syscall.SOCK_STREAM)
				t.Cleanup(func() { _ = a.Close() })
				return []*os.File{control, b, accounting, events}
			},
			env:  []string{childOutside + "=" + outside},
			rule: ruleDescriptorInvalid, probe: outsideDenied,
		},
	} {
		t.Run(name, func(t *testing.T) {
			stderr, done := spawn(t, c.files(t), c.env...)
			ch := &child{t: t, stderr: stderr, done: done}
			code, out := ch.wait()
			if code != 1 || !strings.Contains(out, `"rule":"`+c.rule+`"`) || !strings.Contains(out, c.probe) {
				t.Fatalf("exit %d, want 1 with %s and %q\n%s", code, c.rule, c.probe, out)
			}
		})
	}
}
