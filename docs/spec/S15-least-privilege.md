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

![The secret store is outside every Wraith Box process. wb writes binding secrets to it. wb-proxyd fetches the binding secrets of the VM's sessions and the wrapped CA key, which only the host's hardware key store can sign with. wb-guestadmin writes and fetches the wbadmin passwords, pipes the build password to the build's wb-vmd and to wb-build-ssh, and writes the old and new password to wb-guestd at a VM's first contact. wb-hostd has no store access and passes descriptors on. A VM that runs project code gets placeholders only.](S15-least-privilege.svg)

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
  `com.apple.vm.networking` (X25-vmd-sandbox). In a build signed by a
  team, a process that reads the store gets the Keychain access group
  of its role ("Keychain", below). A new entitlement needs a change here first. Other
  hosts use the equivalents in S12-platforms.
- **The release check.** `wb-release-check` (S04-architecture,
  S14-claude-in-guest) doesn't declare a store item or an entitlement.
  Its compiled-in profile allows only its inherited descriptors, and
  denies files, the network, the secret store, and starting programs.
  The Claude Code release key compiled into it is public.
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
platform's hardware key store whatever the backend, on every host
that has one (S12-platforms). On
macOS the store holds only the key's wrapped form, which no other Mac
can use (S09-policy-credentials-audit, "Key").

- **Keychain.** The data protection keychain, with one access group per
  role, so each item is reachable only by the binaries of its role and
  there is nothing to unlock. Release builds are signed with Wraith
  Box's Developer ID. Development builds are signed with the developer's
  own Apple Development certificate and a provisioning profile that
  grants the access groups, and use the same keychain (decided on I23,
  B23-keychain-unsigned). That waits for X29-dev-signing, which asks
  whether a free personal team can grant the access groups to the
  helpers in `Wraith Box.app`.
