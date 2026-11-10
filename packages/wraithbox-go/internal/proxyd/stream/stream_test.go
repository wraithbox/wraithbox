package stream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse/tlstest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/upstream"
)

// TestC02SNIBinding sends ClientHellos to an inspected TLS port: a
// server name that differs from the stream's, none, and one in upper
// case (S07-07-hosts-and-modes, "Name binding").
func TestC02SNIBinding(t *testing.T) {
	tests := []struct {
		name  string
		hello tlstest.Hello
		check egressmsg.Check
	}{
		{"differs", tlstest.Hello{ServerName: "other.example.com"}, "server-name-differs"},
		{"none", tlstest.Hello{}, "no-server-name"},
		{"IP literal", tlstest.Hello{ServerName: "192.0.2.1"}, "server-name-form"},
		{"trailing dot", tlstest.Hello{ServerName: inspectHost + "."}, "server-name-form"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := newHarness(t, "")
			x.start(testPolicy(t), inspectHost, 443)
			x.send(tt.hello.Records())
			x.wantRefused("reset", RuleSNIMismatch, tt.check, "tls")
			x.wantNoDial()
		})
	}
	t.Run("upper case", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		hello := tlstest.Hello{ServerName: "API.Example.com"}.Records()
		x.send(hello)
		s := x.wantInspected()
		if s.Mode != ModeInspect || s.Protocol != policy.StreamTLS || s.Hello == nil {
			t.Errorf("inspected %+v", s)
		}
		got := make([]byte, len(hello))
		if _, err := io.ReadFull(s.Conn, got); err != nil || !bytes.Equal(got, hello) {
			t.Errorf("replayed %x, %v; want the ClientHello", got, err)
		}
		x.wantAllowed(RuleInspectAllowed, "mode=inspect")
		x.wantNoDial()
	})
}

// TestC02FirstBytesMismatch sends other protocols to a TLS port and to
// port 80, and sees each reset with stream-protocol-mismatch and what it
// looked like, with nothing dialed (S07-15-non-http-streams, "First
// bytes").
func TestC02FirstBytesMismatch(t *testing.T) {
	tests := []struct {
		name  string
		port  uint16
		bytes []byte
		check egressmsg.Check
		looks string
	}{
		{"SSH to 443", 443, []byte("SSH-2.0-OpenSSH_9.6\r\n"), tlsparse.CheckRecordVersion, "ssh"},
		{"PostgreSQL startup to 443", 443, []byte{0, 0, 0, 8, 0, 3, 0, 0}, tlsparse.CheckRecordVersion, "unknown"},
		{"SSLRequest to 443", 443, []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}, tlsparse.CheckRecordVersion, "unknown"},
		{"h2c preface to 443", 443, []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), tlsparse.CheckRecordVersion, "http2"},
		{"HTTP to 443", 443, []byte("GET / HTTP/1.1\r\n"), tlsparse.CheckRecordVersion, "http"},
		{"h2c preface to 80", 80, []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), CheckRequestLine, "http2"},
		{"TLS to 80", 80, tlstest.Hello{ServerName: inspectHost}.Records(), CheckRequestLine, "tls"},
		{"SSH to 80", 80, []byte("SSH-2.0-OpenSSH_9.6\r\n"), CheckRequestLine, "ssh"},
		{"HTTP/0.9 to 80", 80, []byte("GET / HTTP/0.9\r\n"), CheckRequestLine, "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := newHarness(t, "")
			x.start(testPolicy(t), inspectHost, tt.port)
			x.send(tt.bytes)
			x.wantRefused("reset", RuleProtocolMismatch, tt.check, tt.looks)
			x.wantNoDial()
		})
	}
}

