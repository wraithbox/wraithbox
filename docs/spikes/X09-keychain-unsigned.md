# X09 - Keychain access without a signing identity

**Purpose:** The result of the spike for I23. Does the Keychain model
for ad-hoc signed builds in S15-least-privilege work, and can an ad-hoc
signed binary without entitlements keep a Secure Enclave key for the
CA of S09-policy-credentials-audit?

**Requirements:** SEC04-no-guest-secrets, SEC12-least-privilege,
SEC14-no-fake-approvals

**Brief:** B23-keychain-unsigned

## For review

- **Decides:** how ad-hoc signed builds keep their store items across
  an update, and where the CA signing key is kept.
- **You are approving:** the answers below, and the S09 and S15 text
  in this pull request that states what the measured fallback does
  today:
  - a keychain file item is reachable without a dialog only by the
    binaries its access-control list names, each pinned by its cdhash;
  - every other reader, and every rebuilt binary, gets a dialog that
    asks for the password of the keychain file, which no user knows;
  - code can't repair an access-control list without that dialog, and
    only `SecKeychainSetUserInteractionAllowed(false)` turns the dialog
    into an error.
- **Controls touched:** SEC12-least-privilege (credentials are held by
  the store and reached only by their declared processes): unchanged by
  this pull request. The ad-hoc fallback gives G2 by cdhash between
  updates, and the update path is the first open decision.
  SEC14-no-fake-approvals (a prompt is never an approval): a dialog the
  user can't answer is a problem for this control. The first open
  decision removes it.
- **Assumed:**
  - that an item in the login keychain gets the same dialog, asking
    for the login password, which the user knows. The spike didn't
    touch the login keychain (I23);
  - that `kSecAttrTokenOID` keeps its value `toid` and its use for
    restoring a key. Security.framework exports it, but it isn't in
    the SDK headers. Secretive reads the same attribute;
  - that a CryptoKit `dataRepresentation` and the token object ID stay
    the same format. They were on macOS 27.0.1.
