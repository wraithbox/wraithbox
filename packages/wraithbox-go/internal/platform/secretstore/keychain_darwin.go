//go:build darwin && cgo

package secretstore

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// S15-03-access-rules, "No Keychain dialogs". The call is deprecated,
// and still the one that turns them off for the whole process
// (X09-keychain-unsigned).
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static int wb_dialogs_off(void) {
	return (int)SecKeychainSetUserInteractionAllowed(false);
}

static int wb_dialogs_allowed(int *allowed) {
	Boolean b = true;
	OSStatus st = SecKeychainGetUserInteractionAllowed(&b);
	*allowed = b ? 1 : 0;
	return (int)st;
}
#pragma clang diagnostic pop

// What wb_read_self found in the process's own signature.
typedef struct {
	// status is the OSStatus of reading the signature. When it is not 0,
	// nothing else is set.
	int status;
	// team is the team identifier, or NULL when the signature has none.
	char *team;
	// team_bad is 1 when the team value isn't a string that converts.
	int team_bad;
	// groups are the keychain-access-groups, one per line, or NULL when
	// there are none.
	char *groups;
	// groups_bad is 1 when the entitlements can't be read.
	int groups_bad;
	// identifier is the signing identifier, or NULL when there is none.
	char *identifier;
	// executable is the CFBundleExecutable of the Info.plist the
	// signature seals, or NULL when there is none.
	char *executable;
	// code_bad is 1 when either of them isn't a string that converts.
	int code_bad;
} wb_self;