// TestC02PlainHTTP sends HTTP/1.1 and HTTP/1.0 request lines to port 80,
// a byte at a time, and sees each go to the Inspector.
func TestC02PlainHTTP(t *testing.T) {
	for _, line := range []string{"GET / HTTP/1.1\r\nHost: api.example.com\r\n\r\n", "HEAD /x HTTP/1.0\r\n\r\n"} {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 80)
		go func() {
			for i := range len(line) {
				if _, err := x.guest.Write([]byte{line[i]}); err != nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		s := x.wantInspected()
		if s.Protocol != policy.StreamPlainHTTP || s.Hello != nil {
			t.Errorf("inspected %+v", s)
		}
		got := make([]byte, len(line))
		if _, err := io.ReadFull(s.Conn, got); err != nil || string(got) != line {
			t.Errorf("replayed %q, %v", got, err)
		}
	}
}

// TestC02NoProtocol opens a stream to a port no entry names, and sees
// it reset.
func TestC02NoProtocol(t *testing.T) {
	x := newHarness(t, "")
	x.start(testPolicy(t), inspectHost, 8443)
	x.wantRefused("reset", RuleProtocolMismatch, CheckProtocol, "")
	x.wantNoDial()
}

// TestC02FirstBytesTimeout sends nothing, and then one byte of a
// ClientHello a second scaled down, and sees each reset with
// stream-first-bytes-timeout at the deadline from the hand-off.
func TestC02FirstBytesTimeout(t *testing.T) {
	t.Run("nothing", func(t *testing.T) {
		x := newHarness(t, "")
		x.h.firstBytesTimeout = 200 * time.Millisecond
		x.start(testPolicy(t), inspectHost, 443)
		x.wantRefused("reset", RuleFirstBytesTimeout, "deadline", "unknown")
		x.wantNoDial()
	})
	t.Run("one byte at a time", func(t *testing.T) {
		x := newHarness(t, "")
		x.h.firstBytesTimeout = 300 * time.Millisecond
		x.start(testPolicy(t), inspectHost, 443)
		hello := tlstest.Hello{ServerName: inspectHost}.Records()
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			for _, b := range hello {
				select {
				case <-stop:
					return
				case <-time.After(30 * time.Millisecond):
				}
				if _, err := x.guest.Write([]byte{b}); err != nil {
					return
				}
			}
		}()
		x.wantRefused("reset", RuleFirstBytesTimeout, "deadline", "tls")
		x.wantNoDial()
	})
}

// TestC02ClientHelloRecords sends a ClientHello split over three records,
// which is taken, and ClientHello flights at the cap and one byte past
// it (S07-15-non-http-streams, "First bytes"; SEC13-bounded-resources).
func TestC02ClientHelloRecords(t *testing.T) {
	t.Run("three records", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(tlstest.Split(tlstest.TypeHandshake, tlstest.Hello{ServerName: inspectHost}.Message(), 7, 30))
		x.wantInspected()
	})
	t.Run("at the cap", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(flightOf(FirstBytesCap))
		x.wantInspected()
	})
	t.Run("one byte past the cap", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(flightOf(FirstBytesCap + 1))
		x.wantRefused("reset", RuleProtocolMismatch, tlsparse.CheckCap, "tls")
		x.wantNoDial()
	})
}

// flightOf returns a ClientHello to inspectHost in two records whose
// length on the wire is size.
func flightOf(size int) []byte {
	base := len(tlstest.Hello{ServerName: inspectHost, Extensions: []tlstest.Extension{{Type: 21}}}.Message())
	pad := size - 2*tlsparse.HeaderLen - base
	msg := tlstest.Hello{ServerName: inspectHost, Extensions: []tlstest.Extension{{Type: 21, Data: make([]byte, pad)}}}.Message()
	return tlstest.Split(tlstest.TypeHandshake, msg, len(msg)/2)
}

