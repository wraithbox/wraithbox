# S15 - Least Privilege on the Host

**Purpose:** The security design for SEC12-least-privilege: which
rights Wraith Box takes on the host, where credentials are held, and
how each host process gets only the credentials it needs, for only as
long as it needs them.

**Requirements:** SEC12-least-privilege, with SEC04-no-guest-secrets,
SEC10-audit and SEC11-root-gains-nothing where a credential crosses a
boundary.

**Tests:** C05-credentials-and-store, C09-guest-and-sessions

Brief: B145-least-privilege

## Design

![The secret store is outside every Wraith Box process. wb-credset, which wb runs for wb cred, writes, lists and deletes binding secrets in it. wb-proxyd fetches the binding secrets of the VM's sessions and the wrapped CA key, which only the host's hardware key store can sign with. wb-guestadmin writes and fetches the wbadmin passwords, pipes the build password to the build's wb-vmd and to wb-build-ssh, and writes the old and new password to wb-guestd at a VM's first contact. wb-hostd has no store access and passes descriptors on. A VM that runs project code gets placeholders only.](S15-least-privilege.svg)

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
  team, a process with store access gets the Keychain access groups of
  the items it declares, and no other ("Keychain", below). A new entitlement needs a change here first. Other
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
the holder for another, and none gets a secret from the store through
another. One secret passes between two of them before it is written
to the store: `wb` hands the value the user enters for `wb cred set` to
`wb-credset` on its standard input (rule 5). Keys
that must never leave hardware (the CA signing key) are in the
platform's hardware key store whatever the backend, on every host
that has one (S12-platforms). On
macOS the store holds only the key's wrapped form, which no other Mac
can use (S09-policy-credentials-audit, "Key").

- **Keychain.** The data protection keychain, in every build that a
  team signs, with one access group per role, so each item is reachable
  only by the binaries of its role and there is nothing to unlock
  (decided on I248, B248-dev-signing). Release builds are signed with
  Wraith Box's Developer ID. Development builds are signed with the
  developer's own Apple Development certificate and the provisioning
  profile that Xcode makes for the developer's team (S10-tech-stack,
  "Packaging and signing", S12-platforms, "Development builds on
  macOS"). The groups are named `<TEAMID>.nl.wraithbox.<role>`, where
  `<TEAMID>` is the team that signed the build. So a development
  build's groups differ from a release build's and from another
  developer's. That a program signed by another team can't name them
  is assumed: X29-dev-signing had one team, and a profile grants only
  groups under its own team's prefix. Each program that holds a group
  is a bundle of its own, with the profile embedded (S10-tech-stack).

  | Role | Items | Programs that hold the group |
  |---|---|---|
  | `binding` | `binding/<name>` | `wb-credset`, `wb-proxyd` |
  | `ca` | `ca/<vm>/<ca>` | `wb-proxyd` |
  | `wbadmin` | `wbadmin/build/<build>`, `wbadmin/image/<image>`, `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin` |

  A group lets the programs that hold it read, write and delete its
  items. So an access narrower than that, such as `wb-credset`'s write
  of bindings, is enforced by our code (rule 1). `wb` doesn't hold a group
  (decided on I252). It parses guest terminal output
  (S16-terminal-stream) without a sandbox, so a bug there doesn't reach
  the store (SEC04-no-guest-secrets), and AMFI never refuses to start
  `wb` for a profile.

  `wb-credset` is the helper that `wb cred set`, `wb cred rm` and `wb
  cred list` run as the user. `wb` starts it from the installed bundle
  (S10-tech-stack), with the operation and the binding name as its
  arguments, never the value. It confines itself first thing, from a
  compiled-in profile that allows the secret store service and its
  standard streams, and denies files, the network and starting
  programs (S04-architecture, "Each host daemon is self-sandboxed").
  It does one operation and exits: *write* reads the value from its
  standard input, *delete* removes the item, and *list* prints the
  names of the `binding/` items, never a value. It takes exactly two
  arguments, the operation and the binding name, and an environment
  of only the variable names on a fixed list compiled into it, which
  `wb` also uses when it starts it. It refuses any other argument
  count, any variable not on that list, and an operation or name
  outside the fixed forms of "Declared access", with the rule
  `credset-refused`,
  before any store call.

  Each Keychain call names its group. The cgo shim (S12-platforms)
  sets `kSecAttrAccessGroup` to `<TEAMID>.nl.wraithbox.<role>`, where
  `<role>` is the first part of the item name and `<TEAMID>` is the
  team identifier of the process's own code signature, read once at
  start and never taken from configuration or a request. Without the
  attribute, a write by a process with two groups, such as
  `wb-proxyd`, would go to the first group of its entitlements, so a
  `ca/` item could end up in `binding`. Each call states the group its
  caller expects. Before any Keychain call, the shim refuses with the
  rule `store-group-mismatch`, and logs the item and both groups, when
  that group isn't the one the item name gives, or when the process's
  entitlements don't hold it. An entitlement set the shim can't read
  counts as not holding it.
