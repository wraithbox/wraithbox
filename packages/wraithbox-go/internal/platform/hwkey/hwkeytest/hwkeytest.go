// Package hwkeytest holds a software stand-in for the hardware key
// store, for tests. Its keys are software P-256 keys, and the wrapped
// form of each is the key's own PKCS #8 encoding, so the fake holds and
// gives private key bytes as no hardware store does. It can be told to
// fail a generate or a restore, or to restore a different key, as a
// Secure Enclave token ID passed the wrong way does (S09-06-ca-keys,
// "`kSecAttrTokenOID` is undocumented"). No program under cmd/ may
// depend on it (hwkey's TestKeyImporters).
package hwkeytest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
)

// Fake is the software key store. The zero Fake works. Its methods are
// safe for concurrent use.
type Fake struct {
	mu           sync.Mutex
	generateErr  error
	restoreErr   error
	restoreOther bool
	generated    int
	restored     int
	open         int
}

// FailGenerate makes every later Generate return err, or succeed again
// when err is nil.
func (f *Fake) FailGenerate(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generateErr = err
}

// FailRestore makes every later Restore return err, or succeed again
// when err is nil.
func (f *Fake) FailRestore(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreErr = err
}

// RestoreOther makes every later Restore of a valid wrapped form return
// a new key, not the wrapped one, without an error.
func (f *Fake) RestoreOther(other bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreOther = other
}

// Counts returns how many keys Generate and Restore gave, and how many
// of all of them are not closed yet.
func (f *Fake) Counts() (generated, restored, open int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generated, f.restored, f.open
}

// Generate implements hwkey.Provider.
func (f *Fake) Generate(context.Context) (hwkey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.generateErr != nil {
		return nil, f.generateErr
	}
	k, err := f.newKey()
	if err != nil {
		return nil, err
	}
	f.generated++
	return k, nil
}

// Restore implements hwkey.Provider. A wrapped form that isn't the
// PKCS #8 encoding of a P-256 key is an error.
func (f *Fake) Restore(_ context.Context, wrapped []byte) (hwkey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restoreErr != nil {
		return nil, f.restoreErr
	}
	parsed, err := x509.ParsePKCS8PrivateKey(wrapped)
	if err != nil {
		return nil, fmt.Errorf("hwkeytest: the wrapped form doesn't restore: %w", err)
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, errors.New("hwkeytest: the wrapped form isn't a P-256 key")
	}
	var k *key
	if f.restoreOther {
		k, err = f.newKey()
		if err != nil {
			return nil, err
		}
	} else {
		k = &key{fake: f, priv: priv, wrapped: slices.Clone(wrapped)}
		f.open++
	}
	f.restored++
	return k, nil
}

// newKey makes a software key. The caller holds f.mu.
func (f *Fake) newKey() (*key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("hwkeytest: %w", err)
	}
	wrapped, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("hwkeytest: %w", err)
	}
	f.open++
	return &key{fake: f, priv: priv, wrapped: wrapped}, nil
}

// key is one software key.
type key struct {
	fake    *Fake
	priv    *ecdsa.PrivateKey
	wrapped []byte

	mu     sync.Mutex
	closed bool
}

func (k *key) Public() crypto.PublicKey { return k.priv.Public() }

func (k *key) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	k.mu.Lock()
	closed := k.closed
	k.mu.Unlock()
	if closed {
		return nil, errors.New("hwkeytest: sign with a closed key")
	}
	return k.priv.Sign(r, digest, opts)
}

func (k *key) Wrapped() []byte { return slices.Clone(k.wrapped) }

func (k *key) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	k.fake.mu.Lock()
	k.fake.open--
	k.fake.mu.Unlock()
}