// TestC02ALPN sends ALPN lists to an inspected host: ftp and h2 alone
// are reset, and a list with http/1.1 and no list go to the Inspector
// (S07-15-non-http-streams, "Inside TLS on an inspected host").
func TestC02ALPN(t *testing.T) {
	for _, alpn := range [][]string{{"ftp"}, {"h2"}} {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(tlstest.Hello{ServerName: inspectHost, ALPN: alpn}.Records())
		x.wantRefused("reset", RuleProtocolMismatch, CheckALPN, "tls")
	}
	for _, alpn := range [][]string{{"h2", "http/1.1"}, {"ftp", "http/1.1"}, nil} {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(tlstest.Hello{ServerName: inspectHost, ALPN: alpn}.Records())
		x.wantInspected()
	}
}

// TestC02AuditMode sees a host in audit go to the Inspector in audit
// mode, after the same checks: a mismatch is still reset.
func TestC02AuditMode(t *testing.T) {
	p := testPolicy(t)
	p.Endpoints[0].Enforcement = policy.EnforcementAudit
	x := newHarness(t, "")
	x.start(p, inspectHost, 443)
	x.send(tlstest.Hello{ServerName: inspectHost}.Records())
	if s := x.wantInspected(); s.Mode != ModeAudit {
		t.Errorf("mode %v, want audit", s.Mode)
	}
	x = newHarness(t, "")
	x.start(p, inspectHost, 443)
	x.send([]byte("SSH-2.0-x\r\n"))
	x.wantRefused("reset", RuleProtocolMismatch, tlsparse.CheckRecordVersion, "ssh")
}

// TestC02UpstreamRefused opens a pass and a raw TCP stream to a name
// that resolves to 127.0.0.1, through the upstream package's dialer, and
// sees each reset with upstream-address-refused.
func TestC02UpstreamRefused(t *testing.T) {
	for _, tt := range []struct {
		host string
		port uint16
	}{{passHost, 443}, {rawHost, rawPort}} {
		t.Run(tt.host, func(t *testing.T) {
			x := newHarness(t, "")
			x.dialer.inner = upstream.New(slog.New(slog.DiscardHandler))
			x.start(testPolicy(t), tt.host, tt.port)
			if tt.port == 443 {
				x.send(tlstest.Hello{ServerName: passHost}.Records())
			}
			x.wantRefused("reset", string(upstream.RuleAddressRefused), "upstream", "")
		})
	}
}

// TestC02RawRelay relays a server-first protocol: the server speaks
// first, and the guest sends nothing before it reads (S07-15-non-http-
// streams, "Raw TCP entries").
func TestC02RawRelay(t *testing.T) {
	srv := newServer(t, func(s *server, c net.Conn) {
		_, _ = c.Write([]byte("220 ready\r\n"))
		s.read(c, len("QUIT\r\n"))
		_, _ = c.Write([]byte("221 bye\r\n"))
	})
	x := newHarness(t, srv.addr())
	x.start(testPolicy(t), rawHost, rawPort)
	_ = x.guest.SetDeadline(time.Now().Add(waitLong))
	banner := make([]byte, len("220 ready\r\n"))
	if _, err := io.ReadFull(x.guest, banner); err != nil {
		t.Fatalf("banner: %v", err)
	}
	x.send([]byte("QUIT\r\n"))
	rest, err := io.ReadAll(x.guest)
	if err != nil || string(rest) != "221 bye\r\n" {
		t.Errorf("rest = %q, %v", rest, err)
	}
	x.closeWrite()
	if how := x.ended(); how != "close" {
		t.Errorf("ended with %q", how)
	}
	<-x.done
	x.wantAllowed(RuleRawAllowed, "source=\"project demo\"", "database", "end=closed")
	if got := srv.received(t); string(got) != "QUIT\r\n" {
		t.Errorf("server got %q", got)
	}
}

