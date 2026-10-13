// Throwaway cgo shim over Security.framework for X09-keychain-unsigned.
#include "shim.h"
#include <string.h>
#include <stdio.h>
#include <stdlib.h>

// Exported by Security.framework but not in the SDK headers (SPI).
extern const CFStringRef kSecAttrTokenOID;
extern OSStatus SecKeychainItemSetAccessWithPassword(SecKeychainItemRef item,
		SecAccessRef access, UInt32 pwlen, const void *pw);

static int fail_status(OSStatus st, x9err *e, const char *what) {
	e->status = (int)st;
	CFStringRef m = SecCopyErrorMessageString(st, NULL);
	char buf[400] = "";
	if (m) {
		CFStringGetCString(m, buf, sizeof buf, kCFStringEncodingUTF8);
		CFRelease(m);
	}
	snprintf(e->msg, sizeof e->msg, "%s: OSStatus %d: %s", what, (int)st, buf);
	return (int)st;
}

static void fail_cferr(CFErrorRef err, x9err *e, const char *what) {
	if (!err) {
		e->status = -1;
		snprintf(e->msg, sizeof e->msg, "%s: failed without an error", what);
		return;
	}
	e->status = (int)CFErrorGetCode(err);
	CFStringRef d = CFErrorCopyDescription(err);
	char buf[400] = "";
	if (d) {
		CFStringGetCString(d, buf, sizeof buf, kCFStringEncodingUTF8);
		CFRelease(d);
	}
	snprintf(e->msg, sizeof e->msg, "%s: error %ld: %s", what,
			(long)CFErrorGetCode(err), buf);
	CFRelease(err);
}

static CFStringRef str(const char *s) {
	return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

int x9_kc_create(const char *path, const char *pw, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = SecKeychainCreate(path, (UInt32)strlen(pw), pw, false, NULL, &kc);
	if (st) return fail_status(st, e, "SecKeychainCreate");
	CFRelease(kc);
	return 0;
}

static OSStatus open_kc(const char *path, SecKeychainRef *kc) {
	return SecKeychainOpen(path, kc);
}

int x9_kc_unlock(const char *path, const char *pw, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	st = SecKeychainUnlock(kc, (UInt32)strlen(pw), pw, true);
	CFRelease(kc);
	if (st) return fail_status(st, e, "SecKeychainUnlock");
	return 0;
}

int x9_kc_lock(const char *path, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	st = SecKeychainLock(kc);
	CFRelease(kc);
	if (st) return fail_status(st, e, "SecKeychainLock");
	return 0;
}

static OSStatus make_access(const char *label, const char **apps, int n,
		SecAccessRef *out) {
	CFMutableArrayRef list = CFArrayCreateMutable(NULL, n, &kCFTypeArrayCallBacks);
	OSStatus st = 0;
	for (int i = 0; i < n; i++) {
		SecTrustedApplicationRef app = NULL;
		st = SecTrustedApplicationCreateFromPath(apps[i][0] ? apps[i] : NULL, &app);
		if (st) break;
		CFArrayAppendValue(list, app);
		CFRelease(app);
	}
	if (!st) {
		CFStringRef l = str(label);
		st = SecAccessCreate(l, list, out);
		CFRelease(l);
	}
	CFRelease(list);
	return st;
}

int x9_item_add(const char *path, const char *service, const char *value,
		const char **apps, int n, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef svc = str(service), acct = str("x09");
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)value, (CFIndex)strlen(value));
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, svc);
	CFDictionarySetValue(q, kSecAttrAccount, acct);
	CFDictionarySetValue(q, kSecValueData, data);
	CFDictionarySetValue(q, kSecUseKeychain, kc);
	SecAccessRef access = NULL;
	if (n >= 0) {
		st = make_access(service, apps, n, &access);
		if (st) {
			fail_status(st, e, "SecAccessCreate");
			goto out;
		}
		CFDictionarySetValue(q, kSecAttrAccess, access);
	}
	st = SecItemAdd(q, NULL);
	if (st) fail_status(st, e, "SecItemAdd");
out:
	if (access) CFRelease(access);
	CFRelease(q);
	CFRelease(svc);
	CFRelease(acct);
	CFRelease(data);
	CFRelease(kc);
	return (int)st;
}