- **Decisions:** decided 2026-10-15, as recommended, in the I23
  comment that starts `Decision (2026-10-15):`. The specs have them
  (I249): S15-least-privilege ("Keychain", access rule 4, "Declared
  access", and a section on updates that was removed later), S09-04-credentials ("Storage"),
  S09-05-ca-keys ("Key"), S10-tech-stack ("Packaging and signing") and S12-01-platform-interfaces ("Hardware key").
  1. How ad-hoc signed builds keep their items across an update.
     - A. Fail closed: after an update, `wb` says the store must be set
       up again, and the user enters each binding again. Generated
       items, such as `wbadmin/vm/<vm>/<disk>`, are lost.
     - B. Before the binaries are replaced, the old ones copy every item
       into a new keychain file whose access-control lists name the new
       binaries, already on disk. Adding an item with a list of other
       binaries didn't raise a dialog. The whole path wasn't run.
     - C. The keychain file gets a password the user knows, so the
       dialog after an update can be answered once per item.
     - D. No ad-hoc Keychain support: development builds are signed
       with the developer's own Apple Development certificate and a
       profile with a Keychain access group, as Secretive does. Whether
       a free personal team can grant `keychain-access-groups` to the
       helpers in `Wraith Box.app` needs a spike.

     Recommended: D, with a spike first, and A until that spike answers.
     A gives the fail-closed behavior S15 already asks for, with no
     dialog. B and C keep a model that only exists for development
     builds. Decided: D, after X29-dev-signing (I248), and A until that
     spike answers yes.
  2. Where the CA signing key is kept. A permanent Secure Enclave key
     needs an entitlement, so the spec's "key handle" isn't available to
     an ad-hoc build.
     - A. An ephemeral Secure Enclave key, kept as its token object ID
       in the store item `ca/<vm>/<ca>`, for every build. The value is
       usable only by this Mac's Secure Enclave. In a signed build the
       access group protects the item.
     - B. The software fallback key of S09 for ad-hoc builds, and a
       permanent Secure Enclave key for signed builds.

     Recommended: A. Signed and ad-hoc builds share the code path, and a copied item is
     useless on another Mac. Secretive keeps its keys the same way. A
     needs one SPI symbol in the cgo shim. Decided: A. A Linux host
     without a TPM keeps the software key, which no macOS build uses.
- **Brief:** B23-keychain-unsigned

## Question

From I23, for an ad-hoc signed binary with the hardened runtime and no
entitlements:

1. A generic password item whose access-control list names only this
   binary. Can this binary, another ad-hoc binary and
   `security find-generic-password` read it, and what does a rebuild
   (new cdhash) do?
2. Can it create a P-256 Secure Enclave key, permanent and not, and
   sign a certificate with it through Go's `crypto.Signer`?
3. How long does signing a leaf certificate take?

## Answer

1. **Yes, between updates. No across an update.** The list pins each
   ad-hoc binary by its cdhash. A binary on the list reads without a
   dialog. Another binary, Apple's `security` tool, and the same
   binary after a rebuild get a dialog that asks for the password of
   the keychain file. In S15 that password is random and the user
   never sees it, so the user can't answer it. No call repaired the list
   without the dialog.
2. **A permanent key: no. A key kept across restarts: yes.**
   A permanent key fails with -34018 (`errSecMissingEntitlement`). An
   ephemeral key works, and its token object ID, saved, and passed back
   to Security.framework, restores the same key in another process,
   another binary, and a rebuilt binary.
3. **4.7 ms per leaf** with the Secure Enclave key, against 0.1 ms with
   a software key.

## Measurements

All runs were on the host, macOS 27.0.1 (26A434), Apple M2, Go 1.27.1,
with the binaries signed `codesign -s - -o runtime`. The keychain was a
throwaway file made with `SecKeychainCreate` and a random password,
outside the search list, deleted afterwards. The maintainer watched the
screen and answered each dialog.

### Keychain file

| Step | Result |
|---|---|
| `x9-a` adds an item with its own access-control list | no dialog. The list holds `requirement: cdhash H"842344…"` |
| `x9-a` adds an item whose list names `x9-a` and `x9-b` | no dialog, the list holds both binaries |
| `x9-a` reads its item | no dialog, 5 ms |
| `x9-b` reads `x9-a`'s item | dialog: "x9-b wants to use your confidential information stored in "svc-self" in your keychain", a field for the "x09" keychain password, Always Allow, Deny, Allow. Deny returned -128 |
| `security find-generic-password` reads it | the same dialog for `security`, the same password field. Deny returned exit 128 |
| rebuilt `x9-a` (new cdhash) reads its item | the same dialog. With the password and Always Allow, the list gained the new cdhash and kept the old one, and the next read had no dialog |
| rebuilt `x9-a` replaces a list with `SecKeychainItemSetAccessWithPassword` and the right password | -25293, for a new access object and for the item's own one edited in place |
| `security set-generic-password-partition-list -k` | exit 0, list unchanged. These items have no partition entry, both for the default list and for a custom one |

So a list grows with each build after "Always Allow", and nothing a
Wraith Box process can do without the user stops the dialogs after an
update.

Turning the dialog into an error:

| Read with | Result |
|---|---|
| `kSecUseAuthenticationUIFail` | ignored for a keychain file: the dialog appeared and the call waited for it |
| `SecKeychainSetUserInteractionAllowed(false)` | -25293 in 9 ms, no dialog |

The second call is deprecated and applies to the whole process.

### Secure Enclave

| Step | Result |
|---|---|
| ephemeral key (`kSecAttrIsPermanent` false), `kSecAccessControlPrivateKeyUsage`, `kSecAttrAccessibleWhenUnlockedThisDeviceOnly` | works. Signs a self-signed CA through `crypto.Signer`, and the signature verifies |
| permanent key, with `kSecUseDataProtectionKeychain` | -34018, "failed to add key to keychain" |
| token object ID of the ephemeral key, from `SecKeyCopyAttributes` | 324 bytes, saved to a file |
| `SecKeyCreateRandomKey` or `SecKeyCreateWithData` with the ID in `kSecAttrTokenOID` | the same public key, in another process, in `x9-b`, and after a rebuild |
| `SecKeyCreateWithData` with the ID as data but without `kSecAttrTokenOID` | a new key each time, without an error, also for random bytes |
| CryptoKit `SecureEnclave.P256.Signing.PrivateKey(dataRepresentation:)` on the saved ID | the same public key. The reverse works too |

The ID is wrapped by this Mac's Secure Enclave and useless elsewhere.
On this Mac any process of the user that has it can sign with the key.
Neither the binary nor its signature binds it.

### Leaf signing

200 leaf certificates for a P-256 leaf key, signed by a CA key:

| CA key | p50 | p90 | p99 | max |
|---|---|---|---|---|
| Secure Enclave | 4.741 ms | 4.816 ms | 4.865 ms | 4.878 ms |
| software | 96 µs | 100 µs | 117 µs | 327 µs |

`wb-proxyd` signs a leaf once per new host name and caches it
(S09-policy-credentials-audit), so 5 ms is added to the first
connection to a host.

## Other projects

Open source projects that use the Secure Enclave, read for this
result:

- [Secretive](https://github.com/maxgoedjen/secretive) keeps each key
  as its CryptoKit `dataRepresentation` in a generic password item in
  the data protection keychain, behind a `keychain-access-groups`
  entitlement and a provisioning profile. Its migration code reads the
  `toid` attribute of keys made by the Keychain and loads it with
  CryptoKit, the same token object ID this spike used. It has no
  ad-hoc path: debug builds are signed with the developer's team.
- [age-plugin-se](https://github.com/remko/age-plugin-se) keeps the
  `dataRepresentation` in a mode 0600 file and is ad-hoc signed. Its
  design notes give the entitlement a permanent key needs as the
  reason it uses CryptoKit. User presence on each use is its default.

Neither uses a keychain file or access-control lists for ad-hoc
binaries, and neither restores a key through Security.framework.

## Limits

- The login keychain wasn't used, so the dialog for the `unlock` item
  there is assumed, not seen.
- Option B of the first decision wasn't run end to end.
- A permanent key without `kSecUseDataProtectionKeychain` wasn't
  tried, because it would have used the login keychain.
- The runs used one Mac and one macOS version.
- The spike didn't confine the binaries. The sandbox rules for
  `securityd` and the Secure Enclave are I61, which builds on this.

## What it means for the specs

- S15-least-privilege: development builds signed by the developer's
  team, the ad-hoc keychain file until X29-dev-signing, what an update
  does to it ("After an update"), no Keychain dialog in any build, and
  the wrapped CA key in "Declared access" (I249).
- S09-policy-credentials-audit: "Storage" for both kinds of build, and
  "Key" for the wrapped Secure Enclave key, the check that catches a
  break of `kSecAttrTokenOID`, and the key on each host (I249).
- S10-tech-stack: the signing of development builds (I249). The cgo
  shim declares `kSecAttrTokenOID`, which S09-policy-credentials-audit
  states.

## Spike code

Throwaway code on branch `spike/x09-keychain-unsigned`, commit
[`cba8309`](https://github.com/wraithbox/wraithbox/tree/cba8309aee9a06e68b5c4c212fb801ece8410362/spikes/x09-keychain-unsigned):
a cgo shim over Security.framework (`shim.c`), the `x9` commands
(`main.go`), a CryptoKit reference (`ckref.swift`), and `build.sh`.

**Status:** Answered 2026-10-13: yes between updates, no across an update, and no permanent Secure Enclave key. Decided 2026-10-15 (I23): 1D, with 1A until X29-dev-signing, and 2A
