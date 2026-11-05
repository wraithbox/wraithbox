package handoff_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

const generation = 7

var (
	quiet = slog.New(slog.DiscardHandler)
	addr  = netip.MustParseAddr("198.18.0.9")
	flow  = netd.NamedFlow{
		Guest: netip.MustParseAddrPort("10.0.2.15:50000"), Dest: netip.AddrPortFrom(addr, 443),
		Name: nt.Name("example.com"),
	}
)

// setup returns a Sender with a hand-off socket of generation, and the
// fake wb-proxyd at its far end, which hasn't read or sent anything.
func setup(t *testing.T) (*handoff.Sender, *nt.Peer) {
	t.Helper()
	ours, theirs := nt.ChanPair(t)
	s := handoff.New(handoff.Config{Own: nt.Own, Log: quiet})
	s.Replace(ours, generation)
	t.Cleanup(s.Close)
	return s, nt.NewPeer(t, theirs)
}

// handOff runs HandOffStream and closes the channel it returns when
// HandOffStream returns.
func handOff(s *handoff.Sender, conn netd.StreamConn) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.HandOffStream(conn, flow)
	}()
	return done
}

// takeStream reads the Stream message and returns its id and wb-proxyd's
// end of the stream.
func takeStream(t *testing.T, proxyd *nt.Peer) (uint64, net.Conn) {
	t.Helper()
	m := proxyd.Next(time.Second)
	msg, ref := egressmsg.DecodeNetdMessage(m.Record, m.File != nil)
	if ref != nil {
		_ = m.Close()
		t.Fatal(ref)
	}
	if msg.Kind != egressmsg.KindStream || msg.Generation != generation || msg.Addr != addr || msg.Port != 443 {
		_ = m.Close()
		t.Fatalf("got %+v, want a stream of generation %d to %v:443", msg, generation, addr)
	}
	conn, err := net.FileConn(m.File)
	_ = m.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return msg.StreamID, conn
}

func readExactly(t *testing.T, c net.Conn, want string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != want {
		t.Fatalf("read %q, %v, want %q", buf, err, want)
	}
}

func write(t *testing.T, c net.Conn, s string) {
	t.Helper()
	if _, err := c.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

// guestEnd reads the guest's connection to its end and returns the
// error: io.EOF for a FIN, ECONNRESET for an RST.
func guestEnd(t *testing.T, guest net.Conn) error {
	t.Helper()
	_ = guest.SetReadDeadline(time.Now().Add(2 * time.Second))
	var buf [64]byte
	n, err := guest.Read(buf[:])
	if n > 0 {
		t.Fatalf("the guest read %q, want its end", buf[:n])
	}
	return err
}

func wait(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandOffStream didn't return")
	}
}

func resetStream(proxyd *nt.Peer, id uint64) {
	proxyd.Send(&egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{
		StreamReset: &egressv1.StreamReset{StreamId: id},
	}}, nil)
}

// exchange sends bytes each way on a stream, with the guest's FIN in
// between: wb-proxyd reads the guest's bytes and then end of file, and
// still writes to the guest.
func exchange(t *testing.T, guest *net.TCPConn, conn net.Conn) {
	t.Helper()
	write(t, guest, "ping")
	readExactly(t, conn, "ping")
	if err := guest.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := conn.Read(one[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("wb-proxyd read %v after the guest's FIN, want EOF", err)
	}
	write(t, conn, "pong")
	readExactly(t, guest, "pong")
}

// TestStreamEnds checks the bytes and the end of a stream each way: a
// guest FIN gives wb-proxyd end of file, wb-proxyd's close gives the
// guest a FIN, and a StreamReset sent before the close gives the guest
// an RST, logged with stream-reset (S07-05-stream-hand-off, "Resets").
func TestStreamEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset bool
		want  error
	}{
		{"clean close gives a FIN", false, io.EOF},
		{"StreamReset gives an RST", true, syscall.ECONNRESET},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, proxyd := setup(t)
			proxyd.Hello()
			ours, guest := nt.TCPPair(t)
			done := handOff(s, ours)
			id, conn := takeStream(t, proxyd)

			exchange(t, guest, conn)
			if tc.reset {
				resetStream(proxyd, id)
			}
			_ = conn.Close()
			if err := guestEnd(t, guest); !errors.Is(err, tc.want) {
				t.Fatalf("the guest's end is %v, want %v", err, tc.want)
			}
			wait(t, done)
			if got := s.Recorder().Count(netd.RuleStreamReset) == 1; got != tc.reset {
				t.Errorf("stream-reset counted: %v, want %v", got, tc.reset)
			}
		})
	}
}

