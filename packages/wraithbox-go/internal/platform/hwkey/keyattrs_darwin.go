//go:build darwin && cgo && wbstorehost

package hwkey

// The read-back of a key's attributes, for the host tests only
// (C02-streams-and-tls, "CA key"). It is behind the wbstorehost tag,
// which only `mise run store:host-test` sets, and no program's build
// holds it.

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// Exported by Security.framework and declared in its private headers
// only (SecAccessControlPriv.h): the protection class of an access
// control, and its constraints, a dictionary from each key operation to
// what it requires. An operation that requires nothing has true. A flag
// such as kSecAccessControlUserPresence makes the value a dictionary.
extern CFTypeRef SecAccessControlGetProtection(SecAccessControlRef access_control);
extern CFDictionaryRef SecAccessControlGetConstraints(SecAccessControlRef access_control);

// wb_ka_cstring returns a malloc'd UTF-8 copy of s, or NULL. Each cgo
// preamble is its own C file, so this one can't call the copy in
// hwkey_darwin.go.
static char *wb_ka_cstring(CFStringRef s) {
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

// What wb_key_attributes read about a key.
typedef struct {
	// status is not 0 when the attributes couldn't be read.
	int status;
	// permanent is kSecAttrIsPermanent: 0 false, 1 true, -1 absent.
	int permanent;
	// secure_enclave is 1 when kSecAttrTokenID is kSecAttrTokenIDSecureEnclave.
	int secure_enclave;
	// when_unlocked_this_device_only is 1 when the access control's
	// protection is kSecAttrAccessibleWhenUnlockedThisDeviceOnly, and
	// has_access_control 1 when the key has an access control at all.
	int has_access_control;
	int when_unlocked_this_device_only;
	// constraints counts the access control's operations, and
	// unconstrained those whose value is true.
	long constraints;
	long unconstrained;
	// access_control is the access control's description, malloc'd, or
	// NULL.
	char *access_control;
	// keychain is the OSStatus of a search of the data protection
	// keychain for a key with this key's application label: -25300 when
	// the key isn't in it.
	int keychain;
} wb_key_attrs;

// wb_count_true adds to unconstrained when a constraint's value is true.
static void wb_count_true(const void *key, const void *value, void *ctx) {
	(void)key;
	if (value != NULL && CFEqual((CFTypeRef)value, kCFBooleanTrue)) {
		(*(long *)ctx)++;
	}
}

// wb_read_access reads the access control ac into out.
static void wb_read_access(SecAccessControlRef ac, wb_key_attrs *out) {
	out->has_access_control = 1;
	CFTypeRef prot = SecAccessControlGetProtection(ac);
	out->when_unlocked_this_device_only = prot != NULL && CFEqual(prot, kSecAttrAccessibleWhenUnlockedThisDeviceOnly) ? 1 : 0;
	CFDictionaryRef c = SecAccessControlGetConstraints(ac);
	if (c != NULL && CFGetTypeID(c) == CFDictionaryGetTypeID()) {
		out->constraints = (long)CFDictionaryGetCount(c);
		CFDictionaryApplyFunction(c, wb_count_true, &out->unconstrained);
	}
	CFStringRef d = CFCopyDescription(ac);
	if (d != NULL) {
		out->access_control = wb_ka_cstring(d);
		CFRelease(d);
	}
}

// wb_search_label searches the data protection keychain, in every group
// of the process, for a key with the application label label. It asks
// for attributes only. It doesn't search the file-based keychain: a
// Secure Enclave key can be stored in the data protection keychain only.
static int wb_search_label(CFTypeRef label) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (q == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(q, kSecClass, kSecClassKey);
	CFDictionarySetValue(q, kSecAttrApplicationLabel, label);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecAttrSynchronizable, kSecAttrSynchronizableAny);
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef r = NULL;
	OSStatus st = SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (r != NULL) {
		CFRelease(r);
	}
	return (int)st;
}

// wb_key_attributes reads back the attributes of k, for the host test
// that checks what a generate and a restore set (C02-streams-and-tls,
// "CA key").
static wb_key_attrs wb_key_attributes(SecKeyRef k) {
	wb_key_attrs out = {0, -1, 0, 0, 0, 0, 0, NULL, 0};
	CFDictionaryRef a = SecKeyCopyAttributes(k);
	if (a == NULL) {
		out.status = errSecDecode;
		return out;
	}
	CFTypeRef perm = CFDictionaryGetValue(a, kSecAttrIsPermanent);
	if (perm != NULL && CFGetTypeID(perm) == CFBooleanGetTypeID()) {
		out.permanent = CFBooleanGetValue((CFBooleanRef)perm) ? 1 : 0;
	} else if (perm != NULL && CFGetTypeID(perm) == CFNumberGetTypeID()) {
		int v = 0;
		CFNumberGetValue((CFNumberRef)perm, kCFNumberIntType, &v);
		out.permanent = v != 0 ? 1 : 0;
	}
	CFTypeRef tok = CFDictionaryGetValue(a, kSecAttrTokenID);
	out.secure_enclave = tok != NULL && CFEqual(tok, kSecAttrTokenIDSecureEnclave) ? 1 : 0;
	CFTypeRef ac = CFDictionaryGetValue(a, kSecAttrAccessControl);
	if (ac != NULL && CFGetTypeID(ac) == SecAccessControlGetTypeID()) {
		wb_read_access((SecAccessControlRef)ac, &out);
	}
	CFTypeRef label = CFDictionaryGetValue(a, kSecAttrApplicationLabel);
	out.keychain = label != NULL ? wb_search_label(label) : errSecDecode;
	CFRelease(a);
	return out;
}

