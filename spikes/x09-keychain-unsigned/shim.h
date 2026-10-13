// Throwaway cgo shim over Security.framework for X09-keychain-unsigned.
#include <Security/Security.h>

typedef struct {
	int status;     // OSStatus, or CFError code
	char msg[512];  // human-readable error
} x9err;

// File keychain (legacy API, deprecated but the only way to a keychain file).
int x9_kc_create(const char *path, const char *pw, x9err *e);
int x9_kc_unlock(const char *path, const char *pw, x9err *e);
int x9_kc_lock(const char *path, x9err *e);

// Generic password in a keychain file. apps: trusted application paths for
// the ACL; an empty string means "this binary". n == -1 means no
// kSecAttrAccess at all (the default ACL).
int x9_item_add(const char *path, const char *service, const char *value,
		const char **apps, int n, x9err *e);
int x9_item_read(const char *path, const char *service, char *out, int outcap,
		x9err *e);
int x9_item_delete(const char *path, const char *service, x9err *e);
// Replace the ACL (trusted apps) using the keychain password, no prompt.
int x9_item_set_access_pw(const char *path, const char *service,
		const char *pw, const char **apps, int n, x9err *e);

// Secure Enclave keys.
// mode: 0 ephemeral, 1 permanent (default keychain), 2 permanent with
// kSecUseDataProtectionKeychain.
SecKeyRef x9_se_create(int mode, const char *label, x9err *e);
SecKeyRef x9_se_find(const char *label, x9err *e);
int x9_se_delete(const char *label, x9err *e);
int x9_se_pub(SecKeyRef k, unsigned char *out, int outcap, x9err *e);
int x9_se_sign(SecKeyRef k, const unsigned char *digest, int dlen,
		unsigned char *out, int outcap, x9err *e);
int x9_se_token_oid(SecKeyRef k, unsigned char *out, int outcap, x9err *e);
SecKeyRef x9_se_restore(int how, const unsigned char *oid, int len, x9err *e);
void x9_release(SecKeyRef k);
void x9_noui(void);
int x9_item_set_access_pw2(const char *path, const char *service,
		const char *pw, const char **apps, int n, x9err *e);
