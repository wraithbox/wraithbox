package netdtest

import (
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// Own is the build version both ends of a wire fake carry.
const Own = "1.2.3-dev+abc"

// ChanPair returns two connected fdchan channels for records of up to
// egressmsg.MaxRecord bytes. fdchan works on darwin only.
func ChanPair(t testing.TB) (a, b *fdchan.Channel) {
	t.Helper()
	fa, fb, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	a, err = fdchan.New(fa, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	b, err = fdchan.New(fb, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

// Peer is the far end of a channel wb-netd holds: a fake wb-proxyd on
// the hand-off socket, or a fake wb-hostd on the accounting or control
// channel.
type Peer struct {
	t  testing.TB
	Ch *fdchan.Channel
}

// NewPeer returns a Peer on ch.
func NewPeer(t testing.TB, ch *fdchan.Channel) *Peer { return &Peer{t: t, Ch: ch} }

// SendVersion sends the version record.
func (p *Peer) SendVersion() {
	p.t.Helper()
	if err := p.Ch.Send(hostversion.Record(Own), nil); err != nil {
		p.t.Fatal(err)
	}
}

// ReadVersion reads wb-netd's version record and checks it.
func (p *Peer) ReadVersion() {
	p.t.Helper()
	m := p.Next(time.Second)
	if _, err := hostversion.CheckRecord(nil, Own, m.Record); err != nil || m.File != nil {
		p.t.Fatalf("version record %q, descriptor %v: %v", m.Record, m.File, err)
	}
}

// Hello reads wb-netd's version record and sends its own.
func (p *Peer) Hello() {
	p.t.Helper()
	p.ReadVersion()
	p.SendVersion()
}

// Send marshals m and sends it, with fd when it isn't nil.
func (p *Peer) Send(m proto.Message, fd syscall.Conn) {
	p.t.Helper()
	rec, err := egressmsg.Marshal(m)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := p.Ch.Send(rec, fd); err != nil {
		p.t.Fatal(err)
	}
}

// Next returns the next message within d, or fails the test. The caller
// closes its descriptor.
func (p *Peer) Next(d time.Duration) *fdchan.Message {
	p.t.Helper()
	m, err := p.Recv(d)
	if err != nil {
		p.t.Fatalf("no message within %v: %v", d, err)
	}
	return m
}

// Recv returns the next message within d.
func (p *Peer) Recv(d time.Duration) (*fdchan.Message, error) {
	if err := p.Ch.SetReadDeadline(time.Now().Add(d)); err != nil {
		return nil, err
	}
	return p.Ch.Recv()
}

// Queued returns every message queued now, without waiting, up to end
// of file.
func (p *Peer) Queued() []*fdchan.Message {
	p.t.Helper()
	var ms []*fdchan.Message
	for {
		m, err := p.Ch.TryRecv()
		if errors.Is(err, fdchan.ErrNoMessage) || errors.Is(err, io.EOF) {
			return ms
		}
		if err != nil {
			p.t.Fatal(err)
		}
		ms = append(ms, m)
	}
}

// TCPConn is a netd.StreamConn over a loopback TCP connection, so a
// test sees the FIN and the RST the guest would.
type TCPConn struct{ *net.TCPConn }

// Reset closes the connection with an RST.
func (c TCPConn) Reset() {
	_ = c.SetLinger(0)
	_ = c.Close()
}

// TCPPair returns a loopback TCP connection: the end wb-netd holds, and
// the guest's.
func TCPPair(t testing.TB) (ours TCPConn, guest *net.TCPConn) {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	la, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T", ln.Addr())
	}
	guest, err = net.DialTCP("tcp", nil, la)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ln.AcceptTCP()
	if err != nil {
		_ = guest.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guest.Close(); _ = c.Close() })
	return TCPConn{c}, guest
}