static CFMutableDictionaryRef item_query(SecKeychainRef kc, const char *service,
		CFStringRef *svc) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	*svc = str(service);
	CFArrayRef search = CFArrayCreate(NULL, (const void **)&kc, 1, &kCFTypeArrayCallBacks);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, *svc);
	CFDictionarySetValue(q, kSecMatchSearchList, search);
	CFRelease(search);
	return q;
}

int x9_item_read(const char *path, const char *service, char *out, int outcap,
		x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	CFStringRef svc;
	CFMutableDictionaryRef q = item_query(kc, service, &svc);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	if (getenv("X9_NOUI")) CFDictionarySetValue(q, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	CFTypeRef res = NULL;
	st = SecItemCopyMatching(q, &res);
	if (st) {
		fail_status(st, e, "SecItemCopyMatching");
	} else {
		CFIndex len = CFDataGetLength(res);
		if (len >= outcap) len = outcap - 1;
		memcpy(out, CFDataGetBytePtr(res), (size_t)len);
		out[len] = 0;
		CFRelease(res);
	}
	CFRelease(q);
	CFRelease(svc);
	CFRelease(kc);
	return (int)st;
}

int x9_item_delete(const char *path, const char *service, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	CFStringRef svc;
	CFMutableDictionaryRef q = item_query(kc, service, &svc);
	st = SecItemDelete(q);
	if (st) fail_status(st, e, "SecItemDelete");
	CFRelease(q);
	CFRelease(svc);
	CFRelease(kc);
	return (int)st;
}

int x9_item_set_access_pw(const char *path, const char *service,
		const char *pw, const char **apps, int n, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	CFStringRef svc;
	CFMutableDictionaryRef q = item_query(kc, service, &svc);
	CFDictionarySetValue(q, kSecReturnRef, kCFBooleanTrue);
	CFTypeRef item = NULL;
	SecAccessRef access = NULL;
	st = SecItemCopyMatching(q, &item);
	if (st) {
		fail_status(st, e, "SecItemCopyMatching(ref)");
		goto out;
	}
	st = make_access(service, apps, n, &access);
	if (st) {
		fail_status(st, e, "SecAccessCreate");
		goto out;
	}
	st = SecKeychainItemSetAccessWithPassword((SecKeychainItemRef)item, access,
			(UInt32)strlen(pw), pw);
	if (st) fail_status(st, e, "SecKeychainItemSetAccessWithPassword");
out:
	if (access) CFRelease(access);
	if (item) CFRelease(item);
	CFRelease(q);
	CFRelease(svc);
	CFRelease(kc);
	return (int)st;
}

SecKeyRef x9_se_create(int mode, const char *label, x9err *e) {
	CFErrorRef err = NULL;
	SecAccessControlRef ac = SecAccessControlCreateWithFlags(NULL,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
			kSecAccessControlPrivateKeyUsage, &err);
	if (!ac) {
		fail_cferr(err, e, "SecAccessControlCreateWithFlags");
		return NULL;
	}
	CFMutableDictionaryRef priv = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFMutableDictionaryRef a = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef l = str(label);
	CFDataRef tag = CFDataCreate(NULL, (const UInt8 *)label, (CFIndex)strlen(label));
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	CFDictionarySetValue(priv, kSecAttrIsPermanent, mode ? kCFBooleanTrue : kCFBooleanFalse);
	CFDictionarySetValue(priv, kSecAttrAccessControl, ac);
	CFDictionarySetValue(priv, kSecAttrLabel, l);
	CFDictionarySetValue(priv, kSecAttrApplicationTag, tag);
	CFDictionarySetValue(a, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(a, kSecAttrKeySizeInBits, size);
	CFDictionarySetValue(a, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFDictionarySetValue(a, kSecPrivateKeyAttrs, priv);
	if (mode == 2) CFDictionarySetValue(a, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	SecKeyRef k = SecKeyCreateRandomKey(a, &err);
	if (!k) fail_cferr(err, e, "SecKeyCreateRandomKey");
	CFRelease(a);
	CFRelease(priv);
	CFRelease(ac);
	CFRelease(l);
	CFRelease(tag);
	CFRelease(size);
	return k;
}

static CFMutableDictionaryRef key_query(const char *label, CFDataRef *tag) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	*tag = CFDataCreate(NULL, (const UInt8 *)label, (CFIndex)strlen(label));
	CFDictionarySetValue(q, kSecClass, kSecClassKey);
	CFDictionarySetValue(q, kSecAttrApplicationTag, *tag);
	CFDictionarySetValue(q, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	return q;
}

SecKeyRef x9_se_find(const char *label, x9err *e) {
	CFDataRef tag;
	CFMutableDictionaryRef q = key_query(label, &tag);
	CFDictionarySetValue(q, kSecReturnRef, kCFBooleanTrue);
	CFTypeRef k = NULL;
	OSStatus st = SecItemCopyMatching(q, &k);
	if (st) fail_status(st, e, "SecItemCopyMatching(key)");
	CFRelease(q);
	CFRelease(tag);
	return (SecKeyRef)k;
}

int x9_se_delete(const char *label, x9err *e) {
	CFDataRef tag;
	CFMutableDictionaryRef q = key_query(label, &tag);
	OSStatus st = SecItemDelete(q);
	if (st) fail_status(st, e, "SecItemDelete(key)");
	CFRelease(q);
	CFRelease(tag);
	return (int)st;
}

int x9_se_pub(SecKeyRef k, unsigned char *out, int outcap, x9err *e) {
	SecKeyRef pub = SecKeyCopyPublicKey(k);
	if (!pub) {
		e->status = -1;
		snprintf(e->msg, sizeof e->msg, "SecKeyCopyPublicKey: NULL");
		return -1;
	}
	CFErrorRef err = NULL;
	CFDataRef d = SecKeyCopyExternalRepresentation(pub, &err);
	CFRelease(pub);
	if (!d) {
		fail_cferr(err, e, "SecKeyCopyExternalRepresentation");
		return -1;
	}
	int n = (int)CFDataGetLength(d);
	if (n > outcap) n = outcap;
	memcpy(out, CFDataGetBytePtr(d), (size_t)n);
	CFRelease(d);
	return n;
}

int x9_se_sign(SecKeyRef k, const unsigned char *digest, int dlen,
		unsigned char *out, int outcap, x9err *e) {
	CFDataRef in = CFDataCreate(NULL, digest, dlen);
	CFErrorRef err = NULL;
	CFDataRef sig = SecKeyCreateSignature(k,
			kSecKeyAlgorithmECDSASignatureDigestX962SHA256, in, &err);
	CFRelease(in);
	if (!sig) {
		fail_cferr(err, e, "SecKeyCreateSignature");
		return -1;
	}
	int n = (int)CFDataGetLength(sig);
	if (n > outcap) n = outcap;
	memcpy(out, CFDataGetBytePtr(sig), (size_t)n);
	CFRelease(sig);
	return n;
}

int x9_se_token_oid(SecKeyRef k, unsigned char *out, int outcap, x9err *e) {
	CFDictionaryRef attrs = SecKeyCopyAttributes(k);
	if (!attrs) {
		e->status = -1;
		snprintf(e->msg, sizeof e->msg, "SecKeyCopyAttributes: NULL");
		return -1;
	}
	CFDataRef oid = CFDictionaryGetValue(attrs, kSecAttrTokenOID);
	int n = -1;
	if (oid && CFGetTypeID(oid) == CFDataGetTypeID()) {
		n = (int)CFDataGetLength(oid);
		if (n > outcap) n = outcap;
		memcpy(out, CFDataGetBytePtr(oid), (size_t)n);
	} else {
		e->status = -1;
		snprintf(e->msg, sizeof e->msg, "no kSecAttrTokenOID in attributes");
	}
	CFRelease(attrs);
	return n;
}

// how 1: SecKeyCreateWithData; how 2: SecKeyCreateRandomKey with the token OID.
SecKeyRef x9_se_restore(int how, const unsigned char *oid, int len, x9err *e) {
	CFDataRef d = CFDataCreate(NULL, oid, len);
	CFMutableDictionaryRef a = CFDictionaryCreateMutable(NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	CFDictionarySetValue(a, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(a, kSecAttrKeySizeInBits, size);
	CFDictionarySetValue(a, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFErrorRef err = NULL;
	SecKeyRef k = NULL;
	if (how == 1) {
		CFDictionarySetValue(a, kSecAttrKeyClass, kSecAttrKeyClassPrivate);
		k = SecKeyCreateWithData(d, a, &err);
		if (!k) fail_cferr(err, e, "SecKeyCreateWithData");
	} else if (how == 2) {
		CFDictionarySetValue(a, kSecAttrTokenOID, d);
		k = SecKeyCreateRandomKey(a, &err);
		if (!k) fail_cferr(err, e, "SecKeyCreateRandomKey(tokenOID)");
	} else if (how == 3) {
		// Not permanent, so nothing is added to a keychain.
		CFDictionarySetValue(a, kSecAttrTokenOID, d);
		CFDictionarySetValue(a, kSecAttrIsPermanent, kCFBooleanFalse);
		k = SecKeyCreateRandomKey(a, &err);
		if (!k) fail_cferr(err, e, "SecKeyCreateRandomKey(tokenOID, not permanent)");
	} else {
		CFDictionarySetValue(a, kSecAttrKeyClass, kSecAttrKeyClassPrivate);
		CFDictionarySetValue(a, kSecAttrTokenOID, d);
		k = SecKeyCreateWithData(d, a, &err);
		if (!k) fail_cferr(err, e, "SecKeyCreateWithData(tokenOID)");
	}
	CFRelease(a);
	CFRelease(d);
	CFRelease(size);
	return k;
}

void x9_release(SecKeyRef k) {
	if (k) CFRelease(k);
}

void x9_noui(void) { SecKeychainSetUserInteractionAllowed(false); }

// Copy the item's access, replace the app list of the decrypt entry, and
// write it back with the keychain password.
int x9_item_set_access_pw2(const char *path, const char *service,
		const char *pw, const char **apps, int n, x9err *e) {
	SecKeychainRef kc = NULL;
	OSStatus st = open_kc(path, &kc);
	if (st) return fail_status(st, e, "SecKeychainOpen");
	CFStringRef svc;
	CFMutableDictionaryRef q = item_query(kc, service, &svc);
	CFDictionarySetValue(q, kSecReturnRef, kCFBooleanTrue);
	CFTypeRef item = NULL;
	SecAccessRef access = NULL;
	CFArrayRef acls = NULL;
	CFMutableArrayRef list = CFArrayCreateMutable(NULL, n, &kCFTypeArrayCallBacks);
	st = SecItemCopyMatching(q, &item);
	if (st) { fail_status(st, e, "SecItemCopyMatching(ref)"); goto out; }
	st = SecKeychainItemCopyAccess((SecKeychainItemRef)item, &access);
	if (st) { fail_status(st, e, "SecKeychainItemCopyAccess"); goto out; }
	acls = SecAccessCopyMatchingACLList(access, kSecACLAuthorizationDecrypt);
	if (!acls || CFArrayGetCount(acls) == 0) { st = -1; snprintf(e->msg, sizeof e->msg, "no decrypt ACL"); goto out; }
	for (int i = 0; i < n; i++) {
		SecTrustedApplicationRef app = NULL;
		st = SecTrustedApplicationCreateFromPath(apps[i][0] ? apps[i] : NULL, &app);
		if (st) { fail_status(st, e, "SecTrustedApplicationCreateFromPath"); goto out; }
		CFArrayAppendValue(list, app);
		CFRelease(app);
	}
	SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(acls, 0);
	CFArrayRef oldApps = NULL; CFStringRef desc = NULL;
	SecKeychainPromptSelector sel = 0;
	st = SecACLCopyContents(acl, &oldApps, &desc, &sel);
	if (st) { fail_status(st, e, "SecACLCopyContents"); goto out; }
	st = SecACLSetContents(acl, list, desc, sel);
	if (oldApps) CFRelease(oldApps);
	if (desc) CFRelease(desc);
	if (st) { fail_status(st, e, "SecACLSetContents"); goto out; }
	st = SecKeychainItemSetAccessWithPassword((SecKeychainItemRef)item, access,
			(UInt32)strlen(pw), pw);
	if (st) fail_status(st, e, "SecKeychainItemSetAccessWithPassword");
out:
	CFRelease(list);
	if (acls) CFRelease(acls);
	if (access) CFRelease(access);
	if (item) CFRelease(item);
	CFRelease(q);
	CFRelease(svc);
	CFRelease(kc);
	return (int)st;
}
