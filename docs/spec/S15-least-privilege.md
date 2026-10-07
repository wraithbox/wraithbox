# S15 - Least Privilege on the Host

**Purpose:** The security design for SEC12-least-privilege: which
rights Wraith Box takes on the host, where credentials are held, and
how each host process gets only the credentials it needs, for only as
long as it needs them.

**Requirements:** SEC12-least-privilege, with SEC04-no-guest-secrets,
SEC10-audit and SEC11-root-gains-nothing where a credential crosses a
boundary.

Brief: B145-least-privilege

## For review

- **Decides:** how SEC12-least-privilege is met as a whole. Credentials
  are held by the configured secret store, the macOS Keychain first,
  and each host process gets a declared set of items, just in time.
  No Wraith Box process holds secrets for another. The guest admin
  user `wbadmin` is the worked example.
- **You are approving:** no root, no extra host accounts, the
  entitlement table, `wb setup` as the only place for one-time
  prerequisites, the store as nominal holder, the guarantees a store
  backend must give, the access rules, and the table in "Declared
  access". S04-architecture's "Secrets live in one process" is
  superseded.
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host) is reworded from "held by exactly one host process" to
  "nominally held by the configured secret store, with a declared set
  of items per process". SEC11-root-gains-nothing (guest root doesn't
  control the host) is kept: the host already has guest root through
  `wb-guestd`, so a guest admin password in the host's store is no
  escalation. SEC04-no-guest-secrets (no secrets in the guest) is kept
  for every credential that opens anything outside one VM.
- **Assumed:** that Virtualization doesn't write the provisioning
  password into the VM bundle (open question 8). That `sysadminctl`
  reads both the new and the old password from standard input when each
  is `-`. That a disabled account can authorize its own password
  change. That 1Password's desktop app authorizes its CLI for 10
  minutes after use, up to 12 hours.
- **Open decisions:** 1 to 7 under "Open questions", each with a
  recommendation. Question 8 is a check.
- **Brief:** B145-least-privilege

## Design

