//go:build darwin && cgo && wbstorehost

package hwkey

// The host tests of the Secure Enclave key store (C02-streams-and-tls,
// "CA key"). They make Secure Enclave keys, so they run only in a test
// binary that `mise run store:host-test` signs with the team's identity
// into a bundle of its own, and starts with an empty environment but
// for WB_STORE_HOST_TEST=1 and WB_STORE_HOST_STEP. They build only with
// the wbstorehost tag, which that task sets, and without the variables
// each one skips. The only permanent key is the probe of
// TestC02HostKeyPermanentProbe, with the label
// wbtest-hwkey-permanent-probe: the probe deletes any key with that
// label before it makes one, and again after it deleted its own.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// hostEnv turns the host tests on.
	hostEnv = "WB_STORE_HOST_TEST"
	// stepEnv names the step of the run: "" for the main run, and
	// restore-child for the second process of the restore.
	stepEnv = "WB_STORE_HOST_STEP"
	// wrappedEnv is the wrapped form the second process restores, in hex.
	wrappedEnv = "WB_STORE_HOST_WRAPPED"
	// childLine starts the line the second process prints its public key
	// and signature on.
	childLine = "WB-HOST-CHILD "
	// slowSign bounds one signature. A prompt for user presence would
	// hold a signature until it is answered or times out.
	slowSign = 5 * time.Second
)

// childDigest is the digest the second process signs.
var childDigest = sha256.Sum256([]byte("wraithbox I509 restore in a second process"))

// hostStep skips the test unless the host tests are on, the run's step
// is step, and the Mac isn't a VM.
func hostStep(t *testing.T, step string) {
	t.Helper()
	if os.Getenv(hostEnv) != "1" {
		t.Skip("a host test: mise run store:host-test runs it in a team-signed bundle")
	}
	if got := os.Getenv(stepEnv); got != step {
		t.Skipf("the run's step is %q, and this test's %q", got, step)
	}
	if model, err := syscall.Sysctl("hw.model"); err != nil || strings.HasPrefix(model, "VirtualMac") {
		t.Skipf("a Mac VM: hw.model is %q (%v), and its Secure Enclave is the hypervisor's (S09-06-ca-keys)", model, err)
	}
	t.Log(">>> WATCH: no dialog and no Touch ID prompt may appear during this test")
}

// signTimed signs digest with k, fails the test unless it took less than
// slowSign, and returns the signature.
func signTimed(t *testing.T, k Key, digest []byte) []byte {
	t.Helper()
	start := time.Now()
	sig, err := k.Sign(rand.Reader, digest, crypto.SHA256)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	t.Logf("signature in %v", took)
	if took > slowSign {
		t.Errorf("the signature took %v: did a prompt appear?", took)
	}
	return sig
}

// generate makes a key and closes it when the test ends.
func generate(t *testing.T) Key {
	t.Helper()
	k, err := Open().Generate(t.Context())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	t.Cleanup(k.Close)
	t.Logf("wrapped form: %d bytes", len(k.Wrapped()))
	return k
}

// TestC02HostKeyRoundTrip generates a key, signs a digest and verifies it
// with Go, restores the key from its wrapped form in this process and in
// a second one (the test runs itself again), and verifies the second
// process's signature with the first public key (decisions 1 to 4 on
// I509).
func TestC02HostKeyRoundTrip(t *testing.T) {
	hostStep(t, "")
	k := generate(t)
	pub, ok := k.Public().(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("Public is a %T", k.Public())
	}
	digest := sha256.Sum256([]byte("wraithbox I509 first process"))
	if sig := signTimed(t, k, digest[:]); !ecdsa.VerifyASN1(pub, digest[:], sig) {
		t.Fatal("the signature doesn't verify with the public key")
	}
	same, err := Open().Restore(t.Context(), k.Wrapped())
	if err != nil {
		t.Fatalf("restore in this process: %v", err)
	}
	defer same.Close()
	if !pub.Equal(same.Public()) {
		t.Fatal("the key restored in this process has another public key")
	}
	if sig := signTimed(t, same, digest[:]); !ecdsa.VerifyASN1(pub, digest[:], sig) {
		t.Fatal("the restored key's signature doesn't verify with the first public key")
	}

	child, sig := runChild(t, k.Wrapped())
	if !pub.Equal(child) {
		t.Error("the key restored in the second process has another public key")
	}
	if !ecdsa.VerifyASN1(pub, childDigest[:], sig) {
		t.Error("the second process's signature doesn't verify with the first public key")
	}
}

// runChild runs this test binary again, as the second process of the
// restore, with nothing in its environment but the host test's
// variables, and returns the public key and the signature it printed.
func runChild(t *testing.T, wrapped []byte) (*ecdsa.PublicKey, []byte) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.v", "-test.count=1", "-test.run", "^TestC02HostKeyRestoreChild$")
	cmd.Env = []string{hostEnv + "=1", stepEnv + "=restore-child", wrappedEnv + "=" + hex.EncodeToString(wrapped)}
	out, err := cmd.CombinedOutput()
	t.Logf("second process:\n%s", out)
	if err != nil {
		t.Fatalf("second process: %v", err)
	}
	var pubHex, sigHex string
	for line := range strings.Lines(string(out)) {
		if rest, ok := strings.CutPrefix(line, childLine); ok {
			if _, err := fmt.Sscanf(rest, "public=%s signature=%s", &pubHex, &sigHex); err != nil {
				t.Fatalf("second process's line %q: %v", line, err)
			}
		}
	}
	point, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("second process's public key: %v", err)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		t.Fatalf("second process's public key: %v", err)
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) == 0 {
		t.Fatalf("second process's signature %q: %v", sigHex, err)
	}
	return pub, sig
}

