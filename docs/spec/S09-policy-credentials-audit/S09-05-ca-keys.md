# S09-05 - CA Keys

**Purpose:** How the key of each TLS inspection certificate authority is made,
stored and restored, and when a CA is valid.

## Design

### Key

Each CA has its own P-256 key. `wb-proxyd` generates it in
the platform's hardware key store (S12-platforms), non-exportable,
self-signs the CA certificate with it, and uses it to sign leaf
certificates (Go's certificate creation accepts any signer). Only
`wb-proxyd` writes, fetches, deletes, or uses the key and its store
item `wraithbox/ca/<vm>/<ca>` (S15-least-privilege, "Declared
access"). `wb-hostd` gets the public certificate over local IPC and
hands it to `wb-guestd`. Per host:
- *macOS, every build.* A Secure Enclave key that isn't stored in the
  Keychain, whose wrapped form, the token object ID, is the store
  item. To sign, `wb-proxyd` fetches the item and passes it to
  Security.framework in `kSecAttrTokenOID`, which restores the same
  key in this or a later process, also after a rebuild
  (X09-keychain-unsigned). Only this Mac's Secure Enclave can unwrap
  it, so a copied item is useless on another Mac. On this Mac any
  process of the user that has the value can sign with the key, and
  the store's G2 is what keeps it to `wb-proxyd`
  (S15-least-privilege). The same wrapped key is used in every
  build, on the Keychain and the 1Password backends alike (decided on
  I23, B23-keychain-unsigned, decision 2A). A permanent Secure Enclave
  key in the access group also works in a team-signed build
  (X29-dev-signing), and Wraith Box doesn't use one.
- *Attributes.* The ones X09-keychain-unsigned measured, and no
  others: the key has `kSecAttrIsPermanent` false, an access control
  with `kSecAccessControlPrivateKeyUsage` as its only flag, so no
  signature asks for user presence or biometry, and the protection
  `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`. The store item has
  `kSecAttrSynchronizable` false, so it never leaves the Mac.
- *`kSecAttrTokenOID` is undocumented.* Security.framework exports
  the symbol, but the SDK headers don't declare it, so the cgo shim
  (S10-tech-stack) declares it. Passed without that attribute, the ID
  gives a new key without an error (X09-keychain-unsigned). So each
  time `wb-proxyd` restores a key, it checks that the key's public
  key equals the one in the CA certificate before it signs.
- *Windows.* A TPM key through the CNG platform crypto provider,
  which `wb-proxyd` opens by its name, the item name `ca/<vm>/<ca>`.
  Windows 11 requires a TPM 2.0,
  and `wb setup` refuses a host whose platform crypto provider isn't
  available, with no software fallback.
- *Linux.* `wb setup` decides once whether the host has a usable TPM
  2.0, records the choice in `state.db`, and says it. With a TPM, the
  key's wrapped blob is the store item, which `wb-proxyd` loads into
  the TPM to sign. Without one, the fallback: a software key held in
  the secret store as the item, which `wb-proxyd` fetches and signs
  with in its own memory, and the audit record of each CA issue says
  so.
- *Linux, the recorded choice.* `wb-proxyd` probes for a TPM itself at
  start and compares the result with the record. A record of
  "software" on a host that has a TPM, and a record of "TPM" on a
  host that has none, fail each CA issue and restore with the rule
  `ca-key-store-mismatch`, and `wb-proxyd` never moves to the other
  kind on its own. `wb setup` changes a recorded choice only with
  `--ca-key-store tpm` or `--ca-key-store software`, and refuses
  `software` on a host with a TPM. The change drops every CA from the
  record, as `wb vm reset-ca` does for each VM ("Recovery"). So the
  recovery for a host that lost its TPM is `wb setup --ca-key-store
  software`.

### Valid CA

A valid CA is in `wb-hostd`'s record, hasn't
expired, and `wb-proxyd` has fetched its item and restored a key
whose public key equals the certificate's. `wb-proxyd` signs only
with a valid CA.
- *Issue.* `wb-proxyd` makes the key, writes the item, reads it back,
  restores the key from what it read, and compares the public key.
  Only after that round trip does `wb-hostd` record the CA. A failed
  step deletes the item, leaves the record unchanged, and fails the issue with
  `ca-key-restore`.
- *Restore.* In every build and on every backend, a read that fails because the store is
  locked, -25308 on the Keychain or a locked 1Password, waits
  for the unlock (S15-least-privilege, rule 7). Any other failure is `ca-key-restore`: a missing item, a store error,
  an error from the restore, or a public key that differs. Then
  `wb-proxyd` serves none of the VM's streams, and the VM's start or
  restore is refused with that rule.
- *Ad-hoc signed builds.* In an ad-hoc signed build, the Keychain
  backend refuses every access with `keychain-needs-team`
  (S15-least-privilege, "The store holds credentials"). An issue or
  a restore on that backend fails with that rule in place of
  `ca-key-restore`, and so does the VM's start or restore.
- *Recovery.* `wb vm reset-ca <vm>` (S05-cli) drops the VM's CAs from
  `wb-hostd`'s record, puts their items on the removal list
  (S15-least-privilege, "Declared access"), and deletes the VM's
  saved state. The VM then cold boots. At that start the record has no CA, or every CA in it has expired,
  so `wb-proxyd` issues a new CA, and the install rewrites the
  guest's trust files whole (S09-07-guest-trust). Only this command
  and `wb setup --ca-key-store` drop a CA that hasn't expired. Each
  drop is a Device Config State Change (5019) event with the command
  and the SHA-256 fingerprint of each dropped CA certificate. Neither
  rotation nor a restart issues a new CA in place of one that
  failed.

**Status:** Draft
