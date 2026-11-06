//go:build darwin || linux

package trust

import (
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey/hwkeytest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/ca"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// clientEnv names the environment variable that makes
// TestTLSClientHelper the client of TestC02TrustTLSClient.
const clientEnv = "WB_TRUST_TLS_CLIENT"

// leafHost is the name the inspected leaf is for.
const leafHost = "example.com"

// TestTLSClientHelper is the client of TestC02TrustTLSClient, in a child
// process whose environment sets SSL_CERT_FILE. It does nothing in a
// normal run. Its connection is file 3, and it verifies the server with
// Go's system roots, which SSL_CERT_FILE replaces.
func TestTLSClientHelper(t *testing.T) {
	if os.Getenv(clientEnv) == "" {
		t.Skip("the client of TestC02TrustTLSClient")
	}
	conn, err := net.FileConn(os.NewFile(3, "conn"))
	if err != nil {
		t.Fatal(err)
	}
	c := tls.Client(conn, &tls.Config{ServerName: leafHost, MinVersion: tls.VersionTLS13})
	defer func() { _ = c.Close() }()
	if err := c.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("handshake: %v", err)
	}
}

// TestC02TrustTLSClient: a Go tls.Client with SSL_CERT_FILE set to the
// written bundle accepts a leaf that wb-proxyd's CA signed (with the
// fake key), and one with the previous install's bundle refuses it
// after a CA change.
func TestC02TrustTLSClient(t *testing.T) {
	a := newAuthority(t)
	f := newFixture(t)
	oldCA := a.issue(t, "0123456789abcdef0123456789abcdef")
	f.install(oldCA)
	oldBundle := filepath.Join(t.TempDir(), "old-bundle.pem")
	mustDo(t, os.WriteFile(oldBundle, f.read(BundleFile), 0o644))
	newCA := a.issue(t, "fedcba9876543210fedcba9876543210")
	f.install(newCA)
	leaf := a.leaf(t, "fedcba9876543210fedcba9876543210", newCA)

	if out, err := handshake(t, f.path(BundleFile), leaf); err != nil || !strings.Contains(out, "--- PASS: TestTLSClientHelper") {
		t.Errorf("with the new bundle: %v\n%s", err, out)
	}
	out, err := handshake(t, oldBundle, leaf)
	if err == nil || !strings.Contains(out, "certificate signed by unknown authority") {
		t.Errorf("with the previous install's bundle: %v, want the new CA's leaf refused\n%s", err, out)
	}
}

// handshake serves leaf to a child process's tls.Client whose
// SSL_CERT_FILE is bundle, and returns the child's output and error.
func handshake(t *testing.T, bundle string, leaf *tls.Certificate) (string, error) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	server, client := os.NewFile(uintptr(fds[0]), "server"), os.NewFile(uintptr(fds[1]), "client")
	defer func() { _ = client.Close() }()
	conn, err := net.FileConn(server)
	_ = server.Close()
	if err != nil {
		t.Fatal(err)
	}
	srv := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*leaf}, MinVersion: tls.VersionTLS13})
	defer func() { _ = srv.Close() }()
	go func() { _ = srv.HandshakeContext(t.Context()) }()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTLSClientHelper$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), clientEnv+"=1", "SSL_CERT_FILE="+bundle, "SSL_CERT_DIR=")
	cmd.ExtraFiles = []*os.File{client}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// authority is wb-proxyd's CA over the fake store and the fake key
// store, for one VM.
type authority struct {
	a   *ca.Authority
	gen uint64
}

const (
	testTeam = "ABCDE12345"
	testVM   = "work"
)

func newAuthority(t *testing.T) *authority {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	backend := storetest.New(testTeam, storeitem.Group(testTeam, storeitem.RoleCA))
	store, err := secretstore.New(storeitem.ProgramProxyd, backend, log)
	if err != nil {
		t.Fatal(err)
	}
	return &authority{a: ca.New(ca.Config{Store: store, Keys: &hwkeytest.Fake{}, Log: log})}
}

// issue issues a CA and returns its certificate.
func (a *authority) issue(t *testing.T, id string) []byte {
	t.Helper()
	got := a.a.Issue(t.Context(), &egressv1.CaIssue{Vm: testVM, CaId: id})
	if got.GetRule() != "" {
		t.Fatalf("Issue: %s", got.GetRule())
	}
	return got.GetCertificate()
}

// leaf makes CA id the VM's signing CA and returns its leaf for
// leafHost.
func (a *authority) leaf(t *testing.T, id string, der []byte) *tls.Certificate {
	t.Helper()
	a.gen++
	set := &egressv1.CaSet{
		Vm: testVM, Generation: a.gen, SigningCaId: id,
		Cas: []*egressv1.InspectionCa{{CaId: id, Certificate: der}},
	}
	if reply, err := a.a.SetCAs(t.Context(), set); err != nil || reply.GetCaLoaded() == nil {
		t.Fatalf("SetCAs = %v, %v", reply, err)
	}
	if err := a.a.Accept(&egressv1.CaAccept{Vm: testVM, Generation: a.gen}); err != nil {
		t.Fatal(err)
	}
	leaf, ref := a.a.For(testVM, a.gen, leafHost)
	if ref != nil {
		t.Fatal(ref)
	}
	if _, err := x509.ParseCertificate(leaf.Certificate[0]); err != nil {
		t.Fatal(err)
	}
	return leaf
}
