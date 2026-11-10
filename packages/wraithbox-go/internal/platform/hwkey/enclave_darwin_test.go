//go:build darwin && cgo

package hwkey

import (
	"context"
	"crypto"
	"errors"
	"testing"
)

// The checks of the Secure Enclave key store that run before any call of
// Security.framework, so they make and use no key on the machine that
// runs them. The host tests in host_darwin_test.go make the real calls.

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
