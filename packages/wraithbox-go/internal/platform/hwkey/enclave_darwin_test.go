//go:build darwin && cgo

package hwkey

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// The checks of the Secure Enclave key store that run before any call of
// Security.framework, so they make and use no key on the machine that
// runs them. TestC02VMRoundTrip is the exception, and calls it only in a
// Mac VM.
// The host tests in host_darwin_test.go make the real calls.

// TestRestoreRefusesSize: Restore refuses an empty wrapped form and one
// longer than maxWrapped, and a canceled context, with no key
// (decision 3 on I509).
func TestRestoreRefusesSize(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		wrapped []byte
	}{
		{"nil", t.Context(), nil},
		{"empty", t.Context(), []byte{}},
		{"over the cap", t.Context(), make([]byte, maxWrapped+1)},
		{"canceled", canceled, make([]byte, 324)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, err := Open().Restore(tc.ctx, tc.wrapped)
			if k != nil || err == nil {
				t.Fatalf("Restore = %v, %v; want an error and no key", k, err)
			}
			t.Log(err)
		})
	}
}

// TestGenerateCanceled: Generate with a canceled context makes no key.
func TestGenerateCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if k, err := Open().Generate(ctx); k != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate = %v, %v; want context.Canceled", k, err)
	}
}

// TestSignRefuses: Sign refuses a hash other than SHA-256, no options, a
// digest that isn't 32 bytes, and a closed key, before it calls the
// Secure Enclave (decision 4 on I509).
func TestSignRefuses(t *testing.T) {
	digest := make([]byte, 32)
	for _, tc := range []struct {
		name   string
		closed bool
		digest []byte
		opts   crypto.SignerOpts
	}{
		{"SHA-384", false, make([]byte, 48), crypto.SHA384},
		{"SHA-1", false, make([]byte, 20), crypto.SHA1},
		{"no hash", false, digest, crypto.Hash(0)},
		{"no options", false, digest, nil},
		{"short digest", false, make([]byte, 31), crypto.SHA256},
		{"closed", true, digest, crypto.SHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &enclaveKey{closed: tc.closed}
			sig, err := k.Sign(nil, tc.digest, tc.opts)
			if sig != nil || err == nil {
				t.Fatalf("Sign = %x, %v; want an error", sig, err)
			}
			if tc.closed && !errors.Is(err, errClosed) {
				t.Errorf("Sign = %v, want errClosed", err)
			}
		})
	}
}

// TestC02VMRoundTrip runs the real shim on a Mac VM, as CI's macOS
// runners are (decision 5 on I509, as replaced on 2026-11-10). The VM's
// Secure Enclave token is the hypervisor's virtual one, and it gives
// working keys to an unsigned test binary. The test generates a key,
// signs a SHA-256 digest and verifies it with Go, restores the key from
// its wrapped form, and sees changed bytes fail or give another key,
// and a closed key refuse to sign. It makes no Keychain call and stores
// nothing: no key is permanent. On a Mac that isn't a VM it skips, so
// it makes no key on a developer's Secure Enclave.
func TestC02VMRoundTrip(t *testing.T) {
	vmOnly(t)
	k, err := Open().Generate(t.Context())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer k.Close()
	// Generate returned the key, so wb_token_oid saw kSecAttrTokenID be
	// kSecAttrTokenIDSecureEnclave, and a token object ID.
	wrapped := k.Wrapped()
	t.Logf("wrapped form: %d bytes", len(wrapped))
	if len(wrapped) == 0 || len(wrapped) > maxWrapped {
		t.Fatalf("the wrapped form is %d bytes, want 1 to %d", len(wrapped), maxWrapped)
	}
	pub, ok := k.Public().(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Fatalf("Public is a %T, want a P-256 *ecdsa.PublicKey", k.Public())
	}
	digest := sha256.Sum256([]byte("wraithbox I509 CI round trip"))
	verifySign(t, "the new key", k, pub, digest[:])

	restored, err := Open().Restore(t.Context(), wrapped)
	if err != nil {
		t.Fatalf("Restore of the wrapped form: %v", err)
	}
	defer restored.Close()
	if !pub.Equal(restored.Public()) {
		t.Fatal("the restored key has another public key")
	}
	verifySign(t, "the restored key", restored, pub, digest[:])

	restoreChanged(t, pub, wrapped)

	restored.Close()
	if sig, err := restored.Sign(rand.Reader, digest[:], crypto.SHA256); sig != nil || !errors.Is(err, errClosed) {
		t.Fatalf("Sign after Close = %x, %v; want errClosed", sig, err)
	}
}

// restoreChanged restores wrapped with one byte changed, and fails the
// test when that gives the key of pub.
func restoreChanged(t *testing.T, pub *ecdsa.PublicKey, wrapped []byte) {
	t.Helper()
	changed := slices.Clone(wrapped)
	changed[len(changed)/2] ^= 0xff
	other, err := Open().Restore(t.Context(), changed)
	if err != nil {
		t.Logf("Restore of changed bytes: %v", err)
		return
	}
	defer other.Close()
	if pub.Equal(other.Public()) {
		t.Fatal("Restore of changed bytes gave the first key")
	}
	t.Log("Restore of changed bytes gave a key with another public key")
}

// vmOnly skips the test unless hw.model names a Mac VM, and fails it
// when hw.model can't be read.
func vmOnly(t *testing.T) {
	t.Helper()
	model, err := syscall.Sysctl("hw.model")
	if err != nil {
		t.Fatalf("hw.model: %v", err)
	}
	if !strings.HasPrefix(model, "VirtualMac") {
		t.Skipf("hw.model is %q, not a VM: this test makes no key on a real Secure Enclave", model)
	}
	t.Logf("hw.model is %q", model)
}

// verifySign signs digest with k and fails the test unless the
// signature verifies with pub.
func verifySign(t *testing.T, what string, k Key, pub *ecdsa.PublicKey, digest []byte) {
	t.Helper()
	sig, err := k.Sign(rand.Reader, digest, crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign with %s: %v", what, err)
	}
	if !ecdsa.VerifyASN1(pub, digest, sig) {
		t.Fatalf("the signature of %s doesn't verify with the public key", what)
	}
}
