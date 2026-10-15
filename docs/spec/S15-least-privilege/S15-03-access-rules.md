# S15-03 - Access Rules

**Purpose:** How each host process gets only the credentials it needs,
for only as long as it needs them, and which process may access which
item.

## Design

### Access rules

1. **Declared.** Each process has a declared set of items, in the
   table of "Declared access" below. Each has an access kind:
   - *write*;
   - *delete*;
   - *fetch*: the value comes into the process;
   - *list*: item names only, never a value;
   - *use*: the store or the hardware key store signs or decrypts in
     place, and keeps the key.

   Our code refuses an undeclared access where the backend can't, and
   logs it with the rule.
2. **Use over fetch.** Where the store can use a key in place, the
   process doesn't fetch it, as with 1Password's SSH agent signing.
   For the CA key, `wb-proxyd` fetches only the key's wrapped form. The
   Secure Enclave or the TPM signs with it, so the key itself never
   enters the process (S09-06-ca-keys, "Key").
3. **Just in time, sized to the need.** A process fetches when the
   work starts. It keeps the value in memory only, and drops it when
   the work ends. Go and Swift can't reliably wipe memory, so the bound
   is the work's end, or the process's exit.
4. **Few prompts.** A session start fetches every item it needs in one
   batch, so a 1Password unlock asks at most once. `wb-proxyd` doesn't
   fetch per request. A store prompt is never an approval, and is never
   shown in the agent's terminal (SEC14-no-fake-approvals, I51). The
   Keychain doesn't show a dialog in any build ("No Keychain dialogs"
   below).
5. **Consumers that can't read the store.** A program that can't read
   the store itself, such as `wb-vmd` or the system `ssh`, gets the
   value from a declared reader on a pipe or on standard input. It
   never gets it in arguments, the environment, a file, a log line, a
   gRPC message field, an audit record, or an error message. A process
   that only passes the descriptor on never reads it.

   The same rule applies to a value that is to be written to the store.
   `wb` reads it from a prompt or its own standard input. It writes it
   to `wb-credset`'s standard input, and keeps it in memory only until
   that write ends. It never fetches it back.
6. **Audited without the value.** Each fetch, use, list, write and
   delete is an audit record with the process, the item name, and the
   access kind (SEC10-audit).
7. **Locked means closed.** Session start, warm start, builds, and
   rotation wait for the user to unlock a locked store. They say which
   store they wait for. Nothing falls back to another place.

   A read that fails because the store is locked is this case, in
   every build and on every backend. That is -25308 on the Keychain,
   or a locked 1Password. It holds for a CA item too (S09-06-ca-keys, "Valid CA").
8. **Generated items are named and removed.** A secret Wraith Box
   generates is random, from the OS's cryptographic random source. It
   is named for what it belongs to, and removed with it. At start, and
   when a system disk is replaced, `wb-hostd` starts
   `wb-guestadmin-cleanup`. That removes the items of builds, images
   and disks that are gone, and logs each.
9. **Into a guest only before project code.** A credential that opens
   anything outside one VM never enters a guest, which gets a
   placeholder. A `wbadmin` password opens nothing outside its own VM,
   so it isn't a host secret (SEC04-no-guest-secrets). It enters its
   own VM only at first contact, before project code runs.

### No Keychain dialogs

Every process with store access calls
`SecKeychainSetUserInteractionAllowed(false)` before its first store
call. A read that would ask then fails at once instead
(X09-keychain-unsigned).

The call is deprecated and applies to the whole process. A process
that can't make it doesn't touch the store, and logs the rule
`store-dialogs-on`.

### Declared access

Item names start with `wraithbox/`. Each `<…>` part is 1 to 64
characters of `a` to `z`, `0` to `9` and `-`.

