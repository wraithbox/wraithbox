//go:build darwin && cgo

package hwkey

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// The Secure Enclave calls of the hardware key store (S09-06-ca-keys,
// "macOS, every build"; S10-01-libraries, the cgo shim). Nothing here
// logs: Go builds the error, and the caller logs it with its rule. A
// failed call gives its CFError's domain and code, and nothing from
// CFErrorCopyDescription, whose text is the framework's to choose and
// would reach the log (SEC12-least-privilege), as the secret store's
// shim does.

// kSecAttrTokenOID is exported by Security.framework, and the SDK
// headers don't declare it (S09-06-ca-keys, "`kSecAttrTokenOID` is
// undocumented"; X09-keychain-unsigned).
extern const CFStringRef kSecAttrTokenOID;

// What a call of the shim gives: a key or bytes, or the call that
// failed.
typedef struct {
	// key is a new reference to a key, or NULL.
	SecKeyRef key;
	// data is a new reference to the bytes a call gave, or NULL.
	CFDataRef data;
	// call names the call that failed, a static string, or NULL when
	// nothing failed.
	const char *call;
	// code is the code of the CFError of the call that failed, or -1
	// when it gave none.
	long code;
	// domain is a malloc'd copy of that error's domain, or NULL.
	char *domain;
} wb_result;

// wb_key_cstring returns a malloc'd UTF-8 copy of s, or NULL. Each cgo
// preamble is its own C file, so this is a copy of the one in
// secretstore's keychain_darwin.go.
static char *wb_key_cstring(CFStringRef s) {
	CFIndex len = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
	char *buf = malloc((size_t)len);
	if (buf == NULL) {
		return NULL;
	}
	if (!CFStringGetCString(s, buf, len, kCFStringEncodingUTF8)) {
		free(buf);
		return NULL;
	}
	return buf;
}

// wb_fail records in r that call failed with err, which may be NULL,
// and releases err.
static void wb_fail(wb_result *r, const char *call, CFErrorRef err) {
	r->call = call;
	r->code = -1;
	if (err == NULL) {
		return;
	}
	r->code = (long)CFErrorGetCode(err);
	CFStringRef d = CFErrorGetDomain(err);
	if (d != NULL) {
		r->domain = wb_key_cstring(d);
	}
	CFRelease(err);
}

