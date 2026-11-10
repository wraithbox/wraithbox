//go:build darwin && cgo && wbstorehost

package ca

// The host tests of the CA item round trip (C02-streams-and-tls, "CA
// key"): the real secret store and the real Secure Enclave key store.
// They run only in a test binary that `mise run store:host-test` signs
// with the team's identity into a bundle named wb-proxyd, with the
// groups of wb-proxyd's roles, and starts with an empty environment but
// for WB_STORE_HOST_TEST=1 and WB_STORE_HOST_STEP. They build only with
// the wbstorehost tag, which that task sets, and without the variables
// each one skips.

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	// hostEnv turns the host tests on.
	hostEnv = "WB_STORE_HOST_TEST"
	// stepEnv names the step of the run: "" for the main run,
	// restore-child for the second process of the round trip,
	// confine-proxyd for the run under wb-proxyd's profile, and bench for
	// the leaf benchmark.
	stepEnv = "WB_STORE_HOST_STEP"
	// The second process's CA: its VM, its id, and its certificate in
	// hex.
	vmEnv   = "WB_STORE_HOST_VM"
	caIDEnv = "WB_STORE_HOST_CA_ID"
	certEnv = "WB_STORE_HOST_CERT"
	// hostPrefix starts the VM name of every item a host test makes.
	// Each test deletes its items by their exact names. wb-proxyd may not
	// list the ca group (S15-03-access-rules, "Declared access"), so the
	// secret store's bundle finds and removes what a run left
	// (TestC05HostSweep).
	hostPrefix = "wbtest-"
)

// hostStore is the real store, opened as wb-proxyd by TestMain before
// any test confines the process: under a profile the process can't read
// its own signature (decision 7 on I505).
var hostStore secretstore.Store

// proxydProfile is wb-proxyd's profile as X23-sandboxed-daemons ran it
// (i61), with the bundle's path as BUNDLE. wb-proxyd's own copy comes
// with I512. This one is the test's, so the measurement doesn't depend
// on that program.
var proxydProfile = selfsandbox.Profile{
	Name: "proxyd-host-test",
	Text: `(version 1)
(deny default)
(allow network-outbound (remote tcp))
(allow file-read-metadata (literal "/etc") (literal "/var"))
(allow file-read* (literal "/private/etc/resolv.conf") (literal "/private/var/run/resolv.conf") (literal "/private/etc/hosts"))
(allow network-outbound (remote udp "*:53"))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
(allow mach-lookup (global-name "com.apple.trustd.agent"))
(allow file-read-metadata (literal (param "BUNDLE")))
(allow mach-lookup (global-name "com.apple.securityd.xpc" "com.apple.ctkd.token-client"))
`,
}

func TestMain(m *testing.M) {
	var openErr error
	if os.Getenv(hostEnv) == "1" {
		hostStore, openErr = secretstore.Open(storeitem.ProgramProxyd)
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "host test: opening the store as wb-proxyd: %v\n", openErr)
		}
	}
	code := m.Run()
	if openErr != nil {
		code = 1
	}
	os.Exit(code)
}

// hostStep skips the test unless the host tests are on, the run's step
// is step, the store opened, and the Mac has a Secure Enclave.
func hostStep(tb testing.TB, step string) {
	tb.Helper()
	if os.Getenv(hostEnv) != "1" {
		tb.Skip("a host test: mise run store:host-test runs it in a team-signed bundle")
	}
	if got := os.Getenv(stepEnv); got != step {
		tb.Skipf("the run's step is %q, and this test's %q", got, step)
	}
	if model, err := syscall.Sysctl("hw.model"); err != nil || strings.HasPrefix(model, "VirtualMac") {
		tb.Skipf("no Secure Enclave: hw.model is %q (%v), a VM (decision 5 on I509)", model, err)
	}
	if hostStore == nil {
		tb.Fatal("the store didn't open")
	}
	tb.Log(">>> WATCH: no dialog and no Touch ID prompt may appear during this test")
}