// TestC02PolicyRecomputed removes the raw entry of an open relay, and
// takes the pass host of an open pass relay out of the pass list, and
// sees each relay end both ways (S07-01-per-vm-policy, "Open streams").
func TestC02PolicyRecomputed(t *testing.T) {
	t.Run("raw entry removed", func(t *testing.T) {
		srv := newServer(t, func(s *server, c net.Conn) {
			_, _ = c.Write([]byte("hi"))
			s.readRest(c)
		})
		x := newHarness(t, srv.addr())
		x.start(testPolicy(t), rawHost, rawPort)
		greet := make([]byte, 2)
		if _, err := io.ReadFull(x.guest, greet); err != nil {
			t.Fatal(err)
		}
		p := testPolicy(t)
		p.RawTCP = nil
		x.ctl.setPolicy(p)
		if how := x.ended(); how != handoff.RulePolicyRecomputed {
			t.Errorf("ended with %q", how)
		}
		srv.received(t)
		<-x.done
		x.wantRecords(1, 0)
		if !strings.Contains(x.logs.String(), "end="+endedByHandoff) {
			t.Errorf("the relay's end isn't the hand-off's:\n%s", x.logs.String())
		}
	})
	t.Run("host leaves the pass list", func(t *testing.T) {
		hello := tlstest.Hello{ServerName: passHost}.Records()
		srv := newServer(t, func(s *server, c net.Conn) {
			s.read(c, len(hello))
			s.readRest(c)
		})
		x := newHarness(t, srv.addr())
		x.start(testPolicy(t), passHost, 443)
		x.send(hello)
		waitFor(t, func() bool { return strings.Contains(x.logs.String(), RulePassAllowed) })
		p := testPolicy(t)
		p.PassHosts = nil
		x.ctl.setPolicy(p)
		if how := x.ended(); how != handoff.RulePolicyRecomputed {
			t.Errorf("ended with %q", how)
		}
		srv.received(t)
		<-x.done
		x.wantRecords(1, 0)
		if !strings.Contains(x.logs.String(), "end="+endedByHandoff) {
			t.Errorf("the relay's end isn't the hand-off's:\n%s", x.logs.String())
		}
	})
	t.Run("gone at the hand-off", func(t *testing.T) {
		p := testPolicy(t)
		p.RawTCP = nil
		x := newHarness(t, "")
		x.start(p, rawHost, rawPort)
		x.wantRefused("reset", RuleProtocolMismatch, CheckProtocol, "")
	})
}

// TestC02EndedByHandoff ends streams from the hand-off's side, as a
// recompute, an address conflict or the socket's end does, while the
// handler reads the first bytes, during its dial, and as the dial
// opens. The handler logs no refusal and no allowed stream, as the
// hand-off logged the end, and sends nothing upstream.
func TestC02EndedByHandoff(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	t.Run("first bytes", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), inspectHost, 443)
		x.send(hello[:3])
		x.ctl.handoffReset()
		x.wantHandoffEnd()
		x.wantNoDial()
	})
	t.Run("during the dial", func(t *testing.T) {
		x := newHarness(t, "")
		dialing := make(chan struct{})
		x.dialer.block = true
		x.dialer.onDial = func() { close(dialing) }
		x.start(testPolicy(t), passHost, 443)
		x.send(hello)
		<-dialing
		x.ctl.handoffReset()
		x.wantHandoffEnd()
	})
	for _, tt := range []struct {
		name string
		host string
		port uint16
		send []byte
	}{
		{"pass dial opens", passHost, 443, hello},
		{"raw dial opens", rawHost, rawPort, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, func(s *server, c net.Conn) { s.readRest(c) })
			x := newHarness(t, srv.addr())
			x.dialer.afterDial = func() { x.ctl.handoffReset() }
			x.start(testPolicy(t), tt.host, tt.port)
			if tt.send != nil {
				x.send(tt.send)
			}
			x.wantHandoffEnd()
			if got := srv.received(t); len(got) != 0 {
				t.Errorf("upstream got %x, want nothing", got)
			}
		})
	}
}

