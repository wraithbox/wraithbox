package hostlink_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// fakeConn is a channel that returns its messages, then end of file,
// and keeps what is sent on it. It runs on every OS, unlike fdchan.
type fakeConn struct {
	mu     sync.Mutex
	msgs   []*fdchan.Message
	sent   [][]byte
	fds    int
	closed chan struct{}
	once   sync.Once
}

func newFakeConn(msgs []*fdchan.Message) *fakeConn {
	return &fakeConn{msgs: msgs, closed: make(chan struct{})}
}

func (c *fakeConn) Send(record []byte, fd syscall.Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, bytes.Clone(record))
	if fd != nil {
		c.fds++
	}
	return nil
}

func (c *fakeConn) Recv() (*fdchan.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return nil, io.EOF
	}
	m := c.msgs[0]
	c.msgs = c.msgs[1:]
	return m, nil
}

func (c *fakeConn) TryRecv() (*fdchan.Message, error) { return nil, fdchan.ErrNoMessage }

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func (c *fakeConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, m := range c.msgs {
			_ = m.Close()
		}
		c.msgs = nil
	})
	return nil
}

func (c *fakeConn) sends() ([][]byte, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent, c.fds
}

// countTargets counts what a control channel passes on.
type countTargets struct {
	mu       sync.Mutex
	replaced int
	applied  int
}

func (c *countTargets) Replace(conn handoff.Conn, _ uint64) {
	_ = conn.Close()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.replaced++
}

func (c *countTargets) ApplyPolicy(*policy.Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applied++
}

// frames splits fuzz bytes into messages. Each frame is a flags byte, a
// 2-byte record length and the record. With flag 1 a payload pipe
// follows: a 2-byte length and the bytes written to it.
func frames(t *testing.T, b []byte) []*fdchan.Message {
	t.Helper()
	var msgs []*fdchan.Message
	for len(b) >= 3 {
		flags, n := b[0], min(int(binary.BigEndian.Uint16(b[1:3])), len(b)-3)
		m := &fdchan.Message{Record: b[3 : 3+n]}
		b = b[3+n:]
		if flags&1 != 0 {
			var payload []byte
			if len(b) >= 2 {
				k := min(int(binary.BigEndian.Uint16(b[:2])), len(b)-2)
				payload, b = b[2:2+k], b[2+k:]
			}
			m.File = pipeWith(t, payload)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// pipeWith returns the read end of a pipe that holds b and then ends.
func pipeWith(t *testing.T, b []byte) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.Write(b)
		_ = w.Close()
	}()
	return r
}

// FuzzNetdControl feeds arbitrary records to wb-netd's control channel,
// accounting channel and hand-off socket. Whatever they hold, wb-netd
// sends no name report, release, stream or accounting record: on each
// channel only its version record, and on the control channel one
// PolicyAck per policy it applied.
func FuzzNetdControl(f *testing.F) {
	frame := func(m proto.Message, payload proto.Message) []byte {
		rec, _ := egressmsg.Marshal(m)
		flags := byte(0)
		var tail []byte
		if payload != nil {
			flags = 1
			p, _ := proto.Marshal(payload)
			tail = binary.BigEndian.AppendUint32(nil, uint32(len(p)))
			tail = append(tail, p...)
			tail = append(binary.BigEndian.AppendUint16(nil, uint16(len(tail))), tail...)
		}
		out := append([]byte{flags}, binary.BigEndian.AppendUint16(nil, uint16(len(rec)))...)
		return append(append(out, rec...), tail...)
	}
	version := append([]byte{0}, binary.BigEndian.AppendUint16(nil, uint16(len(hostversion.Record(nt.Own))))...)
	version = append(version, hostversion.Record(nt.Own)...)
	start := frame(netdStart(), &egressv1.NetdStartPayload{
		Entries: []*egressv1.MappingEntry{{Name: "a.example.com", Address: 0xc6120001}}, DebitCount: 1,
	})
	change := frame(policyChange(1), validPolicy())
	sock := frame(handoffSocket(vm, gen), &policyv1.EffectivePolicy{})
	reset := frame(&egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{
		StreamReset: &egressv1.StreamReset{StreamId: 1},
	}}, nil)
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	f.Add(cat(version, start, change, sock, change), cat(version, reset, reset))
	f.Add(cat(version, start, sock), cat(version, []byte{0, 0, 1, 0xff}))
	f.Add(cat(version, change), cat(reset))
	f.Fuzz(func(t *testing.T, control, proxyd []byte) {
		log := slog.New(slog.DiscardHandler)

		conn := newFakeConn(frames(t, control))
		tg := &countTargets{}
		c, err := hostlink.NewControl(hostlink.ControlConfig{Conn: conn, Own: nt.Own, Log: log, Handoff: tg, Policy: tg})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Start(); err == nil {
			_ = c.Serve(context.Background())
		}
		checkControlSends(t, conn, tg)

		hs := newFakeConn(frames(t, proxyd))
		s := handoff.New(handoff.Config{Own: nt.Own, Log: log})
		s.Replace(hs, 1)
		<-hs.closed
		onlyVersion(t, "hand-off socket", hs)

		ac := newFakeConn(frames(t, control))
		a, err := hostlink.NewAccounting(ac, nt.Own, log)
		if err != nil {
			t.Fatal(err)
		}
		<-ac.closed
		_ = a.Close()
		onlyVersion(t, "accounting channel", ac)
	})
}

// onlyVersion checks that the version record is all that was sent on c.
func onlyVersion(t *testing.T, what string, c *fakeConn) {
	t.Helper()
	sent, fds := c.sends()
	if len(sent) != 1 || !bytes.Equal(sent[0], hostversion.Record(nt.Own)) || fds != 0 {
		t.Fatalf("%s: sent %d records and %d descriptors beside the version record", what, len(sent), fds)
	}
}

// checkControlSends checks that the control channel sent its version
// record, then one PolicyAck per applied policy, and no hand-off socket
// was taken: a pipe is never a datagram socket.
func checkControlSends(t *testing.T, c *fakeConn, tg *countTargets) {
	t.Helper()
	sent, fds := c.sends()
	if len(sent) == 0 || !bytes.Equal(sent[0], hostversion.Record(nt.Own)) || fds != 0 {
		t.Fatalf("control: sent %d records and %d descriptors", len(sent), fds)
	}
	for _, rec := range sent[1:] {
		m, ref := egressmsg.DecodeNetdToHostd(rec, false)
		if ref != nil || m.GetPolicyAck() == nil {
			t.Fatalf("control: sent %x, not a PolicyAck", rec)
		}
	}
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.applied != len(sent)-1 || tg.replaced != 0 {
		t.Fatalf("control: %d policies applied, %d acks, %d sockets taken", tg.applied, len(sent)-1, tg.replaced)
	}
}
