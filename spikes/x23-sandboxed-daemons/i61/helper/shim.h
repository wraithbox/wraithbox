// Throwaway cgo shim over Security.framework for X29-dev-signing: the data
// protection keychain behind one access group, from helper binaries.
#include <Security/Security.h>

typedef struct {
	int status;     // OSStatus, or CFError code
	char msg[512];
} x29err;

// Generic password items, service "x29", in the data protection keychain.
// group may be "" to leave kSecAttrAccessGroup out.
int x29_put(const char *group, const char *account, const unsigned char *v,
		int vlen, x29err *e);
int x29_get(const char *group, const char *account, unsigned char *out,
		int outcap, x29err *e);
int x29_del(const char *group, const char *account, x29err *e);
// Prints every x29 item the caller can see, with its access group.
int x29_list(x29err *e);

// Secure Enclave keys. permanent 0: ephemeral; 1: permanent in the data
// protection keychain under group, with label as its application tag.
SecKeyRef x29_se_new(int permanent, const char *group, const char *label,
		x29err *e);
SecKeyRef x29_se_find(const char *group, const char *label, x29err *e);
int x29_se_del(const char *group, const char *label, x29err *e);
int x29_se_oid(SecKeyRef k, unsigned char *out, int outcap, x29err *e);
SecKeyRef x29_se_restore(const unsigned char *oid, int len, x29err *e);
int x29_se_pub(SecKeyRef k, unsigned char *out, int outcap, x29err *e);
int x29_se_sign(SecKeyRef k, const unsigned char *digest, int dlen,
		unsigned char *out, int outcap, x29err *e);
void x29_release(SecKeyRef k);

// I61: counts generic passwords in the file-based keychains on the search
// list (the login keychain), attributes only, never data. -1 on error.
int x29_file_kc_count(x29err *e);
