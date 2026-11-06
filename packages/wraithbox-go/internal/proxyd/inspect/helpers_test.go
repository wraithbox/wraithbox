package inspect

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/ca"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
)

// testName is the name of every test stream.
const testName = "example.com"

// modes are the modes each refusal is tested in: audit mode relaxes
// none of them (S07-16-tls-and-tunnels, "Not relaxed by audit mode or
// approvals").
var modes = []stream.Mode{stream.ModeInspect, stream.ModeAudit}

// testCA is a CA made in the test, which signs leaves.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "inspect test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// leaf signs a serverAuth leaf for name.
func (c *testCA) leaf(t *testing.T, name string) *tls.Certificate {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// fakeLeaves signs each leaf with its CA, or refuses with ref.
type fakeLeaves struct {
	t   *testing.T
	ca  *testCA
	ref *ca.Refusal

	mu   sync.Mutex
	reqs []ca.LeafRequest
}

func (l *fakeLeaves) For(req ca.LeafRequest) (*tls.Certificate, *ca.Refusal) {
	l.mu.Lock()
	l.reqs = append(l.reqs, req)
	l.mu.Unlock()
	if l.ref != nil {
		return nil, l.ref
	}
	return l.ca.leaf(l.t, req.Name), nil
}

func (l *fakeLeaves) calls() []ca.LeafRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ca.LeafRequest(nil), l.reqs...)
}

// fakeCtl is a stream.Control. Reset and Close close the stream's
// connection, Reset with no linger so the client sees a TCP reset, and
// record how the stream ended. Only the first end counts.
type fakeCtl struct {
	conn  net.Conn
	p     *policy.Policy
	ended chan string
	done  chan struct{}
	once  sync.Once
}

func (c *fakeCtl) end(how string) bool {
	did := false
	c.once.Do(func() {
		did = true
		close(c.done)
		if tc, ok := c.conn.(*net.TCPConn); ok && how == "reset" {
			_ = tc.SetLinger(0)
		}
		_ = c.conn.Close()
		c.ended <- how
	})
	return did
}

func (c *fakeCtl) Reset() bool { return c.end("reset") }

func (c *fakeCtl) Close() bool                                                   { return c.end("close") }
func (c *fakeCtl) Done() <-chan struct{}                                         { return c.done }
func (c *fakeCtl) AddCheck(func(*policy.Policy) egressmsg.Check) egressmsg.Check { return "" }
func (c *fakeCtl) Policy() *policy.Policy                                        { return c.p }

// fakeHTTP takes each stream that reaches the HTTP handler.
type fakeHTTP struct {
	streams chan *Stream
}

func (h *fakeHTTP) Serve(s *Stream) { h.streams <- s }

// fakeDialer dials addr on loopback whatever it is asked for, or
// refuses with err, and counts its calls.
type fakeDialer struct {
	addr  string
	err   error
	dials atomic.Int32
}

func (d *fakeDialer) Dial(ctx context.Context, _ string, _ uint16) (net.Conn, error) {
	d.dials.Add(1)
	if d.err != nil {
		return nil, d.err
	}
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

// lockedBuffer is a log sink for concurrent writers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// env is a Terminator with fakes around it.
type env struct {
	t      *testing.T
	ca     *testCA
	leaves *fakeLeaves
	http   *fakeHTTP
	dialer *fakeDialer
	logs   *lockedBuffer
	term   *Terminator
}

func newEnv(t *testing.T) *env {
	t.Helper()
	c := newTestCA(t)
	e := &env{
		t:      t,
		ca:     c,
		leaves: &fakeLeaves{t: t, ca: c},
		http:   &fakeHTTP{streams: make(chan *Stream, 1)},
		dialer: &fakeDialer{},
		logs:   &lockedBuffer{},
	}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.term = New(Config{Leaves: e.leaves, Dialer: e.dialer, HTTP: e.http, Log: log})
	return e
}

// testPolicy inspects testName on port 443.
func testPolicy(t *testing.T) *policy.Policy {
	t.Helper()
	return &policy.Policy{Endpoints: []policy.Endpoint{{Host: match.ExactHost(name(t)), Ports: []uint16{443}}}}
}

func name(t *testing.T) dnsname.Name {
	t.Helper()
	n, ref := dnsname.FromHost(testName)
	if ref != nil {
		t.Fatal(ref)
	}
	return n
}

// guest is the client side of a stream under inspection.
type guest struct {
	conn  *net.TCPConn
	ctl   *fakeCtl
	ended chan struct{}
}

// start hands a loopback stream to the Terminator in mode, with
// protocol, and returns the client side.
func (e *env) start(mode stream.Mode, protocol policy.StreamProtocol) *guest {
	e.t.Helper()
	ln, err := new(net.ListenConfig).Listen(e.t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatal(err)
	}
	defer ln.Close()
	var d net.Dialer
	client, err := d.DialContext(e.t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		e.t.Fatal(err)
	}
	server, err := ln.Accept()
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	tcp, ok := client.(*net.TCPConn)
	if !ok {
		e.t.Fatalf("the client is a %T, not a TCP connection", client)
	}
	ctl := &fakeCtl{conn: server, p: testPolicy(e.t), ended: make(chan string, 1), done: make(chan struct{})}
	s := &stream.Inspected{
		VM: "work", Generation: 1, ID: 7, Name: name(e.t), Port: 443,
		Protocol: protocol, Mode: mode, Conn: server, Control: ctl, Began: time.Now(),
	}
	g := &guest{conn: tcp, ctl: ctl, ended: make(chan struct{})}
	go func() {
		defer close(g.ended)
		e.term.Inspect(s)
	}()
	return g
}

// clientConfig is a client that trusts the test CA.
func (e *env) clientConfig() *tls.Config {
	return &tls.Config{ServerName: testName, RootCAs: e.ca.pool, MinVersion: tls.VersionTLS12}
}

// wantReset waits for the Terminator to return, and fails the test
// unless it reset the stream and logged rule, in mode, with each of
// attrs.
func (e *env) wantReset(g *guest, mode stream.Mode, rule string, attrs ...string) {
	e.t.Helper()
	select {
	case <-g.ended:
	case <-time.After(10 * time.Second):
		e.t.Fatal("Inspect didn't return")
	}
	select {
	case how := <-g.ctl.ended:
		if how != "reset" {
			e.t.Errorf("the stream ended with %s, want reset", how)
		}
	default:
		e.t.Fatal("the stream didn't end")
	}
	logs := e.logs.String()
	want := append([]string{"rule=" + rule + " decision=reset", "mode=" + mode.String(), `host="\"` + testName + `\""`, "port=443"}, attrs...)
	for _, w := range want {
		if !strings.Contains(logs, w) {
			e.t.Errorf("the log lacks %q:\n%s", w, logs)
		}
	}
	select {
	case <-e.http.streams:
		e.t.Error("a refused stream reached the HTTP handler")
	default:
	}
	if n := e.dialer.dials.Load(); n != 0 {
		e.t.Errorf("%d upstream dials for a refused stream", n)
	}
}

// served waits for the stream to reach the HTTP handler.
func (e *env) served() *Stream {
	e.t.Helper()
	select {
	case s := <-e.http.streams:
		return s
	case <-time.After(10 * time.Second):
		e.t.Fatalf("no stream reached the HTTP handler; logs:\n%s", e.logs)
	}
	return nil
}
