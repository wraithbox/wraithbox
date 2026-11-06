package stream

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/tlsparse/tlstest"
)

var (
	ccs        = tlstest.Record(tlstest.TypeChangeCipherSpec, []byte{1})
	appData    = tlstest.Record(tlstest.TypeApplicationData, []byte("early"))
	serverDone = tlstest.Record(tlstest.TypeHandshake, tlstest.ServerHello(false))
	retry      = tlstest.Record(tlstest.TypeHandshake, tlstest.ServerHello(true))
)

// TestC02PassHello sends ClientHellos to a pass host that its checks
// refuse before any dial (S07-16-tls-and-tunnels, "Pass").
func TestC02PassHello(t *testing.T) {
	tests := []struct {
		name  string
		ext   uint16
		rule  string
		check egressmsg.Check
	}{
		{"ECH", tlsparse.ExtEncryptedClientHello, RuleECHPass, "extension"},
		{"ESNI", tlsparse.ExtEncryptedServerName, RuleECHPass, "extension"},
		{"early_data", tlsparse.ExtEarlyData, RuleProtocolMismatch, CheckEarlyData},
		{"unknown extension", 0x1234, RuleExtensionUnknown, "extension"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := newHarness(t, "")
			x.start(testPolicy(t), passHost, 443)
			x.send(tlstest.Hello{ServerName: passHost, Extensions: []tlstest.Extension{{Type: tt.ext}}}.Records())
			x.wantRefused("reset", tt.rule, tt.check, "tls")
			x.wantNoDial()
		})
	}
	t.Run("SNI differs", func(t *testing.T) {
		x := newHarness(t, "")
		x.start(testPolicy(t), passHost, 443)
		x.send(tlstest.Hello{ServerName: inspectHost}.Records())
		x.wantRefused("reset", RuleSNIMismatch, "server-name-differs", "tls")
		x.wantNoDial()
	})
}

// TestC02PassRecords sends client records before the ServerHello that a
// pass relay refuses, and sees the stream reset with its rule and
// nothing past the accepted records reach the server
// (S07-16-tls-and-tunnels, "Pass").
func TestC02PassRecords(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	other := tlstest.Hello{ServerName: "other.example.com"}.Records()
	notHello := tlstest.Record(tlstest.TypeHandshake, []byte{20, 0, 0, 1, 0})
	tests := []struct {
		name string
		// first is sent with the ClientHello; after is sent once the
		// guest read the server's retry.
		first, after []byte
		retry        bool
		rule         string
		check        egressmsg.Check
		upstream     []byte
	}{
		{"application data", appData, nil, false, RuleProtocolMismatch, CheckEarlyData, hello},
		{"handshake that isn't a ClientHello", notHello, nil, false, RuleProtocolMismatch, CheckClientRecord, hello},
		{"second ClientHello without a retry", hello, nil, false, RuleProtocolMismatch, CheckClientRecord, hello},
		{"second change cipher spec", cat(ccs, ccs), nil, false, RuleProtocolMismatch, CheckChangeCipherSpec, cat(hello, ccs)},
		{"change cipher spec of 2", tlstest.Record(tlstest.TypeChangeCipherSpec, []byte{2}), nil, false, RuleProtocolMismatch, CheckChangeCipherSpec, hello},
		{"other SNI after a retry", nil, cat(ccs, other), true, RuleSNIMismatch, "server-name-differs", cat(hello, ccs)},
		{"ECH after a retry", nil, tlstest.Hello{ServerName: passHost, Extensions: []tlstest.Extension{{Type: tlsparse.ExtEncryptedClientHello}}}.Records(), true, RuleECHPass, "extension", hello},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, func(s *server, c net.Conn) {
				s.read(c, len(hello))
				if tt.retry {
					_, _ = c.Write(retry)
				}
				s.readRest(c)
			})
			x := newHarness(t, srv.addr())
			x.start(testPolicy(t), passHost, 443)
			x.send(cat(hello, tt.first))
			if tt.retry {
				readN(t, x.guest, len(retry))
				x.send(tt.after)
			}
			x.wantRefused("reset", tt.rule, tt.check, "tls")
			if got := srv.received(t); !bytes.Equal(got, tt.upstream) {
				t.Errorf("server got %x, want %x", got, tt.upstream)
			}
		})
	}
}

// TestC02PassServerHelloEarly has the server send a ServerHello after its
// HelloRetryRequest before the client's second ClientHello, which ends
// the relay, so the checks can't end before that ClientHello is read.
func TestC02PassServerHelloEarly(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	srv := newServer(t, func(s *server, c net.Conn) {
		s.read(c, len(hello))
		_, _ = c.Write(cat(retry, serverDone))
		s.readRest(c)
	})
	x := newHarness(t, srv.addr())
	x.start(testPolicy(t), passHost, 443)
	x.send(hello)
	x.wantRefused("reset", RuleProtocolMismatch, CheckServerRecords, "tls")
}

