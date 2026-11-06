// Package hwkey is the platform interface to the hardware key store
// (S12-01-platform-interfaces, "Hardware key"; S09-06-ca-keys, "Key"):
// it makes a non-exportable P-256 signing key, and gives its wrapped
// form, which the caller keeps in the secret store item `ca/<vm>/<ca>`.
// The wrapped form restores the same key in this or a later process, on
// this host only.
//
// A Key is a crypto.Signer: it signs in the hardware, and no method
// returns the private key. Only wb-proxyd may import this package
// (S15-03-access-rules, "Declared access", the `use` of `ca/<vm>/<ca>`),
// and no program may import its fake in hwkey/hwkeytest. A test in this
// package checks both (SEC12-least-privilege).
//
// On every platform, until each backend is built, Generate and Restore
// return an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly"),
// which the caller treats as `ca-key-restore`.
package hwkey

import (
	"context"
	"crypto"
	"errors"
	"fmt"
)

// Key is one hardware key. The caller closes it when it no longer signs
// with it. A Key is safe for concurrent use.
type Key interface {
	// Public returns the key's public key, an *ecdsa.PublicKey on P-256.
	// Sign signs a digest in the hardware.
	crypto.Signer
	// Wrapped returns the key's wrapped form: the bytes that Restore
	// takes, which only this host's key store can use. On a hardware
	// store it holds no private key bytes. The caller may keep it and
	// zero its copy.
	Wrapped() []byte
	// Close releases the key's handle. Sign fails after it.
	Close()
}

// Provider makes and restores keys in the platform's hardware key store.
type Provider interface {
	// Generate makes a new non-exportable P-256 key.
	Generate(ctx context.Context) (Key, error)
	// Restore rebuilds a key from its wrapped form. The key it gives may
	// differ from the one that was wrapped, without an error, as a
	// Secure Enclave token ID passed the wrong way does
	// (S09-06-ca-keys, "`kSecAttrTokenOID` is undocumented"). So the
	// caller compares its public key with the one it expects before it
	// signs.
	Restore(ctx context.Context, wrapped []byte) (Key, error)
}

// unsupported is the Provider of a platform without a backend. what
// names the backend that isn't built.
type unsupported struct{ what string }

func (u unsupported) Generate(context.Context) (Key, error) {
	return nil, u.err()
}

func (u unsupported) Restore(context.Context, []byte) (Key, error) {
	return nil, u.err()
}

func (u unsupported) err() error {
	return fmt.Errorf("hwkey: %s isn't built: %w", u.what, errors.ErrUnsupported)
}