// WB_PROBE_LABEL is the kSecAttrLabel of the probe's permanent key.
#define WB_PROBE_LABEL CFSTR("wbtest-hwkey-permanent-probe")

// wb_delete_probe deletes every key with the probe's label from the
// data protection keychain, and returns the OSStatus: -25300 when there
// was none.
static int wb_delete_probe(void) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (q == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(q, kSecClass, kSecClassKey);
	CFDictionarySetValue(q, kSecAttrLabel, WB_PROBE_LABEL);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return (int)st;
}

// What wb_permanent_probe saw.
typedef struct {
	// before is the OSStatus of the delete by label before the probe is
	// made: -25300 when no earlier run left one.
	int before;
	// status is not 0 when the permanent key couldn't be made.
	int status;
	// attrs are the permanent key's attributes, read as for any key.
	wb_key_attrs attrs;
	// deleted is the OSStatus of its SecItemDelete, and after that of
	// the search for it once deleted: -25300 when it is gone.
	int deleted;
	int after;
	// fallback is the OSStatus of the delete by label after the probe:
	// -25300 when the delete by application label left nothing.
	int fallback;
} wb_probe;

// wb_make_probe makes the probe: a Secure Enclave key with
// kSecAttrIsPermanent true and the probe's label, in the data protection
// keychain, in the process's first access group. It sets status when
// it fails.
static SecKeyRef wb_make_probe(wb_probe *out);

// wb_permanent_probe is the positive control of the keychain search of
// wb_key_attributes. It deletes any key with the probe's label that an
// earlier run left, and makes no probe when there was one. Otherwise it
// makes the probe, reads its attributes, deletes it by its application
// label, and searches for it again. Last it deletes by the label, which
// removes the probe when the read or the delete by application label
// failed.
static wb_probe wb_permanent_probe(void) {
	wb_probe out = {0};
	out.deleted = errSecParam;
	out.after = errSecParam;
	out.fallback = errSecParam;
	out.before = wb_delete_probe();
	if (out.before != errSecItemNotFound) {
		return out;
	}
	SecKeyRef k = wb_make_probe(&out);
	if (k != NULL) {
		out.attrs = wb_key_attributes(k);
		CFDictionaryRef a = SecKeyCopyAttributes(k);
		CFTypeRef label = a != NULL ? CFDictionaryGetValue(a, kSecAttrApplicationLabel) : NULL;
		CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		if (label != NULL && q != NULL) {
			CFDictionarySetValue(q, kSecClass, kSecClassKey);
			CFDictionarySetValue(q, kSecAttrApplicationLabel, label);
			CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
			out.deleted = (int)SecItemDelete(q);
			out.after = wb_search_label(label);
		}
		if (q != NULL) {
			CFRelease(q);
		}
		if (a != NULL) {
			CFRelease(a);
		}
		CFRelease(k);
	}
	out.fallback = wb_delete_probe();
	return out;
}