// TestC02PassServerRecords has the server send records before its
// ServerHello that the flight check refuses.
func TestC02PassServerRecords(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	for name, reply := range map[string][]byte{
		"application data": appData,
		"two retries":      cat(retry, retry),
		"not TLS":          []byte("HTTP/1.1 400 Bad Request\r\n\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			srv := newServer(t, func(s *server, c net.Conn) {
				s.read(c, len(hello))
				_, _ = c.Write(reply)
				s.readRest(c)
			})
			x := newHarness(t, srv.addr())
			x.start(testPolicy(t), passHost, 443)
			x.send(hello)
			x.wantRefused("reset", RuleProtocolMismatch, CheckServerRecords, "tls")
		})
	}
}

// TestC02PassOneCCS relays the one change cipher spec, and after the
// ServerHello relays the rest unchanged.
func TestC02PassOneCCS(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	srv := newServer(t, func(s *server, c net.Conn) {
		s.read(c, len(hello)+len(ccs))
		_, _ = c.Write(cat(serverDone, []byte("server bytes")))
		s.readRest(c)
	})
	x := newHarness(t, srv.addr())
	x.start(testPolicy(t), passHost, 443)
	x.send(cat(hello, ccs))
	got := readN(t, x.guest, len(serverDone)+len("server bytes"))
	if !bytes.Equal(got, cat(serverDone, []byte("server bytes"))) {
		t.Errorf("guest got %x", got)
	}
	x.send(cat(appData, []byte("anything")))
	x.closeWrite()
	if got, want := srv.received(t), cat(hello, ccs, appData, []byte("anything")); !bytes.Equal(got, want) {
		t.Errorf("server got %x, want %x", got, want)
	}
	if how := x.ended(); how != "close" {
		t.Errorf("ended with %q", how)
	}
	<-x.done
	for _, w := range []string{"rule=" + RulePassAllowed, "mode=pass", "pinned client", "end=closed"} {
		if !strings.Contains(x.logs.String(), w) {
			t.Errorf("log lacks %q:\n%s", w, x.logs.String())
		}
	}
}

// TestC02PassAlert sends an alert before the ServerHello, and sees both
// ways closed with tls-client-alert and the alert not relayed.
func TestC02PassAlert(t *testing.T) {
	hello := tlstest.Hello{ServerName: passHost}.Records()
	srv := newServer(t, func(s *server, c net.Conn) {
		s.read(c, len(hello))
		s.readRest(c)
	})
	x := newHarness(t, srv.addr())
	x.start(testPolicy(t), passHost, 443)
	x.send(cat(hello, tlstest.Record(tlstest.TypeAlert, []byte{2, 40})))
	x.wantRefused("close", RuleClientAlert, "alert", "tls")
	if got := srv.received(t); !bytes.Equal(got, hello) {
		t.Errorf("server got %x", got)
	}
	for _, w := range []string{"alert_level=2", "alert_description=40"} {
		if !strings.Contains(x.logs.String(), w) {
			t.Errorf("log lacks %q", w)
		}
	}
}

// TestC02PassHandshake runs a full TLS 1.3 handshake of crypto/tls
// through a pass relay. The server takes only P-256, so it sends a
// HelloRetryRequest, and the client a change cipher spec and a second
// ClientHello, which pass their checks.
func TestC02PassHandshake(t *testing.T) {
	cert, pool := selfSigned(t, passHost)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates:     []tls.Certificate{cert},
		MinVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.CurveP256},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(waitLong))
		_, _ = io.Copy(c, c)
	}()
	x := newHarness(t, ln.Addr().String())
	x.start(testPolicy(t), passHost, 443)
	client := tls.Client(x.guest, &tls.Config{
		ServerName: passHost, RootCAs: pool, MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2"},
	})
	_ = client.SetDeadline(time.Now().Add(waitLong))
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("handshake: %v\n%s", err, x.logs.String())
	}
	if !client.ConnectionState().HelloRetryRequest {
		t.Error("the handshake had no HelloRetryRequest")
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if got := readN(t, client, 4); string(got) != "ping" {
		t.Errorf("echo %q", got)
	}
	_ = client.Close()
	x.ended()
	<-x.done
	if strings.Contains(x.logs.String(), "decision=reset") {
		t.Errorf("a refusal:\n%s", x.logs.String())
	}
}

func selfSigned(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func cat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(waitLong))
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}
