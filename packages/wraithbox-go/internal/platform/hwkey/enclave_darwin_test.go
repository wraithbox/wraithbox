//go:build darwin && cgo

package hwkey

import (
	"context"
	"crypto"
	"errors"
	"strings"
	"syscall"
	"testing"
)

// The checks of the Secure Enclave key store that run before any call of
// Security.framework, so they make and use no key on the machine that
// runs them. TestNoEnclave is the exception, and calls it only in a VM.
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

// TestNoEnclave: in a macOS VM, which has no Secure Enclave, Generate
// and a Restore of 324 bytes return an error and no key (decision 5 on
// I509; S12-01-platform-interfaces, "Unimplemented platforms fail
// clearly"). CI's macOS runners are VMs, so it runs there. On a Mac
// that isn't a VM it skips, and makes no key.
func TestNoEnclave(t *testing.T) {
	model, err := syscall.Sysctl("hw.model")
	if err != nil {
		t.Fatalf("hw.model: %v", err)
	}
	if !strings.HasPrefix(model, "VirtualMac") {
		t.Skipf("hw.model is %q, not a VM, so it may have a Secure Enclave", model)
	}
	t.Logf("hw.model is %q", model)
	k, err := Open().Generate(t.Context())
	noKey(t, "Generate", k, err)
	k, err = Open().Restore(t.Context(), make([]byte, 324))
	noKey(t, "Restore", k, err)
}

// noKey fails the test unless call gave an error and no key.
func noKey(t *testing.T, call string, k Key, err error) {
	t.Helper()
	if k != nil {
		k.Close()
	}
	if k != nil || err == nil {
		t.Fatalf("%s in a VM = %v, %v; want an error and no key", call, k, err)
	}
	t.Logf("%s: %v", call, err)
}