- **Keychain, ad-hoc signed.** Until X29-dev-signing answers yes,
  development builds are ad-hoc signed. They use a dedicated keychain
  file, whose password is the one Wraith Box item outside it,
  `wraithbox/unlock` in the login keychain. Each process with store
  access fetches it to unlock the file, and "Declared access" lists it.
  Each item's access-control list pins every declared binary by its
  cdhash. An update changes the cdhash and locks every item ("After an
  update", below).
- **1Password.** The desktop app integration, with one vault per role,
  and no service account token to store. It runs only after the user
  turns it on by name.

A backend must give these guarantees:

| Guarantee | Keychain, signed by a team | Keychain, ad-hoc signed | 1Password |
|---|---|---|---|
| G1. A container of Wraith Box items only | yes | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature | enforced by our code only. A compromised declared reader reaches every item. After each update every read fails, with no dialog, until the store is set up again ("After an update") | enforced by our code only. A compromised declared reader reaches every item in the vaults the session has open. A vault per role narrows it |
| G3. Locked or unreachable fails, with no fallback | yes | yes | yes |
| G4. Values never written outside the store | yes | yes | yes |

A backend without G1, G3 or G4 is refused. For a backend or build where
our code alone enforces G2, `wb setup`, `wb cred list` and the audit
record of each session start say so, in the same words for 1Password
and for an ad-hoc signed build. A fallback is never silent.

### After an update

This applies to ad-hoc signed builds that use the Keychain backend,
until X29-dev-signing answers yes (decided on I23,
B23-keychain-unsigned). With 1Password nothing locks at an update, and
no CA is dropped. A rebuilt binary has a new
cdhash, which no access-control list names, and code can't add it
without a dialog that asks for the keychain file's password
(X09-keychain-unsigned). With dialogs off (access rule 4), every read of
`unlock` and of every item in the file fails at once. The store fails
closed:

- **Status codes.** These apply to the read of `unlock`, the unlock of
  the file, and the read of a CA item in `wb-hostd`'s record. A binding
  that isn't in the store gets the refusal of the table below instead.
  For a CA item only -25293 and -25308 apply, and every other status,
  -25300 included, is `ca-key-restore` (S09-policy-credentials-audit,
  "Valid CA"). Each log line names the call that failed, the item, the
  status, and the rule.
  - -25293 (`errSecAuthFailed`, what X09-keychain-unsigned measured
    for a binary the list doesn't name): `store-needs-setup`.
  - -25300 (`errSecItemNotFound`) for `unlock` while the record names a
    keychain file: `store-needs-setup`. That is the state a set-up
    leaves when it stops after its first step.
  - -25308 (`errSecInteractionNotAllowed`, a locked keychain): rule 7.
    The work waits for the user to unlock it, and `wb` names the
    keychain it waits for.
  - Any other status for `unlock` or the file: `store-read-failed`.

  Only `store-needs-setup` leads to setting the store up again.
- **Refused.** Session starts, warm starts, builds, and rotation are
  refused with the rule, and none of them falls back to another place
  (rule 7).
- **Told.** `wb` says that the update locked the store, that `wb setup`
  sets it up again, and which bindings to enter again with `wb cred
  set`: each binding that a policy names.
- **Version gate.** `wb-hostd` keeps in `state.db` the name of the
  keychain file and the build version that made it (S10-tech-stack,
  "One build version"). `wb setup` sets the store up again on its own
  only when reads fail with `store-needs-setup` and the running
  build's version differs from the recorded one. A build of the same
  version with a new cdhash, such as a binary signed again, keeps the
  refusal, and so does a keychain file with no record. For those,
  `wb setup --reset-store` prints the table below for this store and
  asks for confirmation. A person has to confirm, so `wb` refuses
  `--reset-store` without a terminal. Only `wb` on the host runs it,
  never a guest RPC. A `--reset-store` run that stopped needs
  `--reset-store` again.
- **Keychain files.** Each one is in `<data>/keychains/`, never in
  `~/Library/Keychains`, and named `store-<id>.keychain-db`, where
  `<id>` is 16 lowercase hex digits from the OS's cryptographic random
  source.
- **Set up again,** in this order, so that an interrupted run leaves
  nothing that needs a dialog, and the next `wb setup` finishes it:
  1. `wb setup` deletes the old `unlock` item. If the Keychain refuses
     with dialogs off, `wb setup` stops, names the item, and the user
     deletes it in Keychain Access and runs `wb setup` again. An item
     that is already gone counts as deleted.
  2. It makes a keychain file under a new name and writes the new
     `unlock` item, both with lists that name the new binaries.
  3. `wb-hostd` records the new file's name and the running build
     version, and drops every CA of its record (the table below), with
     the 5019 event of S09-policy-credentials-audit, "Recovery".
  4. `wb setup` deletes the old file.
- **Unrecorded files.** Every `wb setup` run, also one that finds the
  store readable, deletes each regular file in `<data>/keychains/`
  whose name has the form above and that the record doesn't name. Such
  a file is Wraith Box's. It checks each entry with `lstat` first. A
  symbolic link, a directory, or any other entry that isn't a regular
  file is foreign, and so is a file with another name. `wb setup`
  leaves each foreign entry, and a link's target, and logs it. It logs
  each deletion.

Every value in the old file is lost. The user enters the bindings
again. The generated items can't be entered again, and `wb setup` says
what each loss means:

| Lost item | Effect | What `wb` does |
|---|---|---|
| `binding/<name>` | `wb` refuses a session start that needs it, and names the binding | lists each binding to enter again |
| `ca/<vm>/<ca>` | `wb-proxyd` can't sign with that CA, and refuses the VM's inspected streams with `store-needs-setup` | Every item was in the old file, so `wb-hostd` drops every CA from its own record, without reading the store. At each VM's next start `wb-proxyd` issues a new CA, and the install rewrites the guest's trust files whole from the record (S09-policy-credentials-audit, "Guest trust"), which removes the dropped CAs. A VM saved before the drop cold boots: its saved state is deleted, as for any saved state of another build version (S06-vm-lifecycle). `wb` lists each running VM to stop and start |
| `wbadmin/build/<build>` | the build fails, as when its SSH session doesn't complete | says to run `wb image build` again |
| `wbadmin/image/<image>` | no new VM passes first contact on that image | lists each image to build again with `wb image build` |
| `wbadmin/vm/<vm>/<disk>` | `wbadmin`'s secure token keeps working in the guest, but its password is gone, so the later diagnostics path (I158) can't use that disk. `wb-hostd` marks the disk "`wbadmin` password lost" in `state.db` | nothing until the next update replaces the system disk, whose first contact writes a new item |

### Access rules

1. **Declared.** Each process has a declared set of items (the table
   below), each with an access kind: *write*, *delete*, *fetch* (the
   value comes into the process), or *use* (the store or the hardware
   key store signs or decrypts in place and keeps the key). Our code refuses an undeclared access where
   the backend can't, and logs it with the rule.
2. **Use over fetch.** Where the store can use a key in place, the
   process doesn't fetch it, as with 1Password's SSH agent signing. For
   the CA key, `wb-proxyd` fetches only the key's wrapped form, and the
   Secure Enclave or the TPM signs with it, so the key itself never
   enters the process (S09-policy-credentials-audit, "Key").
3. **Just in time, sized to the need.** A process fetches when the work
   starts, keeps the value in memory only, and drops it when the work
   ends. Go and Swift can't reliably wipe memory, so the bound is the
   work's end, or the process's exit.
4. **Few prompts.** A session start fetches every item it needs in one
   batch, so a 1Password unlock asks at most once. `wb-proxyd` doesn't
   fetch per request. A store prompt is never an approval, and is never shown in
   the agent's terminal (SEC14-no-fake-approvals, I51). The Keychain
   doesn't show a dialog in any build: every process with store access calls
   `SecKeychainSetUserInteractionAllowed(false)` before its first store
   call, so a read that would ask fails at once instead
   (X09-keychain-unsigned). The call is deprecated and applies to the
   whole process. A process that can't make it doesn't touch the store,
   and logs the rule `store-dialogs-on`.
5. **Consumers that can't read the store.** A program that can't read
   the store itself, such as `wb-vmd` or the system `ssh`, gets the value
   from a declared reader on a pipe or on standard input. Never in
   arguments, the environment, a file, a log line, a gRPC message
   field, an audit record, or an error message. A process that only
   passes the descriptor on never reads it.
6. **Audited without the value.** Each fetch, use, write and delete is an audit
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
| `ca/<vm>/<ca>`, the wrapped CA key (macOS, every build, and Linux with a TPM) | `wb-proxyd`, write when it issues the CA, fetch, delete | to the Secure Enclave as the key's token object ID, or to the TPM as its key blob. The value is useless on another host | the CA's life. `wb-proxyd` deletes it when the CA expires or is dropped (S09-policy-credentials-audit, "Lifetimes") |
| CA signing key (macOS, Windows, and Linux with a TPM) | `wb-proxyd`, use: through the wrapped key, or on Windows by its name in the platform crypto provider. On Windows also delete, of the named key, from the removal list below | none: the key stays in the Secure Enclave or the TPM | the CA's life |
| `ca/<vm>/<ca>`, the software key (Linux hosts without a TPM only) | `wb-proxyd`, write when it issues the CA, fetch, delete | none: it signs in `wb-proxyd` | the CA's life, deleted as above |
| `unlock`, ad-hoc signed builds only | `wb`, write and delete (`wb setup`, "After an update") | none: `wb setup` makes the keychain file with it | until the store is set up again |
| `unlock`, ad-hoc signed builds only | every process with store access, fetch | none: it unlocks the dedicated keychain file | the process's life |
| `wbadmin/build/<build>` | `wb-guestadmin-build`, write, then fetch. `wb-guestadmin-rotate`, fetch at the build VM's first contact | a pipe to the build's `wb-vmd` (until the start call returns), `ssh`'s standard input (one session), the guest connection below | removed when the build ends |
| `wbadmin/image/<image>` | `wb-guestadmin-rotate`, write at the build VM's first contact, fetch at each VM's first contact | the guest connection below | removed with the image |
| `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin-rotate`, write at first contact | the guest connection below | kept for the secure token and a later diagnostics path (I158), removed when the system disk is replaced |

Which `wbadmin` item is current for a disk isn't secret, so `wb-hostd`
keeps that marker in `state.db`, and never stores a value.

When a CA expires or a VM is deleted, `wb-hostd` moves the names of
its CA keys, the items or on Windows the named keys, to a removal list
in `state.db`. `wb-proxyd` deletes each
listed item when it runs, and at its next start when it doesn't, and
`wb-hostd` drops an entry once `wb-proxyd` reports it deleted. An item
that is already gone counts as deleted. On Windows the named key's
name in the platform crypto provider is the item name `ca/<vm>/<ca>`.
`wb-proxyd` checks each batch before it deletes anything. Every entry
must have the form `ca/<vm>/<ca>` with the parts of "Declared access",
and its CA must not be in `wb-hostd`'s record. Otherwise `wb-proxyd`
refuses the batch with the rule `ca-removal-refused`, logs each entry
that failed, and deletes nothing from that batch. `wb-hostd` then
removes the refused entries from the list, logs that, and sends the
others in the next batch, so one refused entry doesn't hold back the
rest. The form
check is what limits a compromised `wb-hostd` (T16-hostd-landing),
which faces the guest. The record check uses `wb-hostd`'s own record,
so it catches bugs. Through the list, a compromised `wb-hostd` can at
worst delete live CA key items, which costs availability: the VM's
start is then refused with `ca-key-restore`. A
dropped CA's items go on the same list. After an update of an ad-hoc
build, the old keychain file goes with its items instead.

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
  unless its slot is `build`, the slot of `wb-vmd-install`, whose only
  bundle is the new build bundle under `<data>/images/`, and logs
  `vmd-provisioning-refused` (S04-architecture).
  `wb-launcher` and `wb-guestadmin` accept only requests whose fields
  are item names in the fixed format above. Each refusal is logged with
  its rule.
- **Not readable in the image.** No automatic login, no
  `/etc/kcpassword`, and no keychain of `wbadmin`'s, which the sealing
  scan checks (S06-vm-lifecycle).

## Out of scope

- The browser login for a Claude subscription (I15). The long-lived
  token is in S09-policy-credentials-audit, "Model credential".
- Project users' accounts in the guest, which have no password the host
  keeps.
- The other `SEC*` controls.

**Status:** Draft