// wb_cstring returns a malloc'd UTF-8 copy of s, or NULL.
static char *wb_cstring(CFStringRef s) {
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

// wb_join_groups sets out->groups to the strings of the array, one per
// line, or sets out->groups_bad when one isn't a string or holds a
// newline.
static void wb_join_groups(CFArrayRef groups, wb_self *out) {
	CFMutableStringRef joined = CFStringCreateMutable(NULL, 0);
	if (joined == NULL) {
		out->groups_bad = 1;
		return;
	}
	CFIndex n = CFArrayGetCount(groups);
	for (CFIndex i = 0; i < n; i++) {
		CFTypeRef v = CFArrayGetValueAtIndex(groups, i);
		if (v == NULL || CFGetTypeID(v) != CFStringGetTypeID() ||
				CFStringFind((CFStringRef)v, CFSTR("\n"), 0).location != kCFNotFound) {
			out->groups_bad = 1;
			CFRelease(joined);
			return;
		}
		if (i > 0) {
			CFStringAppend(joined, CFSTR("\n"));
		}
		CFStringAppend(joined, (CFStringRef)v);
	}
	if (n > 0) {
		out->groups = wb_cstring(joined);
		if (out->groups == NULL) {
			out->groups_bad = 1;
		}
	}
	CFRelease(joined);
}

// wb_read_groups reads the keychain-access-groups entitlement from the
// signing information. No entitlements, or no such entitlement, is no
// group.
static void wb_read_groups(CFDictionaryRef info, wb_self *out) {
	CFTypeRef entitlements = CFDictionaryGetValue(info, kSecCodeInfoEntitlementsDict);
	if (entitlements == NULL) {
		return;
	}
	if (CFGetTypeID(entitlements) != CFDictionaryGetTypeID()) {
		out->groups_bad = 1;
		return;
	}
	CFTypeRef groups = CFDictionaryGetValue((CFDictionaryRef)entitlements, CFSTR("keychain-access-groups"));
	if (groups == NULL) {
		return;
	}
	if (CFGetTypeID(groups) != CFArrayGetTypeID()) {
		out->groups_bad = 1;
		return;
	}
	wb_join_groups((CFArrayRef)groups, out);
}

// wb_copy_string sets *out to a malloc'd copy of v and returns 0, or
// returns 1 when v isn't a string that converts. A NULL v is no value.
static int wb_copy_string(CFTypeRef v, char **out) {
	if (v == NULL) {
		return 0;
	}
	if (CFGetTypeID(v) != CFStringGetTypeID()) {
		return 1;
	}
	*out = wb_cstring((CFStringRef)v);
	return *out == NULL ? 1 : 0;
}

// wb_read_code reads the signing identifier and the CFBundleExecutable
// of the sealed Info.plist from the signing information. A code without
// an Info.plist has no executable.
static void wb_read_code(CFDictionaryRef info, wb_self *out) {
	if (wb_copy_string(CFDictionaryGetValue(info, kSecCodeInfoIdentifier), &out->identifier) != 0) {
		out->code_bad = 1;
		return;
	}
	CFTypeRef plist = CFDictionaryGetValue(info, kSecCodeInfoPList);
	if (plist == NULL) {
		return;
	}
	if (CFGetTypeID(plist) != CFDictionaryGetTypeID()) {
		out->code_bad = 1;
		return;
	}
	CFTypeRef exe = CFDictionaryGetValue((CFDictionaryRef)plist, kCFBundleExecutableKey);
	if (wb_copy_string(exe, &out->executable) != 0) {
		out->code_bad = 1;
	}
}

// wb_fill_self reads the team, the access groups, the signing identifier
// and the executable of the process's own code signature into out
// (S15-02-secret-store, "The group of each call").
static void wb_fill_self(wb_self *out) {
	memset(out, 0, sizeof *out);
	SecCodeRef self = NULL;
	OSStatus st = SecCodeCopySelf(kSecCSDefaultFlags, &self);
	if (st != errSecSuccess) {
		out->status = (int)st;
		return;
	}
	CFDictionaryRef info = NULL;
	st = SecCodeCopySigningInformation((SecStaticCodeRef)self,
			kSecCSSigningInformation | kSecCSRequirementInformation, &info);
	CFRelease(self);
	if (st != errSecSuccess) {
		out->status = (int)st;
		return;
	}
	CFTypeRef team = CFDictionaryGetValue(info, kSecCodeInfoTeamIdentifier);
	if (team != NULL) {
		if (CFGetTypeID(team) == CFStringGetTypeID()) {
			out->team = wb_cstring((CFStringRef)team);
		}
		if (out->team == NULL) {
			out->team_bad = 1;
		}
	}
	wb_read_groups(info, out);
	wb_read_code(info, out);
	CFRelease(info);
}

// wb_read_self returns what wb_fill_self reads. It returns the struct by
// value, so the Go side passes no pointer into C.
static wb_self wb_read_self(void) {
	wb_self out;
	wb_fill_self(&out);
	return out;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unsafe"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Open opens the Keychain for program, the caller's compiled-in
// identity. It turns dialogs off, then reads the team, the groups and
// the executable of the process's own signature, never from
// configuration, a flag, the environment or a request
// (S15-02-secret-store, "The group of each call"). A program calls it
// before it confines itself, and before it reads any argument,
// environment variable or input, as wb-diskfmt works out its bundle path
// before selfsandbox.Confine. See New for what it returns.
func Open(program storeitem.Program) (Store, error) {
	return New(program, &keychain{}, slog.Default())
}

// keychain is the darwin backend: the data protection keychain, through
// the cgo shim (S10-01-libraries). The item calls are in items_darwin.go.
type keychain struct {
	once      sync.Once
	team      string
	teamErr   error
	groups    []string
	groupsErr error
	code      Code
	codeErr   error
}

func (k *keychain) DialogsOff() error {
	if st := C.wb_dialogs_off(); st != 0 {
		return fmt.Errorf("SecKeychainSetUserInteractionAllowed: OSStatus %d", int(st))
	}
	return nil
}

func (k *keychain) Team() (string, error) {
	k.once.Do(k.readSelf)
	return k.team, k.teamErr
}

func (k *keychain) Groups() ([]string, error) {
	k.once.Do(k.readSelf)
	return k.groups, k.groupsErr
}

func (k *keychain) Code() (Code, error) {
	k.once.Do(k.readSelf)
	return k.code, k.codeErr
}

// readSelf reads the process's own signature once. An unreadable
// signature holds no team, no group and no executable
// (S15-02-secret-store).
func (k *keychain) readSelf() {
	self := C.wb_read_self()
	defer C.free(unsafe.Pointer(self.team))
	defer C.free(unsafe.Pointer(self.groups))
	defer C.free(unsafe.Pointer(self.identifier))
	defer C.free(unsafe.Pointer(self.executable))
	if self.status != 0 {
		err := fmt.Errorf("reading the process's own signature: OSStatus %d", int(self.status))
		k.teamErr, k.groupsErr, k.codeErr = err, err, err
		return
	}
	if self.team_bad != 0 {
		k.teamErr = errors.New("the signature's team identifier isn't a string")
	} else if self.team != nil {
		k.team = C.GoString(self.team)
	}
	if self.groups_bad != 0 {
		k.groupsErr = errors.New("the keychain-access-groups entitlement isn't a list of strings")
	} else if self.groups != nil {
		k.groups = strings.Split(C.GoString(self.groups), "\n")
	}
	if self.code_bad != 0 {
		k.codeErr = errors.New("the signing identifier or the sealed Info.plist's CFBundleExecutable isn't a string")
		return
	}
	if self.identifier != nil {
		k.code.Identifier = C.GoString(self.identifier)
	}
	if self.executable != nil {
		k.code.Executable = C.GoString(self.executable)
	}
}

// dialogsAllowed reports whether the Keychain may show a dialog in this
// process, for the test that sees Open turn them off.
func dialogsAllowed() (bool, error) {
	var allowed C.int
	if st := C.wb_dialogs_allowed(&allowed); st != 0 {
		return false, fmt.Errorf("SecKeychainGetUserInteractionAllowed: OSStatus %d", int(st))
	}
	return allowed != 0, nil
}