static SecKeyRef wb_make_probe(wb_probe *out) {
	CFErrorRef err = NULL;
	SecAccessControlRef ac = SecAccessControlCreateWithFlags(NULL,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly, kSecAccessControlPrivateKeyUsage, &err);
	CFMutableDictionaryRef priv = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFMutableDictionaryRef p = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	if (ac == NULL || priv == NULL || p == NULL || size == NULL) {
		out->status = errSecAllocate;
	} else {
		CFDictionarySetValue(priv, kSecAttrIsPermanent, kCFBooleanTrue);
		CFDictionarySetValue(priv, kSecAttrAccessControl, ac);
		CFDictionarySetValue(priv, kSecAttrLabel, WB_PROBE_LABEL);
		CFDictionarySetValue(p, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
		CFDictionarySetValue(p, kSecAttrKeySizeInBits, size);
		CFDictionarySetValue(p, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
		CFDictionarySetValue(p, kSecUseDataProtectionKeychain, kCFBooleanTrue);
		CFDictionarySetValue(p, kSecPrivateKeyAttrs, priv);
	}
	SecKeyRef k = out->status == 0 ? SecKeyCreateRandomKey(p, &err) : NULL;
	if (out->status == 0 && k == NULL) {
		out->status = err != NULL ? (int)CFErrorGetCode(err) : errSecInternalError;
	}
	if (err != NULL) {
		CFRelease(err);
	}
	CFTypeRef refs[] = {ac, priv, p, size};
	for (size_t i = 0; i < sizeof refs / sizeof refs[0]; i++) {
		if (refs[i] != NULL) {
			CFRelease(refs[i]);
		}
	}
	return k;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// keyAttributes is what the host test reads back about a key.
type keyAttributes struct {
	// Permanent is kSecAttrIsPermanent: 0 false, 1 true, -1 absent. It
	// is logged only: a Secure Enclave key reads it back as true
	// whatever the call asked for, so Keychain is the check.
	Permanent int
	// SecureEnclave is whether kSecAttrTokenID is the Secure Enclave's.
	SecureEnclave bool
	// HasAccessControl is whether the key has an access control, and
	// WhenUnlockedThisDeviceOnly whether its protection is
	// kSecAttrAccessibleWhenUnlockedThisDeviceOnly.
	HasAccessControl           bool
	WhenUnlockedThisDeviceOnly bool
	// Constraints counts the access control's operations, and
	// Unconstrained those that require nothing: no user presence, no
	// biometry and no password.
	Constraints   int
	Unconstrained int
	// AccessControl is the access control's description, for the log.
	AccessControl string
	// Keychain is the OSStatus of a search of the data protection
	// keychain for the key: -25300 when it isn't there.
	Keychain int
}

// readKeyAttributes reads back the attributes of a key of this package,
// for the host test (C02-streams-and-tls, "CA key").
func readKeyAttributes(k Key) (keyAttributes, error) {
	ek, ok := k.(*enclaveKey)
	if !ok {
		return keyAttributes{}, fmt.Errorf("a %T isn't a Secure Enclave key", k)
	}
	ek.mu.RLock()
	defer ek.mu.RUnlock()
	if ek.closed {
		return keyAttributes{}, errors.New("the key is closed")
	}
	return goKeyAttributes(C.wb_key_attributes(ek.ref))
}

// permanentProbe is what probePermanent saw: the OSStatus of the delete
// by label before the probe, the permanent key's attributes, the
// OSStatus of its delete and of the search after it, and that of the
// delete by label after it.
type permanentProbe struct {
	Before     int
	Attributes keyAttributes
	Deleted    int
	After      int
	Fallback   int
}

// errSecItemNotFound is the OSStatus of a search or a delete that
// matched nothing.
const errSecItemNotFound = -25300

// probePermanent makes a permanent Secure Enclave key in the keychain,
// reads it back, and deletes it, as the positive control of the
// keychain search of readKeyAttributes (C02-streams-and-tls, "CA key").
// It fails when an earlier run left a probe, which it deletes first, and
// when the probe couldn't be made. Either way the delete by label after
// it has run.
func probePermanent() (permanentProbe, error) {
	r := C.wb_permanent_probe()
	if r.before != errSecItemNotFound {
		return permanentProbe{Before: int(r.before)},
			fmt.Errorf("deleting a probe an earlier run left: OSStatus %d, want %d", int(r.before), errSecItemNotFound)
	}
	if r.status != 0 {
		return permanentProbe{Before: int(r.before), Fallback: int(r.fallback)},
			fmt.Errorf("SecKeyCreateRandomKey of a permanent key: status %d, then the delete by label %d", int(r.status), int(r.fallback))
	}
	a, err := goKeyAttributes(r.attrs)
	return permanentProbe{
		Before:     int(r.before),
		Attributes: a,
		Deleted:    int(r.deleted),
		After:      int(r.after),
		Fallback:   int(r.fallback),
	}, err
}

// goKeyAttributes converts r and frees its C string.
func goKeyAttributes(r C.wb_key_attrs) (keyAttributes, error) {
	defer C.free(unsafe.Pointer(r.access_control))
	if r.status != 0 {
		return keyAttributes{}, fmt.Errorf("SecKeyCopyAttributes: status %d", int(r.status))
	}
	a := keyAttributes{
		Permanent:                  int(r.permanent),
		SecureEnclave:              r.secure_enclave != 0,
		HasAccessControl:           r.has_access_control != 0,
		WhenUnlockedThisDeviceOnly: r.when_unlocked_this_device_only != 0,
		Constraints:                int(r.constraints),
		Unconstrained:              int(r.unconstrained),
		Keychain:                   int(r.keychain),
	}
	if r.access_control != nil {
		a.AccessControl = C.GoString(r.access_control)
	}
	return a, nil
}