- **Ad-hoc signed builds.** An ad-hoc signed build, such as a CI
  build or the build of a developer without an Apple ID in Xcode, has
  no Keychain backend.
  Each process with store access reads the team identifier of its own
  code signature at start. With none, it refuses every Keychain access
  with the rule `keychain-needs-team` and logs it. So in such a build
  these are refused with that rule: a session that needs a binding,
  each VM start, because it needs a CA item (S09-policy-credentials-audit,
  "Valid CA"), `wb cred set` through `wb-credset`, and `wb image build` and the `wbadmin`
  rotation at first contact, because they need `wbadmin/*` items. `wb
  setup` refuses to set up the Keychain backend in such a build with
  the same rule, says that the 1Password backend still works, and says
  what to do: sign in to Xcode with an Apple ID, build once in the
  Xcode app, and build Wraith Box with the team set (S12-platforms,
  "Development builds on macOS"). It still does the rest of its work,
  the per-user services included, and then exits with a nonzero
  status.
- **1Password.** The desktop app integration, with one vault per role,
  and no service account token to store. It runs only after the user
  turns it on by name.

A backend must give these guarantees:

| Guarantee | Keychain | 1Password |
|---|---|---|
| G1. A container of Wraith Box items only | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature, per access group. A group's holders reach every item in it ("Keychain", above). Release builds: only Wraith Box signs with its team. Development builds: team-wide. The free profile grants every group under the developer's team (`<TEAMID>.*`), so another program that the developer's team signs can claim Wraith Box's groups (accepted on I248, B248-dev-signing) | enforced by our code only. A compromised declared reader reaches every item in the vaults the session has open. A vault per role narrows it |
| G3. Locked or unreachable fails, with no fallback | yes | yes |
| G4. Values never written outside the store | yes | yes |

A backend without G1, G3 or G4 is refused. For a backend where our code
alone enforces G2, `wb setup`, `wb cred list` and the audit record of
each session start say so. In a development build, one signed by a
team other than Wraith Box's release team, whose ID the build compiles
in, they name the signing team and say that every program that team
signs can reach Wraith Box's groups. A fallback is never silent.

Each failed store call is logged with the call, the item, the status
and the rule, in every build and on every backend:

- A binding that isn't in the store, -25300 (`errSecItemNotFound`) on
  the Keychain, refuses a session start that needs it, and `wb` names
  the binding and says to set it with `wb cred set`.
- A locked store, -25308 on the Keychain, is rule 7, below.
- A CA item follows S09-policy-credentials-audit, "Valid CA".
- Any other failure of a binding or `wbadmin/*` read, such as -34018
  (`errSecMissingEntitlement`), is `store-read-failed`, and the work
  that needed the item is refused with it.
- A failed write or delete of any item, outside the cases above and
  the CA issue of S09-policy-credentials-audit, "Valid CA", is
  `store-write-failed`, and the command or work that made it is
  refused with it.

### Access rules

1. **Declared.** Each process has a declared set of items (the table
   below), each with an access kind: *write*, *delete*, *fetch* (the
   value comes into the process), *list* (item names only, never a
   value), or *use* (the store or the hardware key store signs or
   decrypts in place and keeps the key). Our code refuses an undeclared access where
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
   passes the descriptor on never reads it. The same rule applies to a
   value that is to be written to the store: `wb` reads it from a prompt or its
   own standard input, writes it to `wb-credset`'s standard input,
   keeps it in memory only until that write ends, and never fetches
   it back.
6. **Audited without the value.** Each fetch, use, list, write and delete is an audit
   record with the process, the item name, and the access kind
   (SEC10-audit).
7. **Locked means closed.** Session start, warm start, builds, and
   rotation wait for the user to unlock a locked store, and say which
   store they wait for. Nothing falls back to another place. In every
   build and on every backend, a read that fails because the store is
   locked, -25308 on the Keychain or a locked 1Password, is this
   case, also for a CA item (S09-policy-credentials-audit, "Valid CA").
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
| `binding/<name>` | `wb-credset`, write (`wb cred set`) | the value from `wb`, on `wb-credset`'s standard input | one run |
| `binding/<name>` | `wb-credset`, delete (`wb cred rm`) | none | one run |
| `binding/<name>` | `wb-credset`, list (`wb cred list`): names only, no value is fetched | the names, on its standard output to `wb` | one run |
| `binding/<name>` | `wb-proxyd`, fetch, the bindings of the VM's sessions | sent to the binding's hosts over TLS checked against system roots, per request | while a session that needs it runs in the VM, dropped at suspend |
| `binding/<name>`, OAuth | `wb-proxyd`, fetch, and write the refresh token | the token endpoint the binding names, over TLS | per exchange. Access tokens stay in memory |
| `ca/<vm>/<ca>`, the wrapped CA key (macOS, every build, and Linux with a TPM) | `wb-proxyd`, write when it issues the CA, fetch, delete | to the Secure Enclave as the key's token object ID, or to the TPM as its key blob. The value is useless on another host | the CA's life. `wb-proxyd` deletes it when the CA expires or is dropped (S09-policy-credentials-audit, "Lifetimes") |
| CA signing key (macOS, Windows, and Linux with a TPM) | `wb-proxyd`, use: through the wrapped key, or on Windows by its name in the platform crypto provider. On Windows also delete, of the named key, from the removal list below | none: the key stays in the Secure Enclave or the TPM | the CA's life |
| `ca/<vm>/<ca>`, the software key (Linux hosts without a TPM only) | `wb-proxyd`, write when it issues the CA, fetch, delete | none: it signs in `wb-proxyd` | the CA's life, deleted as above |
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
dropped CA's items go on the same list.

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
  that could use it. C09-guest-and-sessions has the conformance
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