// wb_key_dict returns a new mutable dictionary of CF types, or NULL.
static CFMutableDictionaryRef wb_key_dict(void) {
	return CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

// wb_p256 returns the parameters every key call shares: a P-256 key of
// the Secure Enclave token. It returns NULL when it can't allocate them.
static CFMutableDictionaryRef wb_p256(void) {
	CFMutableDictionaryRef p = wb_key_dict();
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	if (p == NULL || size == NULL) {
		if (p != NULL) {
			CFRelease(p);
		}
		if (size != NULL) {
			CFRelease(size);
		}
		return NULL;
	}
	CFDictionarySetValue(p, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(p, kSecAttrKeySizeInBits, size);
	CFDictionarySetValue(p, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFRelease(size);
	return p;
}

// wb_generate makes a new key with the attributes of S09-06-ca-keys,
// "Attributes", and no others: kSecAttrIsPermanent false, and an access
// control with kSecAttrAccessibleWhenUnlockedThisDeviceOnly and
// kSecAccessControlPrivateKeyUsage as its only flag, so no signature
// asks for user presence or biometry (SEC12-least-privilege). The key is
// in no keychain.
static wb_result wb_generate(void) {
	wb_result r = {0};
	CFErrorRef err = NULL;
	SecAccessControlRef ac = SecAccessControlCreateWithFlags(NULL,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly, kSecAccessControlPrivateKeyUsage, &err);
	if (ac == NULL) {
		wb_fail(&r, "SecAccessControlCreateWithFlags", err);
		return r;
	}
	CFMutableDictionaryRef priv = wb_key_dict();
	CFMutableDictionaryRef p = wb_p256();
	if (priv == NULL || p == NULL) {
		CFRelease(ac);
		if (priv != NULL) {
			CFRelease(priv);
		}
		if (p != NULL) {
			CFRelease(p);
		}
		wb_fail(&r, "CFDictionaryCreateMutable", NULL);
		return r;
	}
	CFDictionarySetValue(priv, kSecAttrIsPermanent, kCFBooleanFalse);
	CFDictionarySetValue(priv, kSecAttrAccessControl, ac);
	CFDictionarySetValue(p, kSecPrivateKeyAttrs, priv);
	CFRelease(ac);
	CFRelease(priv);
	r.key = SecKeyCreateRandomKey(p, &err);
	CFRelease(p);
	if (r.key == NULL) {
		wb_fail(&r, "SecKeyCreateRandomKey", err);
	}
	return r;
}

// wb_restore rebuilds the key whose token object ID is oid, as
// X09-keychain-unsigned and X23-sandboxed-daemons measured: the P-256
// Secure Enclave parameters, the ID in kSecAttrTokenOID, and
// kSecAttrIsPermanent false, so nothing is added to a keychain. The
// access control is the token object's own. An ID that names no key
// gives a new key and no error (S09-06-ca-keys), which the caller's
// public key check catches.
static wb_result wb_restore(const void *oid, long len) {
	wb_result r = {0};
	CFDataRef d = CFDataCreate(NULL, oid, (CFIndex)len);
	CFMutableDictionaryRef p = wb_p256();
	if (d == NULL || p == NULL) {
		if (d != NULL) {
			CFRelease(d);
		}
		if (p != NULL) {
			CFRelease(p);
		}
		wb_fail(&r, "CFDataCreate", NULL);
		return r;
	}
	CFDictionarySetValue(p, kSecAttrTokenOID, d);
	CFDictionarySetValue(p, kSecAttrIsPermanent, kCFBooleanFalse);
	CFRelease(d);
	CFErrorRef err = NULL;
	r.key = SecKeyCreateRandomKey(p, &err);
	CFRelease(p);
	if (r.key == NULL) {
		wb_fail(&r, "SecKeyCreateRandomKey with kSecAttrTokenOID", err);
	}
	return r;
}

// wb_token_oid returns the key's token object ID, its wrapped form. A key
// whose kSecAttrTokenID isn't the Secure Enclave's is an error, so no
// key outside the Secure Enclave is kept (SEC12-least-privilege,
// S09-06-ca-keys, "macOS, every build"). A key without a token object
// ID, or with an empty one, is an error too.
static wb_result wb_token_oid(SecKeyRef k) {
	wb_result r = {0};
	CFDictionaryRef a = SecKeyCopyAttributes(k);
	if (a == NULL) {
		wb_fail(&r, "SecKeyCopyAttributes", NULL);
		return r;
	}
	CFTypeRef tok = CFDictionaryGetValue(a, kSecAttrTokenID);
	if (tok == NULL || !CFEqual(tok, kSecAttrTokenIDSecureEnclave)) {
		wb_fail(&r, "kSecAttrTokenID of the key's attributes isn't the Secure Enclave", NULL);
		CFRelease(a);
		return r;
	}
	CFTypeRef v = CFDictionaryGetValue(a, kSecAttrTokenOID);
	if (v == NULL || CFGetTypeID(v) != CFDataGetTypeID() || CFDataGetLength((CFDataRef)v) == 0) {
		wb_fail(&r, "kSecAttrTokenOID of the key's attributes", NULL);
	} else {
		r.data = (CFDataRef)CFRetain(v);
	}
	CFRelease(a);
	return r;
}

// wb_public returns the external representation of the key's public
// key: 0x04, X and Y.
static wb_result wb_public(SecKeyRef k) {
	wb_result r = {0};
	SecKeyRef pub = SecKeyCopyPublicKey(k);
	if (pub == NULL) {
		wb_fail(&r, "SecKeyCopyPublicKey", NULL);
		return r;
	}
	CFErrorRef err = NULL;
	r.data = SecKeyCopyExternalRepresentation(pub, &err);
	CFRelease(pub);
	if (r.data == NULL) {
		wb_fail(&r, "SecKeyCopyExternalRepresentation", err);
	}
	return r;
}

// wb_sign signs a SHA-256 digest in the Secure Enclave and returns the
// DER signature, as crypto/x509 takes it.
static wb_result wb_sign(SecKeyRef k, const void *digest, long len) {
	wb_result r = {0};
	CFDataRef in = CFDataCreate(NULL, digest, (CFIndex)len);
	if (in == NULL) {
		wb_fail(&r, "CFDataCreate", NULL);
		return r;
	}
	CFErrorRef err = NULL;
	r.data = SecKeyCreateSignature(k, kSecKeyAlgorithmECDSASignatureDigestX962SHA256, in, &err);
	CFRelease(in);
	if (r.data == NULL) {
		wb_fail(&r, "SecKeyCreateSignature", err);
	}
	return r;
}

static void wb_release_key(SecKeyRef k) {
	if (k != NULL) {
		CFRelease(k);
	}
}
*/
import "C"

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"unsafe"
)

// maxWrapped bounds the wrapped form Restore takes, and every result
// the shim reads back: a token object ID, a public key and a signature.
// A token object ID was 324 bytes in X09-keychain-unsigned and in the
// attended run of I509.
const maxWrapped = 4096

// Open returns the hardware key store of darwin: a Secure Enclave key
// that isn't stored in the Keychain, whose wrapped form is its token
// object ID (S09-06-ca-keys, "macOS, every build"). Without a Secure
// Enclave, Generate and Restore return the error of Security.framework.
// On a Mac VM the Secure Enclave is the hypervisor's virtual one, and
// its keys are taken like any other.
func Open() Provider { return enclave{} }

// enclave is the Secure Enclave key store.
type enclave struct{}

// Generate makes a new key in the Secure Enclave, with the attributes of
// S09-06-ca-keys, "Attributes".
func (enclave) Generate(ctx context.Context) (Key, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newKey("generate", C.wb_generate())
}

// Restore rebuilds a key from its token object ID. It refuses an empty
// wrapped form and one longer than maxWrapped before it calls the
// Secure Enclave. The key it gives may be a new one, and the caller
// compares its public key (S09-06-ca-keys, "Restore"). That public key
// is read from the token object ID's bytes, so Restore also signs a
// fixed digest with the key and fails unless the signature verifies
// with it (S09-06-ca-keys, "`kSecAttrTokenOID` is undocumented").
func (enclave) Restore(ctx context.Context, wrapped []byte) (Key, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(wrapped) == 0 {
		return nil, errors.New("hwkey: restore: the wrapped form is empty")
	}
	if len(wrapped) > maxWrapped {
		return nil, fmt.Errorf("hwkey: restore: the wrapped form is %d bytes, more than %d", len(wrapped), maxWrapped)
	}
	k, err := newKey("restore", C.wb_restore(unsafe.Pointer(&wrapped[0]), C.long(len(wrapped))))
	if err != nil {
		return nil, err
	}
	if err := checkPossession(k); err != nil {
		k.Close()
		return nil, err
	}
	return k, nil
}

// possessionDigest is what checkPossession signs.
var possessionDigest = sha256.Sum256([]byte("wraithbox hwkey restore check"))

// checkPossession signs possessionDigest with k and returns an error
// unless the signature verifies with k's public key: the key that signs
// is the key of that public key.
func checkPossession(k Key) error {
	pub, ok := k.Public().(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("hwkey: restore: the public key is a %T", k.Public())
	}
	sig, err := k.Sign(nil, possessionDigest[:], crypto.SHA256)
	if err != nil {
		return fmt.Errorf("hwkey: restore: the check signature: %w", err)
	}
	if !ecdsa.VerifyASN1(pub, possessionDigest[:], sig) {
		return errors.New("hwkey: restore: the key's signature doesn't verify with its public key")
	}
	return nil
}

// newKey makes a Key of the result of a generate or a restore: it reads
// the key's token object ID and its public key, and releases the key when
// either fails.
func newKey(what string, r C.wb_result) (Key, error) {
	if r.call != nil {
		return nil, resultErr(what, r)
	}
	wrapped, err := dataBytes(what, C.wb_token_oid(r.key))
	if err != nil {
		C.wb_release_key(r.key)
		return nil, err
	}
	point, err := dataBytes(what, C.wb_public(r.key))
	if err != nil {
		C.wb_release_key(r.key)
		return nil, err
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		C.wb_release_key(r.key)
		return nil, fmt.Errorf("hwkey: %s: the public key isn't a P-256 point: %w", what, err)
	}
	return &enclaveKey{ref: r.key, pub: pub, wrapped: wrapped}, nil
}

// resultErr is the error of a failed shim call, with the error domain
// and code Security.framework gave, and no description. It frees the
// domain.
func resultErr(what string, r C.wb_result) error {
	defer C.free(unsafe.Pointer(r.domain))
	domain := "no error"
	if r.domain != nil {
		domain = C.GoString(r.domain)
	}
	return fmt.Errorf("hwkey: %s: %s: %s %d", what, C.GoString(r.call), domain, int(r.code))
}

// dataBytes copies the bytes of a shim call's result and releases them,
// or returns the call's error. It refuses more than maxWrapped bytes.
func dataBytes(what string, r C.wb_result) ([]byte, error) {
	if r.call != nil {
		return nil, resultErr(what, r)
	}
	defer C.CFRelease(C.CFTypeRef(r.data))
	n := C.CFDataGetLength(r.data)
	if n > maxWrapped {
		return nil, fmt.Errorf("hwkey: %s: the framework gave %d bytes, more than %d", what, int64(n), maxWrapped)
	}
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(r.data)), C.int(n)), nil
}

