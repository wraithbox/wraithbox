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
  access fetches it to unlock the file, and it is in "Declared access".
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

This applies to ad-hoc signed builds, until X29-dev-signing answers
yes (decided on I23, B23-keychain-unsigned). A rebuilt binary has a new
cdhash, which no access-control list names, and code can't add it
without a dialog that asks for the keychain file's password
(X09-keychain-unsigned). With dialogs off (access rule 4), every read of
`unlock` and of every item in the file fails at once. The store fails
closed:

- **Refused.** The first refused read is logged with the item and the
  rule `store-needs-setup`. Session starts, warm starts, builds and
  rotation are refused with that rule, and none of them falls back to
  another place (rule 7).
- **Told.** `wb` says that the update locked the store, that it must be
  set up again with `wb setup`, and which bindings to enter again with
  `wb cred set`: each binding that a policy names. `wb-hostd` records in
  `state.db` the build version that made the keychain file
  (S10-tech-stack, "One build version"). `wb setup` replaces the file
  only when the running build's version differs. With the same build, a
  refused read stays an error with its rule.
- **Set up again.** `wb setup` makes a new keychain file and a new
  `unlock` item whose lists name the new binaries, then deletes the old
  file and the old `unlock` item. If the Keychain refuses to delete
  that item with dialogs off, `wb setup` stops, names the item, and the
  user deletes it in Keychain Access.

Every value in the old file is lost. The user enters the bindings
again. The generated items can't be entered again, and `wb setup` says
what each loss means:

| Lost item | Effect | What `wb` does |
|---|---|---|
| `binding/<name>` | a session start that needs it is refused, and the refusal names the binding | lists each binding to enter again |
| `ca/<vm>/<ca>` | `wb-proxyd` can't sign with that CA, and refuses the VM's inspected streams with `store-needs-setup` | `wb-hostd` drops the CAs whose items were in the old file from its record, so the VM's next start finds that it has no valid CA, and `wb-proxyd` issues a new one that `wb-guestd` installs (S09-policy-credentials-audit, "Lifetimes"). `wb` lists each running VM to stop and start |
| `wbadmin/build/<build>` | the build fails, as when its SSH session doesn't complete | says to run `wb image build` again |
| `wbadmin/image/<image>` | no new VM passes first contact on that image | lists each image to build again with `wb image build` |
| `wbadmin/vm/<vm>/<disk>` | `wbadmin`'s secure token keeps working in the guest, but its password is gone, so the later diagnostics path (I158) can't use that disk. `wb-hostd` marks the disk "`wbadmin` password lost" | nothing until the next update replaces the system disk, whose first contact writes a new item |

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
| `ca/<vm>/<ca>`, the wrapped CA key (macOS, every build) | `wb-proxyd`, write when it issues the CA, fetch | to the Secure Enclave, as the key's token object ID. The value is useless on another Mac | the CA's life, removed when `wb-proxyd` destroys the key |
| CA signing key (macOS) | `wb-proxyd`, use, through the wrapped key | none: the key stays in the Secure Enclave | the CA's life |
| `ca/<vm>/<ca>`, the software key (Linux hosts without a TPM only) | `wb-proxyd`, write when it issues the CA, fetch | none: it signs in `wb-proxyd` | the CA's life |
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