// TestC02ResetBeforeHelloSend ends a pass stream from the hand-off's
// side after the dial opened and its end check passed, and before the
// ClientHello's send (I598). The send checks the end again in one step
// with the write, so the upstream server gets nothing, and the handler
// logs neither an allowed stream nor its end.
func TestC02ResetBeforeHelloSend(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	srv := newServer(t, func(s *server, c net.Conn) { s.readRest(c) })
	x := newHarness(t, srv.addr())
	x.start(testPolicy(t), passHost, 443)
	x.ctl.beforeSend = x.ctl.handoffReset
	x.send(hello)
	x.wantHandoffEnd()
	if got := srv.received(t); len(got) != 0 {
		t.Errorf("upstream got %x, want nothing", got)
	}
	if n := x.dialer.dials.Load(); n != 1 {
		t.Errorf("%d upstream dials, want 1", n)
	}
}

// stallDialer opens one end of a net.Pipe whose other end nobody
// reads, so a write to it blocks until its deadline. writing is closed
// as the first write starts.
type stallDialer struct {
	writing chan struct{}
	once    sync.Once
}

func (d *stallDialer) Dial(context.Context, string, uint16) (net.Conn, error) {
	a, _ := net.Pipe()
	return &signalConn{Conn: a, d: d}, nil
}

// signalConn closes its dialer's writing channel as a write starts.
type signalConn struct {
	net.Conn
	d *stallDialer
}

func (c *signalConn) Write(b []byte) (int, error) {
	c.d.once.Do(func() { close(c.d.writing) })
	return c.Conn.Write(b)
}

// TestC02FirstSendTimeout stalls a pass relay's ClientHello write
// upstream. The write ends at firstSendTimeout: the relay resets the
// stream and logs its end as first-send-timeout. A hand-off reset that
// comes during the stalled write waits for it, and no longer
// (SEC13-bounded-resources, I598).
func TestC02FirstSendTimeout(t *testing.T) {
	const timeout = 200 * time.Millisecond
	hello := tlstest.Hello{ServerName: passHost}.Records()
	start := func(t *testing.T) (*harness, *stallDialer) {
		t.Helper()
		x := newHarness(t, "")
		x.h.firstSendTimeout = timeout
		d := &stallDialer{writing: make(chan struct{})}
		x.dialer.inner = d
		x.start(testPolicy(t), passHost, 443)
		x.send(hello)
		return x, d
	}
	t.Run("deadline", func(t *testing.T) {
		x, _ := start(t)
		if how := x.ended(); how != "reset" {
			t.Errorf("stream ended with %q, want a reset", how)
		}
		<-x.done
		x.wantRecords(1, 0)
		if !strings.Contains(x.logs.String(), "end="+endFirstSendTimeout) {
			t.Errorf("the relay's end isn't %s:\n%s", endFirstSendTimeout, x.logs.String())
		}
	})
	t.Run("hand-off reset waits", func(t *testing.T) {
		x, d := start(t)
		<-d.writing
		began := time.Now()
		reset := make(chan struct{})
		go func() { x.ctl.handoffReset(); close(reset) }()
		select {
		case <-reset:
		case <-time.After(waitLong):
			t.Fatalf("the hand-off reset still waits after %v; log:\n%s", waitLong, x.logs.String())
		}
		if waited := time.Since(began); waited > timeout+2*time.Second {
			t.Errorf("the hand-off reset waited %v, want about %v", waited, timeout)
		}
		x.ended()
		<-x.done
	})
}

// wantHandoffEnd checks the hand-off ended the stream, and the handler
// returned with no log record of its own.
func (x *harness) wantHandoffEnd() {
	x.t.Helper()
	if how := x.ended(); how != handoffEnd {
		x.t.Errorf("stream ended with %q, want the hand-off's end", how)
	}
	select {
	case <-x.done:
	case <-time.After(waitLong):
		x.t.Fatal("the handler didn't return")
	}
	x.wantRecords(0, 0)
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLong)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNew(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New without a Dialer and an Inspector succeeded")
	}
}