// TestStreamSendFails checks that a stream whose Stream message can't
// go out within SendTimeout resets the guest, logged with
// handoff-send-failed, and is never sent later.
func TestStreamSendFails(t *testing.T) {
	s, proxyd := setup(t)
	proxyd.ReadVersion() // and never sends its own, so every send waits
	ours, guest := nt.TCPPair(t)
	start := time.Now()
	done := handOff(s, ours)
	if err := guestEnd(t, guest); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("the guest's end is %v, want an RST", err)
	}
	wait(t, done)
	if d := time.Since(start); d < handoff.SendTimeout {
		t.Errorf("reset after %v, before SendTimeout", d)
	}
	if n := s.Recorder().Count(netd.RuleHandoffSendFailed); n != 1 {
		t.Errorf("handoff-send-failed counted %d, want 1", n)
	}
	proxyd.SendVersion()
	if err := report(s); err != nil {
		t.Fatal(err)
	}
	m := proxyd.Next(time.Second)
	defer func() { _ = m.Close() }()
	if msg, ref := egressmsg.DecodeNetdMessage(m.Record, m.File != nil); ref != nil || msg.Kind != egressmsg.KindNameReport {
		t.Fatalf("got %+v, %v after the failed stream, want only the report", msg, ref)
	}
}

// TestSocketEnds checks that every stream of a hand-off socket is reset
// when the socket ends: wb-proxyd closes it, sends a message that fails
// its check, or a new socket replaces it. A send after it fails.
func TestSocketEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(t *testing.T, s *handoff.Sender, proxyd *nt.Peer)
		rule netd.Rule
	}{
		{"closed", func(_ *testing.T, _ *handoff.Sender, p *nt.Peer) { _ = p.Ch.Close() }, netd.RuleHandoffSocketEnded},
		{"malformed message", func(t *testing.T, _ *handoff.Sender, p *nt.Peer) {
			if err := p.Ch.Send([]byte{0xff}, nil); err != nil {
				t.Fatal(err)
			}
		}, netd.RuleProxydMessageRefused},
		{"message with a descriptor", func(t *testing.T, _ *handoff.Sender, p *nt.Peer) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()
			p.Send(&egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{
				StreamReset: &egressv1.StreamReset{StreamId: 1},
			}}, r)
		}, netd.RuleProxydMessageRefused},
		{"replaced", func(t *testing.T, s *handoff.Sender, _ *nt.Peer) {
			ours, _ := nt.ChanPair(t)
			s.Replace(ours, generation+1)
		}, netd.RuleHandoffSocketEnded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, proxyd := setup(t)
			proxyd.Hello()
			ours, guest := nt.TCPPair(t)
			done := handOff(s, ours)
			takeStream(t, proxyd)
			tc.end(t, s, proxyd)
			if err := guestEnd(t, guest); !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("the guest's end is %v, want an RST", err)
			}
			wait(t, done)
			if s.Recorder().Count(tc.rule) == 0 {
				t.Errorf("%s not counted", tc.rule)
			}
			if err := report(s); err == nil {
				t.Error("a report after the socket ended was sent")
			}
		})
	}
}

// TestStreamResetUnknown checks that a StreamReset for a stream wb-netd
// doesn't hold is logged and ignored, and the socket goes on.
func TestStreamResetUnknown(t *testing.T) {
	s, proxyd := setup(t)
	proxyd.Hello()
	resetStream(proxyd, 99)
	if n := nt.WaitCount(s.Recorder(), netd.RuleStreamResetUnknown); n != 1 {
		t.Fatalf("stream-reset-unknown counted %d, want 1", n)
	}
	if err := report(s); err != nil {
		t.Fatalf("a report after the unknown reset: %v", err)
	}
}