| Item | Process, access | Channel to the consumer | Lifetime |
|---|---|---|---|
| `binding/<name>` | `wb-credset`, write (`wb cred set`) | the value from `wb`, on `wb-credset`'s standard input | one run |
| `binding/<name>` | `wb-credset`, delete (`wb cred rm`) | none | one run |
| `binding/<name>` | `wb-credset`, list (`wb cred list`): names only, no value is fetched | the names, on its standard output to `wb` | one run |
| `binding/<name>` | `wb-proxyd`, fetch, the bindings of the VM's sessions | sent to the binding's hosts over TLS checked against system roots, per request | while a session that needs it runs in the VM, dropped at suspend |
| `binding/<name>`, OAuth | `wb-proxyd`, fetch, and write the refresh token | the token endpoint the binding names, over TLS | per exchange. Access tokens stay in memory |
| `ca/<vm>/<ca>`, the wrapped CA key (macOS, every build, and Linux with a TPM) | `wb-proxyd`, write when it issues the CA, fetch, delete | to the Secure Enclave as the key's token object ID, or to the TPM as its key blob. The value is useless on another host | the CA's life. `wb-proxyd` deletes it when the CA expires or is dropped (S09-07-ca-certificates, "Lifetimes") |
| CA signing key (macOS, Windows, and Linux with a TPM) | `wb-proxyd`, use: through the wrapped key, or on Windows by its name in the platform crypto provider. On Windows also delete, of the named key, from the removal list below | none: the key stays in the Secure Enclave or the TPM | the CA's life |
| `ca/<vm>/<ca>`, the software key (Linux hosts without a TPM only) | `wb-proxyd`, write when it issues the CA, fetch, delete | none: it signs in `wb-proxyd` | the CA's life, deleted as above |
| `wbadmin/build/<build>` | `wb-guestadmin-build`, write, then fetch. `wb-guestadmin-rotate`, fetch at the build VM's first contact | a pipe to the build's `wb-vmd` (until the start call returns), `ssh`'s standard input (one session), the guest connection of S15-04-guest-admin | removed when the build ends |
| `wbadmin/image/<image>` | `wb-guestadmin-rotate`, write at the build VM's first contact, fetch at each VM's first contact | the guest connection of S15-04-guest-admin | removed with the image |
| `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin-rotate`, write at first contact | the guest connection of S15-04-guest-admin | kept for the secure token and a later diagnostics path (I158), removed when the system disk is replaced |

Which `wbadmin` item is current for a disk isn't secret. So `wb-hostd`
keeps that marker in `state.db`, and never stores a value.

### Removing CA keys

When a CA expires or a VM is deleted, `wb-hostd` moves the names of
its CA keys to a removal list in `state.db`. Those are the items, or
on Windows the named keys. A dropped CA's items go on the same list.

1. `wb-proxyd` deletes each listed item when it runs, and at its next
   start when it doesn't.
2. `wb-hostd` drops an entry once `wb-proxyd` reports it deleted.

An item that is already gone counts as deleted. On Windows the named
key's name in the platform crypto provider is the item name
`ca/<vm>/<ca>`.

`wb-proxyd` checks each batch before it deletes anything:

- every entry must have the form `ca/<vm>/<ca>`, with the parts of
  "Declared access";
- its CA must not be in `wb-hostd`'s record.

Otherwise `wb-proxyd` refuses the batch with the rule
`ca-removal-refused`. It logs each entry that failed, and deletes
nothing from that batch. `wb-hostd` then removes the refused entries
from the list, and logs that. It sends the others in the next batch,
so one refused entry doesn't hold back the rest.

- The form check is what limits a compromised `wb-hostd`
  (T16-hostd-landing), which faces the guest.
- The record check uses `wb-hostd`'s own record, so it catches bugs.

Through the list, a compromised `wb-hostd` can at worst delete live CA
key items. That costs availability: the VM's start is then refused
with `ca-key-restore`.

### Passwords in remote URLs

A password in a host remote URL isn't a store item. `wb` removes the
user name and password from every remote URL it reads, withheld ones
included. It does that before the URL goes anywhere, the audit log
among them (S08-01-into-the-guest, "Remote URLs").

**Status:** Draft
