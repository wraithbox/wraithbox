package inspect

import (
	"bufio"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/ca"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
)

const request = "GET /x HTTP/1.1\r\nHost: example.com\r\n\r\n"

// TestC02InspectHandshake: a client that trusts the CA completes the
// handshake with TLS 1.2 and 1.3, with http/1.1 or no ALPN, gets
// HTTP/1.1, and its first request reaches the HTTP handler. The leaf is
// asked for the stream's VM, generation, name and port, with its policy.
func TestC02InspectHandshake(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, c := range []struct {
			alpn []string
			want string
		}{
			{[]string{"http/1.1"}, stream.ALPNHTTP1},
			{nil, ""},
			{[]string{"h2", "http/1.1"}, stream.ALPNHTTP1},
		} {
			handshakeServed(t, version, c.alpn, c.want)
		}
	}
}

// handshakeServed runs one client that trusts the CA through the
// handshake, with version and alpn, and checks that it negotiates want
// and that its first request reaches the HTTP handler.
func handshakeServed(t *testing.T, version uint16, alpn []string, want string) {
	t.Helper()
	e := newEnv(t)
	g := e.start(stream.ModeInspect, policy.StreamTLS)
	conf := e.clientConfig()
	conf.MaxVersion = version
	conf.NextProtos = alpn
	c := tls.Client(g.conn, conf)
	defer c.Close()
	if err := c.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("TLS %x, ALPN %v: %v", version, alpn, err)
	}
	if got := c.ConnectionState().NegotiatedProtocol; got != want {
		t.Errorf("ALPN %v negotiated %q, want %q", alpn, got, want)
	}
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	s := e.served()
	if !s.TLS || s.Inspected.Name.String() != testName {
		t.Errorf("served %+v, want a TLS stream for %s", s, testName)
	}
	line, err := bufio.NewReader(s.Conn).ReadString('\n')
	if err != nil || line != "GET /x HTTP/1.1\r\n" {
		t.Errorf("the HTTP handler read %q, %v, want the request line", line, err)
	}
	reqs := e.leaves.calls()
	if len(reqs) != 1 || reqs[0].VM != "work" || reqs[0].Generation != 1 || reqs[0].Name != testName || reqs[0].Port != 443 || reqs[0].Policy == nil {
		t.Errorf("leaf requests = %+v, want one for work/1 %s:443 with its policy", reqs, testName)
	}
	if n := e.dialer.dials.Load(); n != 0 {
		t.Errorf("%d upstream dials at the handshake, want none", n)
	}
}

// TestC02InspectNoSessionTickets: the guest's handshake issues no
// session ticket, with TLS 1.2 or 1.3, so no later stream resumes it
// (S07-07-hosts-and-modes, "inspect").
func TestC02InspectNoSessionTickets(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		e := newEnv(t)
		g := e.start(stream.ModeInspect, policy.StreamTLS)
		cache := tls.NewLRUClientSessionCache(4)
		conf := e.clientConfig()
		conf.MaxVersion = version
		conf.ClientSessionCache = cache
		c := tls.Client(g.conn, conf)
		if err := c.HandshakeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(c, request); err != nil {
			t.Fatal(err)
		}
		// A TLS 1.3 ticket comes after the handshake: one byte from the
		// server makes the client read past it.
		if _, err := io.WriteString(e.served().Conn, "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		if _, ok := cache.Get(testName); ok {
			t.Errorf("TLS %x: the client holds a session ticket", version)
		}
		_ = c.Close()
	}
}

// TestC02InspectPlainHTTP: a plain HTTP stream goes to the HTTP handler
// as it is, with no leaf.
func TestC02InspectPlainHTTP(t *testing.T) {
	e := newEnv(t)
	e.start(stream.ModeInspect, policy.StreamPlainHTTP)
	s := e.served()
	if s.TLS || len(e.leaves.calls()) != 0 {
		t.Errorf("plain HTTP: TLS %v, %d leaf requests, want neither", s.TLS, len(e.leaves.calls()))
	}
}