// TestC02HostKeyRestoreChild is the second process of
// TestC02HostKeyRoundTrip: it restores the key of WB_STORE_HOST_WRAPPED,
// signs childDigest, and prints the public key and the signature.
func TestC02HostKeyRestoreChild(t *testing.T) {
	hostStep(t, "restore-child")
	wrapped, err := hex.DecodeString(os.Getenv(wrappedEnv))
	if err != nil {
		t.Fatalf("%s: %v", wrappedEnv, err)
	}
	k, err := Open().Restore(t.Context(), wrapped)
	if err != nil {
		t.Fatalf("restore in the second process: %v", err)
	}
	defer k.Close()
	pub, ok := k.Public().(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("Public is a %T", k.Public())
	}
	point, err := pub.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	sig := signTimed(t, k, childDigest[:])
	fmt.Println(childLine + fmt.Sprintf("public=%x signature=%x", point, sig))
}

// TestC02HostKeyAttributes reads back the attributes of a new key and of
// the key restored from it, through the shim, and sees each in the
// Secure Enclave and in no keychain, with an access
// control of kSecAttrAccessibleWhenUnlockedThisDeviceOnly whose every
// operation requires nothing: kSecAccessControlPrivateKeyUsage is its
// only flag (S09-06-ca-keys, "Attributes"; C02-streams-and-tls, "CA
// key").
func TestC02HostKeyAttributes(t *testing.T) {
	hostStep(t, "")
	k := generate(t)
	checkKeyAttributes(t, "new key", k)
	restored, err := Open().Restore(t.Context(), k.Wrapped())
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	defer restored.Close()
	checkKeyAttributes(t, "restored key", restored)
}

// checkKeyAttributes reads back the attributes of k and checks them.
func checkKeyAttributes(t *testing.T, what string, k Key) {
	t.Helper()
	a, err := readKeyAttributes(k)
	if err != nil {
		t.Fatalf("attributes of the %s: %v", what, err)
	}
	t.Logf("attributes of the %s: %+v", what, a)
	// The key isn't stored: a search of the keychain by its application
	// label finds nothing (errSecItemNotFound), and
	// TestC02HostKeyPermanentProbe sees the same search find a key that
	// is stored. kSecAttrIsPermanent isn't the check: a Secure Enclave
	// key reads it back as true whatever the call asked for, as the run of
	// 2026-11-10 measured for a new key made with false (I509). A software
	// key made with false reads it back as false.
	if a.Keychain != errSecItemNotFound {
		t.Errorf("searching the keychain for the %s: OSStatus %d, want -25300", what, a.Keychain)
	}
	if !a.SecureEnclave {
		t.Errorf("the %s isn't a Secure Enclave key", what)
	}
	if !a.HasAccessControl || !a.WhenUnlockedThisDeviceOnly {
		t.Errorf("the %s's access control %q isn't kSecAttrAccessibleWhenUnlockedThisDeviceOnly", what, a.AccessControl)
	}
	if a.Constraints == 0 || a.Unconstrained != a.Constraints {
		t.Errorf("the %s's access control requires something for %d of its %d operations: %q",
			what, a.Constraints-a.Unconstrained, a.Constraints, a.AccessControl)
	}
}

// TestC02HostKeyPermanentProbe is the positive control of the keychain
// search of TestC02HostKeyAttributes. It makes a Secure Enclave key with
// kSecAttrIsPermanent true, in the bundle's first access group, sees the
// search find it (OSStatus 0), deletes it, and sees the search find
// nothing after (-25300). The make, the read and the deletes are one
// call of the shim. Before the make it deletes by the probe's label, and
// fails when an earlier run left a probe. After its own delete it
// deletes by the label again, which finds nothing (-25300) when the
// first delete worked.
func TestC02HostKeyPermanentProbe(t *testing.T) {
	hostStep(t, "")
	p, err := probePermanent()
	t.Logf("permanent key: %+v", p)
	if err != nil {
		t.Fatalf("the permanent key: %v", err)
	}
	if p.Attributes.Keychain != 0 {
		t.Errorf("searching the keychain for the permanent key: OSStatus %d, want 0", p.Attributes.Keychain)
	}
	if p.Deleted != 0 || p.After != errSecItemNotFound {
		t.Errorf("deleting the permanent key: OSStatus %d, then the search %d; want 0, then -25300", p.Deleted, p.After)
	}
	if p.Fallback != errSecItemNotFound {
		t.Errorf("deleting by the probe's label after: OSStatus %d, want -25300", p.Fallback)
	}
}

// samePublic reports whether two keys have the same public key.
func samePublic(a, b Key) bool {
	pa, ok := a.Public().(*ecdsa.PublicKey)
	return ok && pa.Equal(b.Public())
}

// TestC02HostKeyRestoreWrong restores from random bytes and from another
// key's wrapped form, and sees each fail or give a key whose public key
// isn't the first key's (S09-06-ca-keys, "`kSecAttrTokenOID` is
// undocumented"). internal/proxyd/ca's host test sees the caller report
// ca-key-restore for both.
func TestC02HostKeyRestoreWrong(t *testing.T) {
	hostStep(t, "")
	first := generate(t)
	other := generate(t)
	random := make([]byte, len(first.Wrapped()))
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		wrapped []byte
	}{
		{"random bytes", random},
		{"another key's wrapped form", other.Wrapped()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, err := Open().Restore(t.Context(), tc.wrapped)
			if err != nil {
				t.Logf("restore failed: %v", err)
				return
			}
			defer k.Close()
			if samePublic(first, k) {
				t.Fatal("the restore gave the first key")
			}
			t.Logf("restore gave a key with another public key; the other key's: %v", samePublic(other, k))
		})
	}
}
