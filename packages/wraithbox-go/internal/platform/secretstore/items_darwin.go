//go:build darwin && cgo

package secretstore

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <limits.h>
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// The item calls of the Keychain backend (S15-02-secret-store,
// "Keychain"). Each returns the OSStatus and nothing from
// SecCopyErrorMessageString, whose text can name an item's attributes:
// Go builds the log line ("Failed store calls"). Nothing here logs.

// wb_item_cstring returns a malloc'd UTF-8 copy of s, or NULL. Each cgo
// preamble is its own C file, so this one can't call wb_cstring of
// keychain_darwin.go.
static char *wb_item_cstring(CFStringRef s) {
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

// wb_dict returns a new mutable dictionary of CF types, or NULL.
static CFMutableDictionaryRef wb_dict(void) {
	return CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

// wb_set_string sets key to the UTF-8 string s, and returns 0, or 1 when
// s doesn't convert.
static int wb_set_string(CFMutableDictionaryRef d, CFStringRef key, const char *s) {
	CFStringRef v = CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
	if (v == NULL) {
		return 1;
	}
	CFDictionarySetValue(d, key, v);
	CFRelease(v);
	return 0;
}

// wb_query returns a new query for the generic password item in group,
// or for every item of the group when item is NULL, or NULL when it
// can't be made. Every query names the data protection keychain, so
// SecKeychainSetUserInteractionAllowed, which covers only the file-based
// keychains, isn't what keeps a call from asking (S15-03-access-rules,
// "No Keychain dialogs"). It names the group, which the checks chose
// (S15-02-secret-store, "The group of each call"), and matches only
// items that never synchronize (G4, S15-02-secret-store, "Backend
// guarantees").
static CFMutableDictionaryRef wb_query(const char *group, const char *item) {
	CFMutableDictionaryRef q = wb_dict();
	if (q == NULL) {
		return NULL;
	}
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecAttrSynchronizable, kCFBooleanFalse);
	if (wb_set_string(q, kSecAttrAccessGroup, group) != 0 ||
			(item != NULL && wb_set_string(q, kSecAttrService, item) != 0)) {
		CFRelease(q);
		return NULL;
	}
	return q;
}

// wb_zero_release zeroes the bytes of d, a copy of a value, and releases
// it.
static void wb_zero_release(CFMutableDataRef d) {
	memset(CFDataGetMutableBytePtr(d), 0, (size_t)CFDataGetLength(d));
	CFRelease(d);
}

// wb_value_attrs returns the attributes a write sets: the value, and the
// protection class of every item, which keeps it on this Mac and
// readable only while the Mac is unlocked (decision 1 on I505;
// S15-02-secret-store, "Keychain"). NULL when it can't be made.
static CFMutableDictionaryRef wb_value_attrs(CFDataRef value) {
	CFMutableDictionaryRef a = wb_dict();
	if (a == NULL) {
		return NULL;
	}
	CFDictionarySetValue(a, kSecValueData, value);
	CFDictionarySetValue(a, kSecAttrAccessible, kSecAttrAccessibleWhenUnlockedThisDeviceOnly);
	return a;
}

// wb_add_or_update adds the item of q with the attributes a, or updates
// them in place when it exists, so there is no moment without the item.
static OSStatus wb_add_or_update(CFDictionaryRef q, CFDictionaryRef a) {
	CFMutableDictionaryRef add = CFDictionaryCreateMutableCopy(NULL, 0, q);
	if (add == NULL) {
		return errSecAllocate;
	}
	CFIndex n = CFDictionaryGetCount(a);
	const void *keys[2];
	const void *values[2];
	CFDictionaryGetKeysAndValues(a, keys, values);
	for (CFIndex i = 0; i < n && i < 2; i++) {
		CFDictionarySetValue(add, keys[i], values[i]);
	}
	OSStatus st = SecItemAdd(add, NULL);
	CFRelease(add);
	if (st == errSecDuplicateItem) {
		st = SecItemUpdate(q, a);
	}
	return st;
}

// wb_write stores the len bytes at value as item in group. The copy of
// the value it makes is zeroed before it is released.
static int wb_write(const char *group, const char *item, const void *value, long len) {
	CFMutableDictionaryRef q = wb_query(group, item);
	if (q == NULL) {
		return errSecParam;
	}
	CFMutableDataRef data = CFDataCreateMutable(NULL, 0);
	if (data == NULL) {
		CFRelease(q);
		return errSecAllocate;
	}
	CFDataAppendBytes(data, (const UInt8 *)value, (CFIndex)len);
	CFMutableDictionaryRef a = wb_value_attrs(data);
	OSStatus st = errSecAllocate;
	if (a != NULL) {
		st = wb_add_or_update(q, a);
		CFRelease(a);
	}
	CFRelease(q);
	wb_zero_release(data);
	return (int)st;
}

// What wb_fetch found: the status, and on success the value, which the
// caller copies and releases.
typedef struct {
	int status;
	CFDataRef data;
} wb_fetched;

// wb_fetch reads the value of item in group. A result that isn't data,
// or is longer than an int, is errSecDecode.
static wb_fetched wb_fetch(const char *group, const char *item) {
	wb_fetched out = {0, NULL};
	CFMutableDictionaryRef q = wb_query(group, item);
	if (q == NULL) {
		out.status = errSecParam;
		return out;
	}
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef r = NULL;
	out.status = (int)SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (out.status != errSecSuccess) {
		if (r != NULL) {
			CFRelease(r);
		}
		return out;
	}
	if (r == NULL || CFGetTypeID(r) != CFDataGetTypeID() || CFDataGetLength((CFDataRef)r) > INT_MAX) {
		if (r != NULL) {
			CFRelease(r);
		}
		out.status = errSecDecode;
		return out;
	}
	out.data = (CFDataRef)r;
	return out;
}

// wb_delete removes item from group.
static int wb_delete(const char *group, const char *item) {
	CFMutableDictionaryRef q = wb_query(group, item);
	if (q == NULL) {
		return errSecParam;
	}
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return (int)st;
}

// What wb_list found: the status, and on success the count of items and
// their names, one per line, or NULL for none.
typedef struct {
	int status;
	long count;
	char *names;
} wb_listed;

// wb_append_name appends the service attribute of the item attributes a
// to joined. It returns 1 when the item has no service, or one that
// isn't a string or holds a newline.
static int wb_append_name(CFMutableStringRef joined, CFTypeRef a, CFIndex i) {
	if (a == NULL || CFGetTypeID(a) != CFDictionaryGetTypeID()) {
		return 1;
	}
	CFTypeRef s = CFDictionaryGetValue((CFDictionaryRef)a, kSecAttrService);
	if (s == NULL || CFGetTypeID(s) != CFStringGetTypeID() ||
			CFStringFind((CFStringRef)s, CFSTR("\n"), 0).location != kCFNotFound) {
		return 1;
	}
	if (i > 0) {
		CFStringAppend(joined, CFSTR("\n"));
	}
	CFStringAppend(joined, (CFStringRef)s);
	return 0;
}

// wb_join_names joins the names of the items in r, an array of attribute
// dictionaries, into out. Anything else is errSecDecode.
static void wb_join_names(CFTypeRef r, wb_listed *out) {
	if (CFGetTypeID(r) != CFArrayGetTypeID()) {
		out->status = errSecDecode;
		return;
	}
	CFMutableStringRef joined = CFStringCreateMutable(NULL, 0);
	if (joined == NULL) {
		out->status = errSecAllocate;
		return;
	}
	CFIndex n = CFArrayGetCount((CFArrayRef)r);
	for (CFIndex i = 0; i < n; i++) {
		if (wb_append_name(joined, CFArrayGetValueAtIndex((CFArrayRef)r, i), i) != 0) {
			out->status = errSecDecode;
			CFRelease(joined);
			return;
		}
	}
	out->count = (long)n;
	if (n > 0) {
		out->names = wb_item_cstring(joined);
		if (out->names == NULL) {
			out->status = errSecDecode;
		}
	}
	CFRelease(joined);
}

// wb_list returns the names of the items in group. It asks for their
// attributes only, never their data. A group with no item is no names.
static wb_listed wb_list(const char *group) {
	wb_listed out = {0, 0, NULL};
	CFMutableDictionaryRef q = wb_query(group, NULL);
	if (q == NULL) {
		out.status = errSecParam;
		return out;
	}
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef r = NULL;
	OSStatus st = SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (st == errSecItemNotFound) {
		return out;
	}
	if (st != errSecSuccess || r == NULL) {
		out.status = st != errSecSuccess ? (int)st : errSecDecode;
		if (r != NULL) {
			CFRelease(r);
		}
		return out;
	}
	wb_join_names(r, &out);
	CFRelease(r);
	return out;
}

// What wb_item_attributes found for the host test: how many items of the
// name the process reaches in any of its groups, and for the first one
// its group, whether its protection class is the one of wb_value_attrs,
// and its synchronizable attribute: 0 false, 1 true, -1 absent.
typedef struct {
	int status;
	long count;
	char *group;
	char *accessible;
	int when_unlocked_this_device_only;
	int synchronizable;
} wb_attrs;

// wb_read_attrs fills out from the attribute dictionary a.
static void wb_read_attrs(CFDictionaryRef a, wb_attrs *out) {
	CFTypeRef g = CFDictionaryGetValue(a, kSecAttrAccessGroup);
	if (g != NULL && CFGetTypeID(g) == CFStringGetTypeID()) {
		out->group = wb_item_cstring((CFStringRef)g);
	}
	CFTypeRef c = CFDictionaryGetValue(a, kSecAttrAccessible);
	if (c != NULL && CFGetTypeID(c) == CFStringGetTypeID()) {
		out->accessible = wb_item_cstring((CFStringRef)c);
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
	wb_attrs out = {0, 0, NULL, NULL, 0, -1};
	CFMutableDictionaryRef q = wb_dict();
	if (q == NULL || wb_set_string(q, kSecAttrService, item) != 0) {
		if (q != NULL) {
			CFRelease(q);
		}
		out.status = errSecParam;
		return out;
	}
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecAttrSynchronizable, kSecAttrSynchronizableAny);
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef r = NULL;
	out.status = (int)SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (out.status != errSecSuccess || r == NULL || CFGetTypeID(r) != CFArrayGetTypeID()) {
		if (r != NULL) {
			CFRelease(r);
			out.status = errSecDecode;
		}
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

import (
	"context"
	"strings"
	"unsafe"
)

// cStrings returns C copies of group and item, and the function that
// frees them.
func cStrings(group, item string) (*C.char, *C.char, func()) {
	g, i := C.CString(group), C.CString(item)
	return g, i, func() {
		C.free(unsafe.Pointer(g))
		C.free(unsafe.Pointer(i))
	}
}

// Write stores value as item in group: SecItemAdd, and SecItemUpdate of
// the value when the item exists. The value crosses into C once, and the
// shim zeroes its copy.
func (k *keychain) Write(_ context.Context, group, item string, value []byte) Status {
	g, i, free := cStrings(group, item)
	defer free()
	var p unsafe.Pointer
	if len(value) > 0 {
		p = unsafe.Pointer(&value[0])
	}
	return Status(C.wb_write(g, i, p, C.long(len(value))))
}

// Fetch returns the value of item in group. The value is copied into Go
// memory once and the CFData is released at once. The caller zeroes the
// copy when its work ends (S15-03-access-rules, rule 3).
func (k *keychain) Fetch(_ context.Context, group, item string) ([]byte, Status) {
	g, i, free := cStrings(group, item)
	defer free()
	r := C.wb_fetch(g, i)
	if r.status != 0 {
		return nil, Status(r.status)
	}
	defer C.CFRelease(C.CFTypeRef(r.data))
	n := C.CFDataGetLength(r.data)
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(r.data)), C.int(n)), StatusOK
}

// Delete removes item from group.
func (k *keychain) Delete(_ context.Context, group, item string) Status {
	g, i, free := cStrings(group, item)
	defer free()
	return Status(C.wb_delete(g, i))
}

// errSecDecode is the status of a list whose names can't be read back.
const errSecDecode Status = -26275

// List returns the names of the items in group, never a value. The
// checks refuse a name that isn't an item of the group's role.
func (k *keychain) List(_ context.Context, group string) ([]string, Status) {
	g := C.CString(group)
	defer C.free(unsafe.Pointer(g))
	r := C.wb_list(g)
	defer C.free(unsafe.Pointer(r.names))
	if r.status != 0 {
		return nil, Status(r.status)
	}
	if r.count == 0 {
		return nil, StatusOK
	}
	names := strings.Split(C.GoString(r.names), "\n")
	if len(names) != int(r.count) {
		return nil, errSecDecode
	}
	return names, StatusOK
}

// itemAttributes is what the host test reads back about an item.
type itemAttributes struct {
	// Count is how many items of the name the process reaches.
	Count int
	// Group is the access group of the first one.
	Group string
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
	if r.accessible != nil {
		a.Accessible = C.GoString(r.accessible)
	}
	return a, StatusOK
}