// TestC02InspectLeafRefused: a refusal from the leaf interface resets
// the stream with its rule.
func TestC02InspectLeafRefused(t *testing.T) {
	for _, mode := range modes {
		e := newEnv(t)
		e.leaves.ref = &ca.Refusal{Rule: ca.RuleNotInstalled, Reason: "no signing CA"}
		g := e.start(mode, policy.StreamTLS)
		e.wantReset(g, mode, ca.RuleNotInstalled, `reason="no signing CA"`)
	}
}

// TestC02InspectALPN: a client that offers only h2 gets no handshake.
// The stream package resets it first; this is the second line.
func TestC02InspectALPN(t *testing.T) {
	for _, mode := range modes {
		e := newEnv(t)
		g := e.start(mode, policy.StreamTLS)
		conf := e.clientConfig()
		conf.NextProtos = []string{"h2"}
		if err := tls.Client(g.conn, conf).HandshakeContext(t.Context()); err == nil {
			t.Error("an h2-only client completed the handshake")
		}
		e.wantReset(g, mode, stream.RuleProtocolMismatch, "check="+string(CheckHandshake))
	}
}

// echConfigList is an ECHConfigList with one X25519, HKDF-SHA256,
// AES-128-GCM configuration for publicName, whose private key nobody
// keeps.
func echConfigList(t *testing.T, publicName string) []byte {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := key.PublicKey().Bytes()
	var c []byte
	c = append(c, 1)                             // config_id
	c = binary.BigEndian.AppendUint16(c, 0x0020) // DHKEM(X25519, HKDF-SHA256)
	c = binary.BigEndian.AppendUint16(c, uint16(len(pub)))
	c = append(c, pub...)
	c = binary.BigEndian.AppendUint16(c, 4)
	c = append(c, 0, 1, 0, 1) // HKDF-SHA256, AES-128-GCM
	c = append(c, 0)          // maximum_name_length
	c = append(c, byte(len(publicName)))
	c = append(c, publicName...)
	c = binary.BigEndian.AppendUint16(c, 0) // extensions
	conf := binary.BigEndian.AppendUint16(nil, 0xfe0d)
	conf = binary.BigEndian.AppendUint16(conf, uint16(len(c)))
	conf = append(conf, c...)
	list := binary.BigEndian.AppendUint16(nil, uint16(len(conf)))
	return append(list, conf...)
}

// TestC02InspectECH: a Go client with an ECH configuration aborts with
// ech_required, as wb-proxyd holds no ECH keys, and the stream is reset
// with tls-ech-refused (S07-16-tls-and-tunnels, "Encrypted ClientHello").
func TestC02InspectECH(t *testing.T) {
	for _, mode := range modes {
		e := newEnv(t)
		g := e.start(mode, policy.StreamTLS)
		conf := e.clientConfig()
		conf.MinVersion = tls.VersionTLS13
		conf.EncryptedClientHelloConfigList = echConfigList(t, testName)
		err := tls.Client(g.conn, conf).HandshakeContext(t.Context())
		if _, ok := errors.AsType[*tls.ECHRejectionError](err); !ok {
			t.Fatalf("the ECH client's handshake = %v, want an ECH rejection", err)
		}
		e.wantReset(g, mode, RuleECHRefused, `alert="encrypted client hello required"`)
		if strings.Contains(e.logs.String(), "known_gap") {
			t.Error("an ECH refusal names the known gap of a rejected leaf")
		}
	}
}

// TestC02InspectRejectedLeaf: a client that doesn't accept the leaf
// aborts, and the stream is reset with tls-client-rejected-leaf, the
// alert's description and the known gap (I246). The Go client sends
// bad_certificate for each failed verification, so a change of
// crypto/tls's alert text fails here and in TestC02InspectECH.
// TestHandshakeErrorRule covers every other alert code.
func TestC02InspectRejectedLeaf(t *testing.T) {
	for _, c := range []struct {
		what    string
		version uint16
		set     func(*tls.Config)
		alert   string
	}{
		{"distrusts the CA, TLS 1.2", tls.VersionTLS12, func(c *tls.Config) { c.RootCAs = x509.NewCertPool() }, "bad certificate"},
		{"distrusts the CA, TLS 1.3", tls.VersionTLS13, func(c *tls.Config) { c.RootCAs = x509.NewCertPool() }, "bad certificate"},
		{"another name, TLS 1.3", tls.VersionTLS13, func(c *tls.Config) { c.ServerName = "example.org" }, "bad certificate"},
	} {
		for _, mode := range modes {
			e := newEnv(t)
			g := e.start(mode, policy.StreamTLS)
			conf := e.clientConfig()
			conf.MaxVersion = c.version
			c.set(conf)
			if err := tls.Client(g.conn, conf).HandshakeContext(t.Context()); err == nil {
				t.Fatalf("%s: the handshake completed", c.what)
			}
			e.wantReset(g, mode, RuleClientRejectedLeaf, `alert="`+c.alert+`"`, "known_gap="+knownGap)
		}
	}
}

