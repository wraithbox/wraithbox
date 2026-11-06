package inspect

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/upstream"
)

// upstreamServer is a TLS server on loopback, with a certificate for
// name from its own CA, that answers each connection with "ok" after
// the handshake.
type upstreamServer struct {
	ca    *testCA
	addr  string
	state chan tls.ConnectionState
}

func newUpstream(t *testing.T, name string, conf *tls.Config) *upstreamServer {
	t.Helper()
	u := &upstreamServer{ca: newTestCA(t), state: make(chan tls.ConnectionState, 1)}
	conf.Certificates = []tls.Certificate{*u.ca.leaf(t, name)}
	conf.NextProtos = []string{"h2", "http/1.1"}
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	u.addr = ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			tc := tls.Server(c, conf)
			if tc.HandshakeContext(t.Context()) == nil {
				u.state <- tc.ConnectionState()
				_, _ = io.WriteString(tc, "ok")
			}
			_ = tc.Close()
		}
	}()
	return u
}

// tlsStream is a TLS stream as the HTTP handler gets it, in mode.
func (e *env) tlsStream(mode stream.Mode) *Stream {
	return &Stream{Inspected: &stream.Inspected{VM: "work", Generation: 1, ID: 7, Name: name(e.t), Port: 443, Mode: mode}, TLS: true}
}

// open calls Upstream and reads the first bytes, as the HTTP handler
// does after its request, and returns the bytes or the error.
func (e *env) open(s *Stream) (string, error) {
	e.t.Helper()
	conn, err := e.term.Upstream(e.t.Context(), s)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	b, err := io.ReadAll(conn)
	if len(b) > 0 {
		return string(b), nil
	}
	return "", err
}

// wantRefusal fails the test unless err is a Refusal with rule, logged
// with the stream in mode.
func (e *env) wantRefusal(err error, mode stream.Mode, rule string) {
	e.t.Helper()
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != rule {
		e.t.Fatalf("Upstream = %v, want a refusal with %s", err, rule)
	}
	want := "rule=" + rule + " decision=" + decisionUpstream + " mode=" + mode.String()
	if !strings.Contains(e.logs.String(), want) {
		e.t.Errorf("the log lacks %q:\n%s", want, e.logs)
	}
}

// TestC02UpstreamTLS: Upstream verifies the server for the stream's
// name, offers http/1.1 only and presents no certificate to a server
// that only asks for one.
func TestC02UpstreamTLS(t *testing.T) {
	e := newEnv(t)
	u := newUpstream(t, testName, &tls.Config{ClientAuth: tls.RequestClientCert})
	e.term.roots = u.ca.pool
	e.dialer.addr = u.addr
	got, err := e.open(e.tlsStream(stream.ModeInspect))
	if err != nil || got != "ok" {
		t.Fatalf("Upstream read %q, %v, want ok; logs:\n%s", got, err, e.logs)
	}
	st := <-u.state
	if st.NegotiatedProtocol != stream.ALPNHTTP1 || len(st.PeerCertificates) != 0 {
		t.Errorf("upstream saw ALPN %q and %d client certificates, want http/1.1 and none", st.NegotiatedProtocol, len(st.PeerCertificates))
	}
}

// TestC02UpstreamPlain: a plain HTTP stream gets the dialed connection.
func TestC02UpstreamPlain(t *testing.T) {
	e := newEnv(t)
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			_, _ = io.WriteString(c, "plain")
			_ = c.Close()
		}
	}()
	e.dialer.addr = ln.Addr().String()
	s := e.tlsStream(stream.ModeInspect)
	s.TLS = false
	if got, err := e.open(s); got != "plain" {
		t.Errorf("Upstream read %q, %v, want the plain bytes", got, err)
	}
}

// TestC02UpstreamDialRefused: the dialer's refusal comes back with its
// rule.
func TestC02UpstreamDialRefused(t *testing.T) {
	e := newEnv(t)
	e.dialer.err = &upstream.Refusal{Rule: upstream.RuleAddressRefused, Host: testName, Reason: "test"}
	_, err := e.term.Upstream(t.Context(), e.tlsStream(stream.ModeInspect))
	var ref *upstream.Refusal
	if !errors.As(err, &ref) || ref.Rule != upstream.RuleAddressRefused {
		t.Errorf("Upstream = %v, want the dialer's refusal", err)
	}
}

// TestC02UpstreamClientCert: an upstream that requires a client
// certificate gives tls-client-cert-unsupported, with TLS 1.2 in the
// handshake and with TLS 1.3 on the first read (S07-16-tls-and-tunnels,
// "TLS with a client certificate").
func TestC02UpstreamClientCert(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, mode := range modes {
			e := newEnv(t)
			u := newUpstream(t, testName, &tls.Config{ClientAuth: tls.RequireAnyClientCert, MaxVersion: version})
			e.term.roots = u.ca.pool
			e.dialer.addr = u.addr
			_, err := e.open(e.tlsStream(mode))
			e.wantRefusal(err, mode, RuleClientCertUnsupported)
		}
	}
}

// TestC02UpstreamVerify: an upstream whose certificate the roots don't
// trust, or that names another host, gives upstream-tls-verify.
func TestC02UpstreamVerify(t *testing.T) {
	for _, c := range []struct {
		what  string
		name  string
		roots func(u *upstreamServer) *x509.CertPool
	}{
		{"an untrusted CA", testName, func(*upstreamServer) *x509.CertPool { return x509.NewCertPool() }},
		{"another name", "example.org", func(u *upstreamServer) *x509.CertPool { return u.ca.pool }},
	} {
		for _, mode := range modes {
			e := newEnv(t)
			u := newUpstream(t, c.name, &tls.Config{})
			e.term.roots = c.roots(u)
			e.dialer.addr = u.addr
			_, err := e.open(e.tlsStream(mode))
			e.wantRefusal(err, mode, RuleUpstreamVerify)
		}
	}
}

// TestC02UpstreamFailed: another handshake failure, here a server that
// isn't TLS, gives upstream-tls-failed.
func TestC02UpstreamFailed(t *testing.T) {
	for _, mode := range modes {
		e := newEnv(t)
		ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			if c, err := ln.Accept(); err == nil {
				_, _ = io.WriteString(c, "SSH-2.0-OpenSSH_9.9\r\n")
				_ = c.Close()
			}
		}()
		e.dialer.addr = ln.Addr().String()
		_, err = e.open(e.tlsStream(mode))
		e.wantRefusal(err, mode, RuleUpstreamFailed)
		_ = ln.Close()
	}
}
