# S15 - Least Privilege on the Host

**Purpose:** The security design for SEC12-least-privilege: which
rights Wraith Box takes on the host, where credentials are held, and
how each host process gets only the credentials it needs, for only as
long as it needs them.

**Requirements:** SEC12-least-privilege, with SEC04-no-guest-secrets,
SEC10-audit and SEC11-root-gains-nothing where a credential crosses a
boundary.

Brief: B145-least-privilege

## Design

![The secret store is outside every Wraith Box process. wb writes binding secrets to it. wb-proxyd fetches the binding secrets of the VM's sessions and uses the CA key in place. wb-guestadmin writes and fetches the wbadmin passwords, pipes the build password to the build's wb-vmd and to wb-build-ssh, and writes the old and new password to wb-guestd at a VM's first contact. wb-hostd has no store access and passes descriptors on. A VM that runs project code gets placeholders only.](S15-least-privilege.svg)

### Rights

- **No root or administrator rights at run time.** The processes of
  S04-architecture's table run as the user. The packet transport and
  the userspace network stack replace the host networking features that
  would need root. The image build never writes to a guest disk from
  the host, so it doesn't need root either (X17-image-build).
- **No extra host user accounts.** Wraith Box's own processes are kept
  apart by self-sandboxing (S04-architecture, "Each host daemon is
  self-sandboxed"), and projects by guest users (SEC08-proj-isolation).
- **Hardening and entitlements.** Every binary in the bundle is signed
  with the hardened runtime (S10-tech-stack). On macOS, `wb-vmd` alone
  has `com.apple.security.virtualization`, and no process has
  `com.apple.vm.networking` (X25-vmd-sandbox). A process that reads the
  store gets the Keychain access group of its role once a signing
  identity exists. A new entitlement needs a change here first. Other
  hosts use the equivalents in S12-platforms.
- **One-time prerequisites.** `wb setup` checks the prerequisites of
  S12-platforms, explains each missing one, and says who can fix it.
  The user makes each change, and `wb setup` doesn't ask for an
  administrator password. A prerequisite that needs administrator rights
  on every run needs a change to this spec first.

### The store holds credentials

Every credential is nominally held by the configured secret store, in a
container that holds only Wraith Box's items. No Wraith Box process is
the holder for another, and none gets a secret through another. Keys
that must never leave hardware (the CA signing key) are in the
platform's hardware key store whatever the backend (S12-platforms).

- **Keychain.** With signed builds, the data protection keychain, with
  one access group per role, so each item is reachable only by the
  binaries of its role and there is nothing to unlock. Ad-hoc signed
  builds are for development only. They use a dedicated keychain file,
  whose password is the one Wraith Box item outside it, `wraithbox/unlock`
  in the login keychain. Each process with store access fetches it to
  unlock the file, and it is in "Declared access".
- **1Password.** The desktop app integration, with one vault per role,
  and no service account token to store. It runs only after the user
  turns it on by name.

A backend must give these guarantees:

| Guarantee | Keychain, signed | Keychain, ad-hoc signed | 1Password |
|---|---|---|---|
| G1. A container of Wraith Box items only | yes | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature | enforced by our code only. A compromised declared reader reaches every item. Each update prompts again (X09-keychain-unsigned) | enforced by our code only. A compromised declared reader reaches every item in the vaults the session has open. A vault per role narrows it |
| G3. Locked or unreachable fails, with no fallback | yes | yes | yes |
| G4. Values never written outside the store | yes | yes | yes |

A backend without G1, G3 or G4 is refused. For a backend or build where
our code alone enforces G2, `wb setup`, `wb cred list` and the audit
record of each session start say so, in the same words for 1Password
and for an ad-hoc signed build. A fallback is never silent.

### Access rules

1. **Declared.** Each process has a declared set of items (the table
   below), each with an access kind: *write*, *fetch* (the value comes
   into the process), or *use* (the store signs or decrypts in place
   and keeps the value). Our code refuses an undeclared access where
   the backend can't, and logs it with the rule.
2. **Use over fetch.** Where the store can use a key in place, the
   process doesn't fetch it: Secure Enclave signing, and 1Password's SSH
   agent signing.
3. **Just in time, sized to the need.** A process fetches when the work
   starts, keeps the value in memory only, and drops it when the work
   ends. Go and Swift can't reliably wipe memory, so the bound is the
   work's end, or the process's exit.
4. **Few prompts.** A session start fetches every item it needs in one
   batch, so a 1Password unlock asks at most once. `wb-proxyd` doesn't
   fetch per request. A store prompt is never an approval, and is never shown in
   the agent's terminal (SEC14-no-fake-approvals, I51). With ad-hoc
   signed builds the Keychain asks once per item after each update.
   Signed builds remove that.
5. **Consumers that can't read the store.** A program that can't read
   the store itself, such as `wb-vmd` or the system `ssh`, gets the value
   from a declared reader on a pipe or on standard input. Never in
   arguments, the environment, a file, a log line, a gRPC message
   field, an audit record, or an error message. A process that only
   passes the descriptor on never reads it.
6. **Audited without the value.** Each fetch, use and write is an audit
   record with the process, the item name, and the access kind
   (SEC10-audit).
7. **Locked means closed.** Session start, warm start, builds, and
   rotation wait for the user to unlock a locked store, and say which
   store they wait for. Nothing falls back to another place.
8. **Generated items are named and removed.** A secret Wraith Box
   generates is random, from the OS's cryptographic random source,
   named for what it belongs to, and removed with it. At start, and
   when a system disk is replaced, `wb-hostd` starts
   `wb-guestadmin-cleanup`, which removes the items of builds, images
   and disks that are gone, and logs each.
9. **Into a guest only before project code.** A credential that opens
   anything outside one VM never enters a guest, which gets a
   placeholder. A `wbadmin` password opens nothing outside its own VM,
   so it isn't a host secret (SEC04-no-guest-secrets). It enters its
   own VM only at first contact, before project code runs.

### Declared access

Item names start with `wraithbox/`. Each `<…>` part is 1 to 64 characters of `a` to `z`, `0` to `9` and `-`.

| Item | Process, access | Channel to the consumer | Lifetime |
|---|---|---|---|
| `binding/<name>` | `wb`, write (`wb cred set`) | the value from a prompt or standard input | one command |
| `binding/<name>` | `wb-proxyd`, fetch, the bindings of the VM's sessions | sent to the binding's hosts over TLS checked against system roots, per request | while a session that needs it runs in the VM, dropped at suspend |
| `binding/<name>`, OAuth | `wb-proxyd`, fetch, and write the refresh token | the token endpoint the binding names, over TLS | per exchange. Access tokens stay in memory |
| CA signing key | `wb-proxyd`, use | none: the key stays in the Secure Enclave | the CA's life |
| `ca/<vm>/<ca>`, the software fallback key | `wb-proxyd`, fetch | none: it signs in `wb-proxyd` | the CA's life |
| `unlock`, ad-hoc signed builds only | every process with store access, fetch | none: it unlocks the dedicated keychain file | the process's life |
| `wbadmin/build/<build>` | `wb-guestadmin-build`, write, then fetch. `wb-guestadmin-rotate`, fetch at the build VM's first contact | a pipe to the build's `wb-vmd` (until the start call returns), `ssh`'s standard input (one session), the guest connection below | removed when the build ends |
| `wbadmin/image/<image>` | `wb-guestadmin-rotate`, write at the build VM's first contact, fetch at each VM's first contact | the guest connection below | removed with the image |
| `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin-rotate`, write at first contact | the guest connection below | kept for the secure token and a later diagnostics path (I158), removed when the system disk is replaced |

Which `wbadmin` item is current for a disk isn't secret, so `wb-hostd`
keeps that marker in `state.db`, and never stores a value.

A password in a host remote URL isn't a store item. `wb` removes the
user name and password from every remote URL it reads, withheld ones
included, before the URL goes anywhere, the audit log among them
(S08-workspace-and-git, "Remote URLs").

### The guest admin user

The provisioning options create the user `wbadmin`, the volume's only
secure-token holder, which macOS refuses to delete (X17-image-build).
It stays in the image as a host-administered account: disabled,
hidden, without a shell and out of `admin` (S06-vm-lifecycle,
"Sealing"). The host keeps its password in the store, and the secure
token keeps working. That keeps SEC11-root-gains-nothing (guest root
gains no control over the host), because the asset never leaves its
VM. The password and the token unlock only that VM's disk, nothing
they unlock crosses the Virtualization boundary, and no VM can reach
another VM's `wbadmin`. Guest root can't read the password back,
because the guest keeps only what macOS keeps for any account, and the
build deletes `wbadmin`'s keychains before sealing. `wb-guestadmin`
(S04-architecture) does each step below, and only it reads or writes
`wbadmin/*`.

- **Build.** `wb-guestadmin-build` generates the build password and
  writes `wbadmin/build/<build>`. `wb-hostd` creates two pipes, and
  `wb-launcher` hands one read end to the build's `wb-vmd` and the other
  to `wb-build-ssh` as its standard input. `wb-guestadmin-build` writes
  the password to the first, and two newline-terminated copies to the
  second.
- **The build's SSH session.** `wb-build-ssh` (S04-architecture) runs
  the system `ssh` with `-F /dev/null -o UserKnownHostsFile=/dev/null
  -o IdentitiesOnly=yes`, with `SSH_ASKPASS_REQUIRE=force`, and with
  `SSH_ASKPASS` set to the path of `wb-askpass` in the bundle, a
  program path and not the value. `wb-askpass` reads one line from its
  standard input, the first copy, and prints it for the login. The
  second line reaches the guest's `sudo -S` on the session's standard
  input. Standard input was chosen over answering the prompt through a
  terminal, because the password then never passes through a terminal
  stream. The session runs only on the build VM's own file-handle
  network through the host's userspace stack, which no third party can
  join, so the channel authenticates the guest (S06-vm-lifecycle,
  "Images"). A session that doesn't complete burns the build password.
  The build then fails, its bundle is deleted, and cleanup removes its
  items. The build never writes the password into the build bundle, and
  its test searches the bundle for it.
- **One password per VM.** Every system disk is a clone of the image.
  At first contact in each fresh clone, `wb-guestadmin-rotate` runs
  before any other request: before `wb-hostd` sends the time and before
  `wb-guestd` lists its sessions (S06-vm-lifecycle). It refuses when the
  store already holds `wbadmin/vm/<vm>/<disk>`, whatever `wb-hostd` or
  the guest reports. Otherwise it fetches the image's item, generates
  the new password, writes `wbadmin/vm/<vm>/<disk>`, and sends the old
  and new password to `wb-guestd` on a host-guest connection of its
  own, to a port below 1024 that serves only this. It never reads from
  that connection. `wb-guestd`, as root, enables `wbadmin`, runs
  `sysadminctl -resetPasswordFor wbadmin -newPassword - -adminUser wbadmin -adminPassword -`
  with both passwords on standard input, disables `wbadmin` again, and
  reports the result to `wb-hostd`. On success, `wb-hostd` marks the VM
  item current. The build VM rotates the same way, from the build
  password to the image password.
- **Rotation failure.** On a failed report or no answer, both items
  stay, `wb-hostd` marks the disk "rotation unknown", and every session
  start in that VM is refused with the rule. Recovery replaces the
  system disk, and cleanup removes the disk's item.
- **What first contact relies on.** `wb-guestd` is honest at first
  contact, because the disk is a fresh clone of a sealed image and no
  project code has run on it. Nothing from the data disk runs before
  rotation, because `wb-guestd` starts no project process and no
  session until rotation has succeeded, and no user is logged in.
  `wbadmin` is enabled only during the reset, when nothing else runs
  that could use it. S11-verification-and-spikes has the conformance
  case.
- **Break-glass.** When `wb-guestd` is dead or wedged, `wb-hostd`
  replaces the VM's system disk with a fresh clone of the image. The
  data disk stays, and first contact rotates again. No password is
  fetched and nobody logs in. `wb-guestadmin-cleanup` removes the old
  disk's item.
- **Refused outside the path.** `wb-vmd` refuses provisioning options
  for any VM that isn't a new build bundle under `<data>/images/`.
  `wb-launcher` and `wb-guestadmin` accept only requests whose fields
  are item names in the fixed format above. Each refusal is logged with
  its rule.
- **Not readable in the image.** No automatic login, no
  `/etc/kcpassword`, and no keychain of `wbadmin`'s, which the sealing
  scan checks (S06-vm-lifecycle).

## Out of scope

- Which login Wraith Box owns for a Claude subscription
  (X01-model-credential).
- Project users' accounts in the guest, which have no password the host
  keeps.
- The other `SEC*` controls.

**Status:** Draft