// TestVersionRecordRefused checks that a hand-off socket whose peer
// sends another build's version record, or one with a descriptor, ends
// at once, and every send on it fails.
func TestVersionRecordRefused(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, p *nt.Peer){
		"another version": func(t *testing.T, p *nt.Peer) {
			if err := p.Ch.Send(hostversion.Record("9.9.9-dev+other"), nil); err != nil {
				t.Fatal(err)
			}
		},
		"with a descriptor": func(t *testing.T, p *nt.Peer) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()
			if err := p.Ch.Send(hostversion.Record(nt.Own), r); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, proxyd := setup(t)
			proxyd.ReadVersion()
			send(t, proxyd)
			err := report(s)
			if !errors.Is(err, handoff.ErrNoSocket) {
				t.Fatalf("report: %v, want ErrNoSocket", err)
			}
			if n := s.Recorder().Count(netd.RuleProxydMessageRefused); n != 1 {
				t.Errorf("proxyd-message-refused counted %d, want 1", n)
			}
		})
	}
}

// report sends a report of flow's name within SendTimeout, as the pool
// does.
func report(s *handoff.Sender) error {
	ctx, cancel := context.WithTimeout(context.Background(), handoff.SendTimeout)
	defer cancel()
	return s.Report(ctx, flow.Name, addr)
}

// release sends a release of addr within SendTimeout.
func release(s *handoff.Sender) error {
	ctx, cancel := context.WithTimeout(context.Background(), handoff.SendTimeout)
	defer cancel()
	return s.Release(ctx, addr)
}

// TestNoSocket checks that a Sender without a hand-off socket fails
// every send at once.
func TestNoSocket(t *testing.T) {
	s := handoff.New(handoff.Config{Own: nt.Own, Log: quiet})
	if err := report(s); !errors.Is(err, handoff.ErrNoSocket) {
		t.Errorf("report: %v, want ErrNoSocket", err)
	}
	if err := release(s); !errors.Is(err, handoff.ErrNoSocket) {
		t.Errorf("release: %v, want ErrNoSocket", err)
	}
}

// slowClose is a hand-off socket whose first Close waits for release,
// so a test acts while the socket is ending.
type slowClose struct {
	*fdchan.Channel
	entered, release chan struct{}
	once             sync.Once
}

func (c *slowClose) Close() error {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.Channel.Close()
}

// TestCloseOrder checks that wb-proxyd closing the hand-off socket and
// then a stream's socket gives the guest an RST, not a FIN, also when
// wb-netd reads the stream's end of file while the socket is ending
// (S07-05-stream-hand-off, "Resets").
func TestCloseOrder(t *testing.T) {
	ch, theirs := nt.ChanPair(t)
	ours := &slowClose{Channel: ch, entered: make(chan struct{}), release: make(chan struct{})}
	s := handoff.New(handoff.Config{Own: nt.Own, Log: quiet})
	s.Replace(ours, generation)
	t.Cleanup(s.Close)
	proxyd := nt.NewPeer(t, theirs)
	proxyd.Hello()
	conn, guest := nt.TCPPair(t)
	done := handOff(s, conn)
	_, stream := takeStream(t, proxyd)

	_ = proxyd.Ch.Close()
	select {
	case <-ours.entered: // the socket has ended, and its streams aren't reset yet
	case <-time.After(2 * time.Second):
		t.Fatal("the hand-off socket didn't end")
	}
	_ = stream.Close()
	err := guestEnd(t, guest)
	close(ours.release)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("the guest's end is %v, want an RST", err)
	}
	wait(t, done)
}

// TestNoVersionRecord checks that a hand-off socket whose wb-proxyd
// sends no version record within the version timeout ends, refused with
// peer-version-timeout, and every send on it fails (S04-architecture,
// "The version record").
func TestNoVersionRecord(t *testing.T) {
	ours, theirs := nt.ChanPair(t)
	s := handoff.New(handoff.Config{Own: nt.Own, Log: quiet, VersionTimeout: 50 * time.Millisecond})
	s.Replace(ours, generation)
	t.Cleanup(s.Close)
	nt.NewPeer(t, theirs).ReadVersion()
	if n := nt.WaitCount(s.Recorder(), netd.RuleProxydMessageRefused); n != 1 {
		t.Fatalf("proxyd-message-refused counted %d, want 1", n)
	}
	if err := report(s); !errors.Is(err, handoff.ErrNoSocket) {
		t.Fatalf("report: %v, want ErrNoSocket", err)
	}
}
