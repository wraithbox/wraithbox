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
// for attributes only.
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
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// keyAttributes is what the host test reads back about a key.
type keyAttributes struct {
	// Permanent is kSecAttrIsPermanent: 0 false, 1 true, -1 absent.
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
	r := C.wb_key_attributes(ek.ref)
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
