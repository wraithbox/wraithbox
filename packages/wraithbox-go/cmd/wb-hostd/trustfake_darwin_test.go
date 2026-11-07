package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/carecord"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// recordGuest is a carecord.Guest that keeps the CAs it was told to
// install.
type recordGuest struct {
	certs [][]byte
}

func (g *recordGuest) SetClock(context.Context, carecord.Reason) error { return nil }

func (g *recordGuest) InstallTrust(_ context.Context, certs [][]byte) error {
	g.certs = certs
	return nil
}

// newFakeKeeper returns a CA record over a new state.db with the
// child's fake wb-proxyd end, which writes its leaves to dir, and the
// buffer the fake logs to.
func newFakeKeeper(t *testing.T, dir string) (*carecord.Keeper, *caTest, *bytes.Buffer) {
	t.Helper()
	db, err := state.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	fake, err := newFakeCA(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	k := carecord.New(carecord.Config{
		Records: db, Proxyd: fake.Proxyd, Ender: fake.Ender,
		Audit: &audittest.Recorder{}, Log: log,
	})
	return k, fake, &logs
}

// TestFakeCABootWritesLeaf runs the CA record's boot order against the
// fake wb-proxyd end, and checks that the leaf it writes chains to the
// CA the guest was told to install, for leafHost only.
func TestFakeCABootWritesLeaf(t *testing.T) {
	dir := t.TempDir()
	k, _, logs := newFakeKeeper(t, dir)
	g := &recordGuest{}
	if err := k.Boot(t.Context(), string(vm.SlotWork), 1, carecord.ReasonBoot, g); err != nil {
		t.Fatalf("Boot: %v\n%s", err, logs)
	}
	if len(g.certs) != 1 {
		t.Fatalf("the guest was told to install %d CAs, want 1", len(g.certs))
	}
	if strings.Contains(logs.String(), msgFakeEnded) {
		t.Errorf("the fake was ended:\n%s", logs)
	}
	leaf := loadLeaf(t, leafPath(dir, 1))
	ca, err := x509.ParseCertificate(g.certs[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	opts := x509.VerifyOptions{Roots: roots, DNSName: leafHost}
	if _, err := leaf.Leaf.Verify(opts); err != nil {
		t.Errorf("the leaf doesn't verify for %s against the installed CA: %v", leafHost, err)
	}
	opts.DNSName = "example.com"
	if _, err := leaf.Leaf.Verify(opts); err == nil {
		t.Error("the leaf verifies for example.com, want only " + leafHost)
	}
}

// TestFakeCAResetWritesNewLeaf runs the steps of TestC02GuestTrustVM's
// CA change against the fake: a boot, `wb vm reset-ca`, and a boot of a
// new generation. The second leaf chains to a new CA, and the first
// doesn't.
func TestFakeCAResetWritesNewLeaf(t *testing.T) {
	dir := t.TempDir()
	k, _, logs := newFakeKeeper(t, dir)
	first := &recordGuest{}
	if err := k.Boot(t.Context(), string(vm.SlotWork), 1, carecord.ReasonBoot, first); err != nil {
		t.Fatalf("Boot: %v\n%s", err, logs)
	}
	cas, err := k.List(t.Context(), string(vm.SlotWork))
	if err != nil || len(cas) != 1 {
		t.Fatalf("List = %d CAs, %v; want 1", len(cas), err)
	}
	if _, err := k.Reset(t.Context(), string(vm.SlotWork), []string{cas[0].SHA256}, "wb vm reset-ca work"); err != nil {
		t.Fatalf("Reset: %v\n%s", err, logs)
	}
	second := &recordGuest{}
	if err := k.Boot(t.Context(), string(vm.SlotWork), 2, carecord.ReasonBoot, second); err != nil {
		t.Fatalf("Boot after Reset: %v\n%s", err, logs)
	}
	if len(second.certs) != 1 || bytes.Equal(second.certs[0], first.certs[0]) {
		t.Fatalf("after Reset the guest was told to install %d CAs, want 1 new one", len(second.certs))
	}
	if strings.Contains(logs.String(), msgFakeEnded) {
		t.Errorf("the fake was ended:\n%s", logs)
	}
	ca, err := x509.ParseCertificate(second.certs[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	opts := x509.VerifyOptions{Roots: roots, DNSName: leafHost}
	if _, err := loadLeaf(t, leafPath(dir, 2)).Leaf.Verify(opts); err != nil {
		t.Errorf("the new leaf doesn't verify against the new CA: %v", err)
	}
	if _, err := loadLeaf(t, leafPath(dir, 1)).Leaf.Verify(opts); err == nil {
		t.Error("the dropped CA's leaf verifies against the new CA")
	}
}

// loadLeaf reads a leaf file the fake wrote: its chain, then its key.
func loadLeaf(t *testing.T, path string) tls.Certificate {
	t.Helper()
	leaf, err := tls.LoadX509KeyPair(path, path)
	if err != nil {
		t.Fatalf("the fake's leaf: %v", err)
	}
	return leaf
}

// TestFakeCABootHook checks the time hook the fake wraps: a call that
// isn't the boot's goes to send alone, and a boot call whose clock
// isn't set returns send's error, so the link is dropped as with send
// alone.
func TestFakeCABootHook(t *testing.T) {
	k, fake, _ := newFakeKeeper(t, t.TempDir())
	var reasons []guestv1.TimeReason
	clockErr := errors.New("no clock")
	send := func(_ context.Context, _ vm.Slot, _ uint64, _ *guestlink.Link, reason guestv1.TimeReason) error {
		reasons = append(reasons, reason)
		if reason == guestv1.TimeReason_TIME_REASON_BOOT {
			return clockErr
		}
		return nil
	}
	hook := fake.Boot(k, send)
	if err := hook(t.Context(), vm.SlotWork, 1, nil, guestv1.TimeReason_TIME_REASON_WAKE); err != nil {
		t.Errorf("a wake call: %v, want send's nil", err)
	}
	if err := hook(t.Context(), vm.SlotWork, 1, nil, guestv1.TimeReason_TIME_REASON_BOOT); !errors.Is(err, clockErr) {
		t.Errorf("a boot call whose clock fails: %v, want send's error", err)
	}
	want := []guestv1.TimeReason{guestv1.TimeReason_TIME_REASON_WAKE, guestv1.TimeReason_TIME_REASON_BOOT}
	if len(reasons) != len(want) || reasons[0] != want[0] || reasons[1] != want[1] {
		t.Errorf("send saw %v, want %v", reasons, want)
	}
}

// TestTLSServer builds testdata/tlsserver for the host and checks that
// it presents the fake's leaf, answers a git client's paths, and
// tunnels a CONNECT for leafHost to its TLS listener.
func TestTLSServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	dir := t.TempDir()
	k, _, logs := newFakeKeeper(t, dir)
	g := &recordGuest{}
	if err := k.Boot(t.Context(), string(vm.SlotWork), 1, carecord.ReasonBoot, g); err != nil {
		t.Fatalf("Boot: %v\n%s", err, logs)
	}
	ca, err := x509.ParseCertificate(g.certs[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	tlsPort, proxyPort := startTLSServer(t, dir, leafPath(dir, 1))

	direct := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: leafHost, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+tlsPort)
		},
	}}
	for path, want := range map[string]string{
		"/":                   "wbtest-ok\n",
		"/repo.git/HEAD":      "ref: refs/heads/main\n",
		"/repo.git/info/refs": "0123456789abcdef0123456789abcdef01234567\trefs/heads/main\n",
	} {
		if got := get(t, direct, "https://"+leafHost+path); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
	proxied := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Proxy:           http.ProxyURL(mustURL(t, "http://127.0.0.1:"+proxyPort)),
	}}
	if got := get(t, proxied, "https://"+leafHost+"/"); got != "wbtest-ok\n" {
		t.Errorf("GET through the proxy = %q, want wbtest-ok", got)
	}
	untrusting := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12},
		Proxy:           http.ProxyURL(mustURL(t, "http://127.0.0.1:"+proxyPort)),
	}}
	if _, err := fetch(t.Context(), untrusting, "https://"+leafHost+"/"); err == nil {
		t.Error("a client without the CA accepted the leaf")
	}
}

// startTLSServer builds testdata/tlsserver for the host into dir, starts
// it with the leaf at cert, and returns its TLS and proxy ports.
func startTLSServer(t *testing.T, dir, cert string) (string, string) {
	t.Helper()
	exe := filepath.Join(dir, "tlsserver")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", exe, "./testdata/tlsserver").CombinedOutput(); err != nil {
		t.Fatalf("go build testdata/tlsserver: %v\n%s", err, out)
	}
	ports := filepath.Join(dir, "ports")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, exe, "-cert", cert, "-ports", ports, "-lifetime", "1m")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		t.Logf("tlsserver:\n%s", stderr.String())
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(ports)
		if err == nil {
			f := strings.Fields(string(b))
			if len(f) != 2 {
				t.Fatalf("ports file %q, want two ports", b)
			}
			return f[0], f[1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("tlsserver wrote no ports: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	body, err := fetch(t.Context(), c, url)
	if err != nil {
		t.Errorf("GET %s: %v", url, err)
	}
	return body
}

// fetch returns the body of a GET of url.
func fetch(ctx context.Context, c *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