// TestC02InspectClientClosed: a client that closes after the server's
// flight, with no alert, during the handshake or after it and before its
// first request byte, is logged as a rejected leaf with "closed".
func TestC02InspectClientClosed(t *testing.T) {
	for _, mode := range modes {
		for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
			e := newEnv(t)
			g := e.start(mode, policy.StreamTLS)
			conf := e.clientConfig()
			conf.MaxVersion = version
			conf.VerifyConnection = func(tls.ConnectionState) error {
				// Half-close, so no alert can follow and the server reads
				// the end.
				_ = g.conn.CloseWrite()
				return errors.New("closing")
			}
			_ = tls.Client(g.conn, conf).HandshakeContext(t.Context())
			e.wantReset(g, mode, RuleClientRejectedLeaf, `alert=closed`, "known_gap="+knownGap)
		}

		e := newEnv(t)
		g := e.start(mode, policy.StreamTLS)
		if err := tls.Client(g.conn, e.clientConfig()).HandshakeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = g.conn.CloseWrite()
		e.wantReset(g, mode, RuleClientRejectedLeaf, `alert=closed`)
	}
}

// TestC02InspectAfterHandshake: bytes after the handshake that aren't an
// HTTP/1.1 or HTTP/1.0 request line, within the cap and by the deadline,
// reset the stream, with no upstream dial (S07-15-non-http-streams,
// "Inside TLS on an inspected host").
func TestC02InspectAfterHandshake(t *testing.T) {
	for _, c := range []struct {
		what  string
		send  string
		close bool
		rule  string
		check string
	}{
		{"SSH", "SSH-2.0-OpenSSH_9.9\r\n", false, stream.RuleProtocolMismatch, string(stream.CheckRequestLine)},
		{"the HTTP/2 preface", "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n", false, stream.RuleProtocolMismatch, string(stream.CheckRequestLine)},
		{"over the cap", "GET /" + strings.Repeat("a", 100), false, stream.RuleProtocolMismatch, string(tlsparse.CheckCap)},
		{"a partial line, then the end", "GET /", true, stream.RuleProtocolMismatch, string(stream.CheckEnded)},
		{"nothing by the deadline", "", false, stream.RuleFirstBytesTimeout, "deadline"},
		{"a partial line by the deadline", "GET /", false, stream.RuleFirstBytesTimeout, "deadline"},
	} {
		for _, mode := range modes {
			e := newEnv(t)
			e.term.firstBytesCap = 64
			e.term.firstBytesTimeout = time.Second
			g := e.start(mode, policy.StreamTLS)
			conn := tls.Client(g.conn, e.clientConfig())
			if err := conn.HandshakeContext(t.Context()); err != nil {
				t.Fatalf("%s: %v", c.what, err)
			}
			if _, err := io.WriteString(conn, c.send); err != nil {
				t.Fatal(err)
			}
			if c.close {
				_ = conn.CloseWrite()
			}
			e.wantReset(g, mode, c.rule, "check="+c.check)
		}
	}
}

// TestC02InspectDeadlineCoversHandshake: a client that stalls in the
// handshake is reset by the first-bytes deadline from the hand-off.
func TestC02InspectDeadlineCoversHandshake(t *testing.T) {
	e := newEnv(t)
	e.term.firstBytesTimeout = 200 * time.Millisecond
	g := e.start(stream.ModeInspect, policy.StreamTLS)
	// The client sends nothing: the stream package replays the ClientHello
	// in production, and a stalled client is all this needs.
	e.wantReset(g, stream.ModeInspect, stream.RuleFirstBytesTimeout, "check=deadline")
}