// hostAuthority is an Authority over the real store and key store,
// logging to the test's output.
func hostAuthority(tb testing.TB) *Authority {
	log := slog.New(slog.NewTextHandler(tb.Output(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(Config{Store: hostStore, Keys: hwkey.Open(), Log: log})
}

// random returns n random bytes in hex.
func random(tb testing.TB, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		tb.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// hostCA names a new CA of a host test's VM, and deletes its item when
// the test ends, whatever the test did.
func hostCA(tb testing.TB) (vm, id string) {
	tb.Helper()
	vm, id = hostPrefix+random(tb, 6), random(tb, 16)
	n, err := itemName(vm, id)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := hostAuthority(tb).remove(context.Background(), n); err != nil {
			tb.Errorf("removing %s: %v", n, err)
		}
	})
	return vm, id
}

// hostIssue issues the CA with a, and fails the test unless it succeeds.
func hostIssue(tb testing.TB, a *Authority, vm, id string) []byte {
	tb.Helper()
	got := a.Issue(tb.Context(), &egressv1.CaIssue{Vm: vm, CaId: id})
	if got.GetRule() != "" || len(got.GetCertificate()) == 0 {
		tb.Fatalf("Issue(%s, %s) = rule %q, %d bytes", vm, id, got.GetRule(), len(got.GetCertificate()))
	}
	return got.GetCertificate()
}

// hostSet sends a with a CaSet of generation 1 that has the CA alone as
// its signing CA, and returns the reply.
func hostSet(tb testing.TB, a *Authority, vm, id string, der []byte) *egressv1.ProxydToHostd {
	tb.Helper()
	reply, err := a.SetCAs(tb.Context(), &egressv1.CaSet{
		Vm: vm, Generation: 1, SigningCaId: id,
		Cas: []*egressv1.InspectionCa{{CaId: id, Certificate: der}},
	})
	if err != nil {
		tb.Fatalf("SetCAs: %v", err)
	}
	return reply
}

// restoreAndLeaf restores the CA in a, a new Authority as in a new
// wb-proxyd process, accepts it, and checks a leaf for example.com
// signed by it.
func restoreAndLeaf(tb testing.TB, a *Authority, vm, id string, der []byte) {
	tb.Helper()
	if reply := hostSet(tb, a, vm, id, der); reply.GetCaLoaded() == nil {
		tb.Fatalf("SetCAs = %v, want CaLoaded", reply)
	}
	if err := a.Accept(&egressv1.CaAccept{Vm: vm, Generation: 1}); err != nil {
		tb.Fatalf("Accept: %v", err)
	}
	leaf, ref := a.For(leafReq(vm, 1, "example.com"))
	if ref != nil {
		tb.Fatalf("For: %v", ref)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatal(err)
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		tb.Fatal(err)
	}
	if err := cert.CheckSignatureFrom(ca); err != nil {
		tb.Fatalf("the leaf's signature doesn't verify with the CA: %v", err)
	}
}

// TestC02HostIssueRoundTrip issues a CA with the real store and key
// store, reads back its item's attributes, restores the CA in a second
// process (the test runs itself again) and has it sign a leaf, then
// deletes the item (S09-06-ca-keys; C02-streams-and-tls, "CA key").
func TestC02HostIssueRoundTrip(t *testing.T) {
	hostStep(t, "")
	vm, id := hostCA(t)
	der := hostIssue(t, hostAuthority(t), vm, id)

	n, _ := itemName(vm, id)
	attrs, st := secretstore.ReadItemAttributes(n.String())
	if st != secretstore.StatusOK {
		t.Fatalf("attributes of %s: OSStatus %d", n, st)
	}
	t.Logf("attributes of %s: %+v", n, attrs)
	if attrs.Count != 1 || !strings.HasSuffix(attrs.Group, ".nl.wraithbox."+string(storeitem.RoleCA)) ||
		!attrs.WhenUnlockedThisDeviceOnly || attrs.Synchronizable == 1 {
		t.Errorf("attributes of %s: %+v, want one item in the ca group, WhenUnlockedThisDeviceOnly, not synchronizable", n, attrs)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.v", "-test.count=1", "-test.run", "^TestC02HostRestoreChild$")
	cmd.Env = []string{hostEnv + "=1", stepEnv + "=restore-child", vmEnv + "=" + vm, caIDEnv + "=" + id, certEnv + "=" + hex.EncodeToString(der)}
	out, err := cmd.CombinedOutput()
	t.Logf("second process:\n%s", out)
	if err != nil || !strings.Contains(string(out), "--- PASS: TestC02HostRestoreChild") {
		t.Fatalf("second process: %v", err)
	}
}

// TestC02HostRestoreChild is the second process of
// TestC02HostIssueRoundTrip: it restores the CA of its environment and
// signs a leaf with it.
func TestC02HostRestoreChild(t *testing.T) {
	hostStep(t, "restore-child")
	der, err := hex.DecodeString(os.Getenv(certEnv))
	if err != nil {
		t.Fatalf("%s: %v", certEnv, err)
	}
	restoreAndLeaf(t, hostAuthority(t), os.Getenv(vmEnv), os.Getenv(caIDEnv), der)
}

// TestC02HostRestoreWrong overwrites a CA's item with random bytes, and
// then with another CA's wrapped form, and sees each restore fail with
// ca-key-restore (S09-06-ca-keys, "Restore").
func TestC02HostRestoreWrong(t *testing.T) {
	hostStep(t, "")
	vmA, idA := hostCA(t)
	vmB, idB := hostCA(t)
	derA := hostIssue(t, hostAuthority(t), vmA, idA)
	hostIssue(t, hostAuthority(t), vmB, idB)
	itemA, _ := itemName(vmA, idA)
	itemB, _ := itemName(vmB, idB)
	wrappedB, err := hostStore.Fetch(t.Context(), itemB, storeitem.RoleCA)
	if err != nil {
		t.Fatalf("fetch %s: %v", itemB, err)
	}
	randomBytes, err := hex.DecodeString(random(t, len(wrappedB)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value []byte
	}{
		{"random bytes", randomBytes},
		{"another CA's wrapped form", wrappedB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := hostStore.Write(t.Context(), itemA, storeitem.RoleCA, tc.value); err != nil {
				t.Fatalf("overwrite %s: %v", itemA, err)
			}
			reply := hostSet(t, hostAuthority(t), vmA, idA, derA)
			if failed := reply.GetCaRestoreFailed(); failed == nil || failed.GetRule() != RuleKeyRestore || failed.GetCaId() != idA {
				t.Fatalf("SetCAs = %v, want CaRestoreFailed %s for %s", reply, RuleKeyRestore, idA)
			}
		})
	}
}

// TestC02HostConfined confines the process with wb-proxyd's profile after
// the store opened, then issues a CA, restores it in a new Authority,
// signs a leaf, and deletes the item, all under the profile
// (S04-03-self-sandboxing; C02-streams-and-tls, "CA key").
func TestC02HostConfined(t *testing.T) {
	hostStep(t, "confine-proxyd")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(exe), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	vm, id := hostCA(t)
	if err := selfsandbox.Confine(proxydProfile, map[string]string{"BUNDLE": bundle}); err != nil {
		t.Fatalf("confine: %v", err)
	}
	der := hostIssue(t, hostAuthority(t), vm, id)
	restoreAndLeaf(t, hostAuthority(t), vm, id, der)
}

// BenchmarkC02HostLeaf signs leaves with a CA whose key is in the Secure
// Enclave, beside BenchmarkLeaf's fake key (C02-streams-and-tls, "CA
// key").
func BenchmarkC02HostLeaf(b *testing.B) {
	hostStep(b, "bench")
	vm, id := hostCA(b)
	a := hostAuthority(b)
	der := hostIssue(b, a, vm, id)
	if reply := hostSet(b, a, vm, id, der); reply.GetCaLoaded() == nil {
		b.Fatalf("SetCAs = %v, want CaLoaded", reply)
	}
	ca := a.vms[vm].cas[id]
	for b.Loop() {
		if _, err := signLeaf(ca, "example.com", a.clock()); err != nil {
			b.Fatal(err)
		}
	}
}
