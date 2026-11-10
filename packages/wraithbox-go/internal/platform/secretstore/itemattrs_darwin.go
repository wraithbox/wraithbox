//go:build darwin && cgo && wbstorehost

package secretstore

// The read-back of an item's attributes, for the host tests only
// (C05-credentials-and-store, "Host tests"). It matches an item in any
// of the process's groups, synchronizable or not, so it is behind the
// wbstorehost tag, which only `mise run store:host-test` sets, and no
// program's build holds it.

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// wb_attrs_cstring returns a malloc'd UTF-8 copy of s, or NULL. Each cgo
// preamble is its own C file, so this one can't call the copies of
// keychain_darwin.go and items_darwin.go.
static char *wb_attrs_cstring(CFStringRef s) {
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

// What wb_item_attributes found for the host test: how many items of the
// name the process reaches in any of its groups and accounts, and for
// the first one its group, its account, whether its protection class is the one of wb_value_attrs,
// and its synchronizable attribute: 0 false, 1 true, -1 absent.
typedef struct {
	int status;
	long count;
	char *group;
	char *account;
	char *accessible;
	int when_unlocked_this_device_only;
	int synchronizable;
} wb_attrs;

// wb_read_attrs fills out from the attribute dictionary a.
static void wb_read_attrs(CFDictionaryRef a, wb_attrs *out) {
	CFTypeRef g = CFDictionaryGetValue(a, kSecAttrAccessGroup);
	if (g != NULL && CFGetTypeID(g) == CFStringGetTypeID()) {
		out->group = wb_attrs_cstring((CFStringRef)g);
	}
	CFTypeRef acct = CFDictionaryGetValue(a, kSecAttrAccount);
	if (acct != NULL && CFGetTypeID(acct) == CFStringGetTypeID()) {
		out->account = wb_attrs_cstring((CFStringRef)acct);
	}
	CFTypeRef c = CFDictionaryGetValue(a, kSecAttrAccessible);
	if (c != NULL && CFGetTypeID(c) == CFStringGetTypeID()) {
		out->accessible = wb_attrs_cstring((CFStringRef)c);
		out->when_unlocked_this_device_only = CFEqual(c, kSecAttrAccessibleWhenUnlockedThisDeviceOnly) ? 1 : 0;
	}
	CFTypeRef s = CFDictionaryGetValue(a, kSecAttrSynchronizable);
	out->synchronizable = -1;
	if (s != NULL && CFGetTypeID(s) == CFBooleanGetTypeID()) {
		out->synchronizable = CFBooleanGetValue((CFBooleanRef)s) ? 1 : 0;
	} else if (s != NULL && CFGetTypeID(s) == CFNumberGetTypeID()) {
		int v = 0;
		CFNumberGetValue((CFNumberRef)s, kCFNumberIntType, &v);
		out->synchronizable = v != 0 ? 1 : 0;
	}
}

// wb_item_attributes reads back the attributes of item, in whatever group
// and whether it synchronizes or not, for the host test that checks what
// a write set (I505). It asks for attributes only, never data.
static wb_attrs wb_item_attributes(const char *item) {
	wb_attrs out = {0, 0, NULL, NULL, NULL, 0, -1};
	CFStringRef service = CFStringCreateWithCString(NULL, item, kCFStringEncodingUTF8);
	if (service == NULL) {
		out.status = errSecParam;
		return out;
	}
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (q == NULL) {
		CFRelease(service);
		out.status = errSecAllocate;
		return out;
	}
	CFDictionarySetValue(q, kSecAttrService, service);
	CFRelease(service);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecAttrSynchronizable, kSecAttrSynchronizableAny);
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef r = NULL;
	out.status = (int)SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (out.status != errSecSuccess) {
		if (r != NULL) {
			CFRelease(r);
		}
		return out;
	}
	if (r == NULL || CFGetTypeID(r) != CFArrayGetTypeID()) {
		if (r != NULL) {
			CFRelease(r);
		}
		out.status = errSecDecode;
		return out;
	}
	out.count = (long)CFArrayGetCount((CFArrayRef)r);
	CFTypeRef first = out.count > 0 ? CFArrayGetValueAtIndex((CFArrayRef)r, 0) : NULL;
	if (first != NULL && CFGetTypeID(first) == CFDictionaryGetTypeID()) {
		wb_read_attrs((CFDictionaryRef)first, &out);
	}
	CFRelease(r);
	return out;
}
*/
import "C"

import "unsafe"

// itemAttributes is what the host test reads back about an item.
type itemAttributes struct {
	// Count is how many items of the name the process reaches.
	Count int
	// Group is the access group of the first one, and Account its
	// account.
	Group   string
	Account string
	// Accessible is its protection class, as the Keychain names it, and
	// WhenUnlockedThisDeviceOnly whether that is
	// kSecAttrAccessibleWhenUnlockedThisDeviceOnly.
	Accessible                 string
	WhenUnlockedThisDeviceOnly bool
	// Synchronizable is 0 for false, 1 for true, -1 when absent.
	Synchronizable int
}

// readItemAttributes reads back the attributes of item, in any of the
// process's groups, for the host test (C05-credentials-and-store). It
// never reads a value.
func readItemAttributes(item string) (itemAttributes, Status) {
	i := C.CString(item)
	defer C.free(unsafe.Pointer(i))
	r := C.wb_item_attributes(i)
	defer C.free(unsafe.Pointer(r.group))
	defer C.free(unsafe.Pointer(r.account))
	defer C.free(unsafe.Pointer(r.accessible))
	if r.status != 0 {
		return itemAttributes{}, Status(r.status)
	}
	a := itemAttributes{
		Count:                      int(r.count),
		WhenUnlockedThisDeviceOnly: r.when_unlocked_this_device_only != 0,
		Synchronizable:             int(r.synchronizable),
	}
	if r.group != nil {
		a.Group = C.GoString(r.group)
	}
	if r.account != nil {
		a.Account = C.GoString(r.account)
	}
	if r.accessible != nil {
		a.Accessible = C.GoString(r.accessible)
	}
	return a, StatusOK
}
