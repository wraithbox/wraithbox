//go:build darwin || linux

package trust

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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

// fixture is an Installer over a temporary trust directory that doesn't
// exist yet, and a public roots file of two certificates.
type fixture struct {
	t     *testing.T
	in    *Installer
	logs  *lockedBuffer
	roots []*x509.Certificate
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	roots := []*x509.Certificate{
		parse(t, selfSigned(t, "Public Root A", true, newP256(t))),
		parse(t, selfSigned(t, "Public Root B", true, newP256(t))),
	}
	var pemRoots bytes.Buffer
	// A block of another type is skipped.
	_ = pem.Encode(&pemRoots, &pem.Block{Type: "TRUSTED CERTIFICATE", Bytes: []byte("skip me")})
	pemRoots.Write(encodePEM(roots))
	rootsPath := filepath.Join(base, "cert.pem")
	if err := os.WriteFile(rootsPath, pemRoots.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// The installer makes etc/wraithbox and etc/wraithbox/trust, as the
	// guest has /etc.
	if err := os.Mkdir(filepath.Join(base, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	in := &Installer{
		log:   slog.New(slog.NewJSONHandler(logs, nil)),
		dir:   filepath.Join(base, "etc", "wraithbox", "trust"),
		roots: rootsPath,
		owner: uint32(os.Geteuid()),
	}
	return &fixture{t: t, in: in, logs: logs, roots: roots}
}

// path is the full path of a file in the trust directory.
func (f *fixture) path(name string) string { return filepath.Join(f.in.dir, name) }

// read returns a trust file's bytes.
func (f *fixture) read(name string) []byte {
	f.t.Helper()
	data, err := os.ReadFile(f.path(name))
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}

// install runs Install and fails the test unless it succeeds.
func (f *fixture) install(ders ...[]byte) {
	f.t.Helper()
	if r := f.in.Install(f.t.Context(), ders); r != nil {
		f.t.Fatalf("Install: %v; logs:\n%s", r, f.logs)
	}
}

// snapshot is every entry of the trust directory and its bytes, or nil
// when the directory doesn't exist.
func (f *fixture) snapshot() map[string]string {
	f.t.Helper()
	entries, err := os.ReadDir(f.in.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		out[e.Name()] = string(f.read(e.Name()))
	}
	return out
}

func newP256(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// selfSigned returns the DER of a self-signed certificate with key,
// with basic constraints CA set to ca.
func selfSigned(t testing.TB, name string, ca bool, key crypto.Signer) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  ca,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// rsaCA returns the DER of a self-signed CA with an RSA key.
func rsaCA(t testing.TB) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return selfSigned(t, "RSA CA", true, k)
}

func parse(t testing.TB, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pemCerts parses every CERTIFICATE block of data, and fails the test on
// another block or one that doesn't parse.
func pemCerts(t testing.TB, data []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for rest := data; len(bytes.TrimSpace(rest)) > 0; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil || b.Type != "CERTIFICATE" {
			t.Fatalf("not a CERTIFICATE block at %q", rest)
		}
		parse(t, b.Bytes)
		out = append(out, b.Bytes)
	}
	return out
}