![The secret store is outside every Wraith Box process. wb writes binding secrets to it. wb-proxyd fetches the binding secrets of the VM's sessions and uses the CA key in place. wb-guestadmin writes and fetches the wbadmin passwords, pipes the build password to the build's wb-vmd and SSH client, and writes the old and new password to wb-guestd at a VM's first contact. wb-hostd has no store access and passes descriptors on. A VM that runs project code gets placeholders only.](S15-least-privilege.svg)

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
  on every run is a spec change, not a workaround.

### The store holds credentials

Every credential is nominally held by the configured secret store, in a
container that holds only Wraith Box's items: a dedicated keychain, or
a dedicated 1Password vault per role. Keys that must never leave
hardware (the CA signing key) are in the platform's hardware key store
whatever the backend (S12-platforms). No Wraith Box process is the
holder for another, and none gets a secret through another.

A backend must give these guarantees:

| Guarantee | Keychain, signed | Keychain, ad-hoc signed | 1Password |
|---|---|---|---|
| G1. A container of Wraith Box items only | yes | yes | yes, a vault |
| G2. Each item reachable only by its declared processes | yes, by code signature | weaker: by the binary's hash. Each update prompts again (X09-keychain-unsigned) | no: by vault and session. A vault per role narrows it |
| G3. Locked or unreachable fails, with no fallback | yes | yes | yes |
| G4. Values never written outside the store | yes | yes | yes |

A backend without G1, G3 or G4 is refused. A backend that gives G2
only per vault runs only after the user turns it on by name, and
`wb setup`, `wb cred list` and the audit log of each session start say
which guarantee is weaker. No fallback is silent.

### Access rules

1. **Declared.** Each process has a declared set of items (the table
   below), each with an access kind: *write*, *fetch* (the value comes
   into the process), or *use* (the store signs or decrypts in place
   and keeps the value). Our code refuses an undeclared access
   even where the backend can't, and logs it with the rule.
2. **Use over fetch.** Where the store can use a key in place, the
   process doesn't fetch it: Secure Enclave signing, and 1Password's SSH
   agent signing.
3. **Just in time, sized to the need.** A process fetches when the work
   starts, keeps the value in memory only, and drops it when the work
   ends. Go and Swift can't reliably wipe memory, so the bound is the
   work's end, or the process's exit.
4. **Few prompts.** A session start fetches every item it needs in one
   batch, so a 1Password unlock asks once. `wb-proxyd` doesn't fetch per
   request. A store prompt is never an approval, and is never shown in
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
   named for what it belongs to, and removed with it. At start,
   `wb-hostd` asks `wb-guestadmin` to remove items whose image or VM is
   gone, and logs each.
9. **Into a guest only before project code, or as a burn.** A
   credential that opens anything outside one VM never enters a guest,
   which gets a placeholder (SEC04-no-guest-secrets). A `wbadmin`
   password enters its own guest at first contact, before project code,
   or at a break-glass login, which burns it.

### Declared access

| Item | Process, access | Channel to the consumer | Lifetime | Status |
|---|---|---|---|---|
| `binding/<name>` | `wb`, write (`wb cred set`) | the value from a prompt or standard input | one command | instance |
| `binding/<name>` | `wb-proxyd`, fetch, the bindings of the VM's sessions | sent to the binding's hosts over TLS checked against system roots, per request | while a session that needs it runs in the VM, dropped at suspend | instance. G2 on Keychain waits for X09-keychain-unsigned and I61 |
| `binding/<name>`, OAuth | `wb-proxyd`, fetch, and write the refresh token | the binding's token endpoint, over TLS | per exchange. Access tokens stay in memory | instance, once a binding names its token endpoint |
| CA signing key | `wb-proxyd`, use | none: the key stays in the Secure Enclave | the CA's life | instance. The software fallback is a fetch, weaker |
| `wbadmin/build/<build>` | `wb-guestadmin`, write, then fetch | pipes to the build's `wb-vmd` (until the start call returns) and its SSH client (one session) | removed when the build ends | instance (open questions 1 and 2) |
| `wbadmin/image/<image>` | `wb-guestadmin`, write at the build, fetch at each VM's first contact | the guest connection below | removed with the image | instance |
| `wbadmin/vm/<vm>/<disk>` | `wb-guestadmin`, write at first contact, fetch to rotate again | the guest connection below | removed when the system disk is replaced | instance |
| `wbadmin/vm/<vm>/<disk>` | `wb`, fetch, break-glass | shown to the user | one login, then burned | open question 4 |
| Password in a host remote URL | not a store item. `wb` removes it from each URL it reads | | at once | open question 7 |
| Claude subscription login | open | | | X01-model-credential |

### Worked example: the guest admin user

The provisioning options create the user `wbadmin`, the volume's only
secure-token holder, which macOS refuses to delete (X17-image-build).
It stays in the image as a host-administered account. The host keeps
its password in the store, the secure token keeps working, and the
host alone can unlock it. The host already has guest root through
`wb-guestd`, so this gives the host no more than it has.

- **Build.** `wb-guestadmin` generates the build password and writes
  `wbadmin/build/<build>`. `wb-hostd` creates two pipes and has
  `wb-launcher` hand their read ends to the build's `wb-vmd` and SSH
  client at spawn. `wb-guestadmin` writes the password to both.
- **One password per VM.** Every system disk is a clone of the image.
  At first contact in each fresh clone, before any other request and
  before project code, `wb-guestadmin` writes a new item, marked
  pending, then sends the old and new password to `wb-guestd` on a
  host-guest connection of its own, to a port below 1024 that serves
  only this. It never reads from that connection. `wb-guestd` passes
  both to `sysadminctl -resetPasswordFor wbadmin -newPassword - -adminUser wbadmin -adminPassword -`
  on standard input and reports the result to `wb-hostd`. Only then is
  the item marked current. A crash then never loses the working password.
  The build VM rotates the same way, from the build password to the
  image password. `wb-hostd` doesn't start a session in a VM whose
  rotation hasn't succeeded.
- **Refused outside the path.** `wb-vmd` refuses provisioning options
  for any VM that isn't a new build bundle under `<data>/images/`.
  `wb-guestadmin` refuses a rotation for a VM disk that has rotated.
  Both log the refusal with its rule.
- **Not readable in the image.** No automatic login and no
  `/etc/kcpassword`, which the sealing scan checks (S06-vm-lifecycle).
- **I142** has to show that guest root can't extract the host-held
  password or reset its way into the secure token. Until then that is
  assumed.

## Open questions

1. **Who builds and rotates.** Leaning: `wb-guestadmin`, a short-lived Go
   program that `wb-launcher` starts for one build or one rotation, with
   store access to `wbadmin/` items only and no guest parser. The
   alternative, `wb-hostd`, would give store access to the process that
   parses guest messages.
2. **The build's SSH client.** Leaning: `wb-guestadmin` pipes the
   password, to `ssh` through an askpass program that reads it from an
   inherited descriptor, and to the guest's `sudo -S` on the session's
   standard input. The alternative is a `wb-askpass` helper that reads
   the store, which adds a store reader.
3. **After break-glass.** Leaning: replace the VM's system disk with a
   fresh clone at its next stop. The data disk stays, and first contact
   rotates. Rotating in place hands the new password to a guest that may
   be rooted.
4. **Break-glass delivery, and `admin`.** Leaning: `wb vm console`
   fetches the VM's item and shows it once. Whether `wbadmin` stays in
   `admin`, enabled, and with a shell depends on what that login needs.
   The sealing scan today requires it disabled, hidden, without a shell,
   and out of `admin` (I146).
5. **Unlocking the dedicated keychain.** Leaning: with signed builds, the
   data protection keychain, with one access group per role and nothing
   to unlock. Until then, a dedicated keychain file, whose password is
   the one Wraith Box item in the login keychain.
6. **1Password.** Leaning: the desktop app integration, with a vault per
   role and no service account token to store. It needs its own issue
   and a license check of the client before it is built.
7. **Credentials in host remote URLs.** Leaning: remove user name and
   password from withheld URLs too, before their audit record
   (S08-workspace-and-git, "Remote URLs").
8. **The provisioning password in the bundle.** Check in the I139 work
   whether Virtualization writes it into the build bundle. If it does,
   the build deletes it before sealing.

## Out of scope

- Which login Wraith Box owns for a Claude subscription
  (X01-model-credential).
- Project users' accounts in the guest, which have no password the host
  keeps.
- The other `SEC*` controls.

**Status:** Draft