// errClosed is the error of a signature with a closed key.
var errClosed = errors.New("hwkey: sign with a closed key")

// enclaveKey is one Secure Enclave key. Its private key never leaves the
// Secure Enclave (S15-03-access-rules, rule 2): the process holds a
// reference to it, its public key and its token object ID.
type enclaveKey struct {
	pub     *ecdsa.PublicKey
	wrapped []byte

	// mu guards ref: signatures hold it to read, and Close to write.
	mu  sync.RWMutex
	ref C.SecKeyRef
	// closed is set by Close, after it released ref.
	closed bool
}

func (k *enclaveKey) Public() crypto.PublicKey { return k.pub }

// Sign signs a SHA-256 digest with kSecKeyAlgorithmECDSASignatureDigestX962SHA256,
// and returns the DER signature. It refuses any other hash, and a digest
// that isn't 32 bytes. The access control asks for no user presence, so
// no signature shows a prompt (S09-06-ca-keys, "Attributes").
func (k *enclaveKey) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts == nil || opts.HashFunc() != crypto.SHA256 {
		return nil, errors.New("hwkey: the Secure Enclave key signs SHA-256 digests only")
	}
	if len(digest) != sha256.Size {
		return nil, fmt.Errorf("hwkey: the digest is %d bytes, not %d", len(digest), sha256.Size)
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.closed {
		return nil, errClosed
	}
	return dataBytes("sign", C.wb_sign(k.ref, unsafe.Pointer(&digest[0]), C.long(len(digest))))
}

func (k *enclaveKey) Wrapped() []byte { return slices.Clone(k.wrapped) }

func (k *enclaveKey) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		k.closed = true
		C.wb_release_key(k.ref)
	}
}
