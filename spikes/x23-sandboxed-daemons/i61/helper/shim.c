// Throwaway cgo shim over Security.framework, copied from X29-dev-signing
// (spike/x29-dev-signing d236057) for I61.
#include "shim.h"
#include <stdio.h>
#include <string.h>

// Exported by Security.framework but not in the SDK headers (SPI), as in
// X09-keychain-unsigned.
extern const CFStringRef kSecAttrTokenOID;

static int fail_status(OSStatus st, x29err *e, const char *what) {
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

static void fail_cferr(CFErrorRef err, x29err *e, const char *what) {
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

static CFMutableDictionaryRef dict(void) {
	return CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks,
			&kCFTypeDictionaryValueCallBacks);
}

static void set_str(CFMutableDictionaryRef d, CFStringRef k, const char *v) {
	CFStringRef s = str(v);
	CFDictionarySetValue(d, k, s);
	CFRelease(s);
}

static CFMutableDictionaryRef item_query(const char *group, const char *account) {
	CFMutableDictionaryRef q = dict();
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	set_str(q, kSecAttrService, "x29");
	set_str(q, kSecAttrAccount, account);
	if (group[0]) set_str(q, kSecAttrAccessGroup, group);
	return q;
}

int x29_put(const char *group, const char *account, const unsigned char *v,
		int vlen, x29err *e) {
	CFMutableDictionaryRef q = item_query(group, account);
	CFDataRef d = CFDataCreate(NULL, v, vlen);
	CFDictionarySetValue(q, kSecValueData, d);
	CFDictionarySetValue(q, kSecAttrAccessible,
			kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
	OSStatus st = SecItemAdd(q, NULL);
	if (st) fail_status(st, e, "SecItemAdd");
	CFRelease(d);
	CFRelease(q);
	return (int)st;
}

int x29_get(const char *group, const char *account, unsigned char *out,
		int outcap, x29err *e) {
	CFMutableDictionaryRef q = item_query(group, account);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	CFTypeRef r = NULL;
	OSStatus st = SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (st) return -1 + 0 * fail_status(st, e, "SecItemCopyMatching");
	int n = (int)CFDataGetLength(r);
	if (n > outcap) n = outcap;
	memcpy(out, CFDataGetBytePtr(r), (size_t)n);
	CFRelease(r);
	return n;
}

int x29_del(const char *group, const char *account, x29err *e) {
	CFMutableDictionaryRef q = item_query(group, account);
	OSStatus st = SecItemDelete(q);
	if (st) fail_status(st, e, "SecItemDelete");
	CFRelease(q);
	return (int)st;
}

int x29_list(x29err *e) {
	CFMutableDictionaryRef q = dict();
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	set_str(q, kSecAttrService, "x29");
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef r = NULL;
	OSStatus st = SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (st) return fail_status(st, e, "SecItemCopyMatching(list)");
	CFIndex n = CFArrayGetCount(r);
	for (CFIndex i = 0; i < n; i++) {
		CFDictionaryRef a = CFArrayGetValueAtIndex(r, i);
		char acct[256] = "", grp[256] = "";
		CFStringRef s = CFDictionaryGetValue(a, kSecAttrAccount);
		if (s) CFStringGetCString(s, acct, sizeof acct, kCFStringEncodingUTF8);
		s = CFDictionaryGetValue(a, kSecAttrAccessGroup);
		if (s) CFStringGetCString(s, grp, sizeof grp, kCFStringEncodingUTF8);
		printf("  item account=%s group=%s\n", acct, grp);
	}
	CFRelease(r);
	return 0;
}

SecKeyRef x29_se_new(int permanent, const char *group, const char *label,
		x29err *e) {
	CFErrorRef err = NULL;
	SecAccessControlRef ac = SecAccessControlCreateWithFlags(NULL,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
			kSecAccessControlPrivateKeyUsage, &err);
	if (!ac) {
		fail_cferr(err, e, "SecAccessControlCreateWithFlags");
		return NULL;
	}
	CFMutableDictionaryRef priv = dict();
	CFMutableDictionaryRef a = dict();
	CFDataRef tag = CFDataCreate(NULL, (const UInt8 *)label, (CFIndex)strlen(label));
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	CFDictionarySetValue(priv, kSecAttrIsPermanent,
			permanent ? kCFBooleanTrue : kCFBooleanFalse);
	CFDictionarySetValue(priv, kSecAttrAccessControl, ac);
	CFDictionarySetValue(priv, kSecAttrApplicationTag, tag);
	CFDictionarySetValue(a, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(a, kSecAttrKeySizeInBits, size);
	CFDictionarySetValue(a, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFDictionarySetValue(a, kSecPrivateKeyAttrs, priv);
	if (permanent) {
		CFDictionarySetValue(a, kSecUseDataProtectionKeychain, kCFBooleanTrue);
		if (group[0]) set_str(a, kSecAttrAccessGroup, group);
	}
	SecKeyRef k = SecKeyCreateRandomKey(a, &err);
	if (!k) fail_cferr(err, e, "SecKeyCreateRandomKey");
	CFRelease(a);
	CFRelease(priv);
	CFRelease(ac);
	CFRelease(tag);
	CFRelease(size);
	return k;
}

SecKeyRef x29_se_find(const char *group, const char *label, x29err *e) {
	CFMutableDictionaryRef q = dict();
	CFDataRef tag = CFDataCreate(NULL, (const UInt8 *)label, (CFIndex)strlen(label));
	CFDictionarySetValue(q, kSecClass, kSecClassKey);
	CFDictionarySetValue(q, kSecAttrApplicationTag, tag);
	CFDictionarySetValue(q, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	if (group[0]) set_str(q, kSecAttrAccessGroup, group);
	CFDictionarySetValue(q, kSecReturnRef, kCFBooleanTrue);
	CFTypeRef k = NULL;
	OSStatus st = SecItemCopyMatching(q, &k);
	if (st) fail_status(st, e, "SecItemCopyMatching(key)");
	CFRelease(q);
	CFRelease(tag);
	return (SecKeyRef)k;
}

int x29_se_del(const char *group, const char *label, x29err *e) {
	CFMutableDictionaryRef q = dict();
	CFDataRef tag = CFDataCreate(NULL, (const UInt8 *)label, (CFIndex)strlen(label));
	CFDictionarySetValue(q, kSecClass, kSecClassKey);
	CFDictionarySetValue(q, kSecAttrApplicationTag, tag);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanTrue);
	if (group[0]) set_str(q, kSecAttrAccessGroup, group);
	OSStatus st = SecItemDelete(q);
	if (st) fail_status(st, e, "SecItemDelete(key)");
	CFRelease(q);
	CFRelease(tag);
	return (int)st;
}

int x29_se_oid(SecKeyRef k, unsigned char *out, int outcap, x29err *e) {
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

// The X09 restore path: SecKeyCreateRandomKey with the token object ID,
// not permanent, so nothing is added to a keychain.
SecKeyRef x29_se_restore(const unsigned char *oid, int len, x29err *e) {
	CFDataRef d = CFDataCreate(NULL, oid, len);
	CFMutableDictionaryRef a = dict();
	int bits = 256;
	CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
	CFDictionarySetValue(a, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(a, kSecAttrKeySizeInBits, size);
	CFDictionarySetValue(a, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFDictionarySetValue(a, kSecAttrTokenOID, d);
	CFDictionarySetValue(a, kSecAttrIsPermanent, kCFBooleanFalse);
	CFErrorRef err = NULL;
	SecKeyRef k = SecKeyCreateRandomKey(a, &err);
	if (!k) fail_cferr(err, e, "SecKeyCreateRandomKey(tokenOID)");
	CFRelease(a);
	CFRelease(d);
	CFRelease(size);
	return k;
}

int x29_se_pub(SecKeyRef k, unsigned char *out, int outcap, x29err *e) {
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

int x29_se_sign(SecKeyRef k, const unsigned char *digest, int dlen,
		unsigned char *out, int outcap, x29err *e) {
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

void x29_release(SecKeyRef k) {
	if (k) CFRelease(k);
}

// I61: a sandboxed wb-proxyd must not see the login keychain. Attributes
// only, so nothing prompts and no value is read.
int x29_file_kc_count(x29err *e) {
	CFMutableDictionaryRef q = dict();
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecUseDataProtectionKeychain, kCFBooleanFalse);
	CFDictionarySetValue(q, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	CFDictionarySetValue(q, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	CFTypeRef r = NULL;
	OSStatus st = SecItemCopyMatching(q, &r);
	CFRelease(q);
	if (st) return -1 + 0 * fail_status(st, e, "SecItemCopyMatching(file keychains)");
	int n = (int)CFArrayGetCount(r);
	CFRelease(r);
	return n;
}
