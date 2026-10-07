# S15 - Least Privilege on the Host

**Purpose:** The security design for SEC12-least-privilege: which
rights Wraith Box takes on the host, which process holds each
credential, and the one pattern a credential follows when it has to
reach another process.

**Requirements:** SEC12-least-privilege, with SEC04-no-guest-secrets,
SEC10-audit and SEC11-root-gains-nothing where a credential crosses a
boundary.

Brief: B145-least-privilege

## For review

- **Decides:** how SEC12-least-privilege is met as a whole, and the
  predetermined pattern for sharing a credential beyond its holder.
  The main worked example is the guest admin password: the host keeps
  the password of each VM's provisioning user, which replaces I145's
  "discard it and let the secure token die".
- **You are approving:** no root, no extra host accounts, the
  entitlement table, `wb setup` as the only place for one-time
  prerequisites, one nominal holder per credential, and the six sharing
  rules below. Each known crossing is either an instance of the pattern
  or listed as open. `wb-proxyd` holds one guest admin password per
  image and one per VM, and `wb-guestd` rotates it at each VM's first
  contact.
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host) is reworded. "Held by exactly one host process" becomes
  "nominally held by one process, and shared only along a path named
  here". The rewording allows nothing by itself: each crossing needs
  a path in this spec.
  SEC11-root-gains-nothing (guest root doesn't control the host)
  is kept: the host already has guest root through `wb-guestd`, so a
  guest admin password it holds is no escalation. SEC04-no-guest-secrets
  (no secrets in the guest) is kept for every credential that opens
  anything outside one VM. Whether the guest admin password needs a
  sentence in SEC04 is a question in the brief.
- **Assumed:** that Apple's Virtualization framework doesn't write the
  provisioning password into the VM bundle (open question 6). That
  `sysadminctl` reads both the new and the old password from standard
  input when each is given as `-` (the I145 comment checked
  `-adminPassword -` only). That a disabled account can authorize its
  own password change, which rotation relies on.
- **Open decisions:** 1 to 5 under "Open questions", each with a
  recommendation. Question 6 is a check.
- **Brief:** B145-least-privilege

## Design

![The host processes and which one nominally holds each credential. wb-proxyd holds the binding secrets, the CA key handle and the guest admin passwords, all at rest in the platform secret store. A binding secret goes to the binding's upstream host per request, never to a VM that runs project code. During an image build, the build's wb-vmd gets the provisioning password over a pipe until the start call returns. The build's SSH client gets it over another pipe for its one session. At a VM's first contact, wb-proxyd writes the old and the new guest admin password to wb-guestd on a host-guest connection of its own. wb-hostd passes the pipe ends and the connection on, and reads none of them.](S15-least-privilege.svg)

### Rights

- **No root or administrator rights at run time.** The processes of
  S04-architecture's table run as the user. The packet transport and the userspace
  network stack replace the host networking features that would need
  root (S04-architecture). The image build doesn't need root either: it
  never writes to a guest disk from the host (X17-image-build,
  S06-vm-lifecycle "Images").
- **No extra host user accounts.** Separation between Wraith Box's own
  processes comes from self-sandboxing (S04-architecture, "Each host
  daemon is self-sandboxed"), not from host accounts. Separation between
  projects happens in the guest (SEC08-proj-isolation).
- **Hardening and entitlements.** Every binary in the bundle is signed
  with the hardened runtime (S10-tech-stack). The macOS entitlements:

  | Process | Entitlement | Why |
  |---|---|---|
  | `wb-vmd` | `com.apple.security.virtualization` | runs VMs |
  | `wb-proxyd` | Keychain access group, once a signing identity exists | reads its own secret store items (S09-policy-credentials-audit) |
  | every other process | none | |

  No process has `com.apple.vm.networking`, which a bridged network
  needs (X25-vmd-sandbox). A new entitlement needs a change to this
  table first. Other hosts use the equivalents in S12-platforms.
- **One-time prerequisites.** `wb setup` checks the platform
  prerequisites of S12-platforms ("Host prerequisites"), explains each
  missing one, and says who can fix it. The user makes each change,
  and `wb setup` doesn't ask for an administrator password. A prerequisite that needs
  administrator rights on every run, rather than once, is a spec change
  to this section, not a workaround (S12-platforms, Windows).

### Nominal holders

Each credential has one host process that nominally holds it. That
process reads it, uses it, and decides who else may see it, within the
rules below. At rest, a credential is in the platform secret store,
readable by its holder only (S09-policy-credentials-audit, "Storage").

| Credential | Nominal holder | At rest |
|---|---|---|
| Binding secrets: the model credential, forge tokens, OAuth refresh tokens, AWS keys | `wb-proxyd` | secret store |
| CA signing key | `wb-proxyd`, as a handle | Secure Enclave, or the secret store as fallback |
| Guest admin password: one per image build, one per image, one per VM | `wb-proxyd` | secret store, one item each |

`wb-proxyd` is the holder because S04-architecture already puts every
credential there, with the only secret store access. `wb-hostd` was
rejected, because it terminates the host-guest socket and parses guest
messages. A new process only for these passwords was rejected as more
code for no gain in separation, because a compromise of `wb-proxyd`
already reaches every binding secret, each of which opens more than a guest
admin password does.

`wb-hostd`, `wb-launcher`, `wb-netd`, `wb-git`, `wb-prover` and
`wb-guestd` don't hold a credential. `wb-hostd` may pass on a
descriptor with one in it, and never reads from it.

### The sharing pattern

A credential reaches anything other than its nominal holder only along
a path listed in "Crossings". Each path follows these rules:

1. **Named in advance.** The path names the recipient (a process, an OS
   service, an upstream host, or a guest), and the event that ends its
   use. A recipient not on the list gets nothing. A request outside the
   path's scope is refused, and the refusal is logged with its rule.
2. **Random and narrow where we create it.** A credential that Wraith
   Box generates comes from the OS's cryptographic random source, and is
   scoped as narrowly as its use allows: one per build, one per VM.
3. **Descriptors or standard input only.** It goes over a pipe or a
   socket of its own, or on a program's standard input. Never in
   arguments, the environment, a file, a log line, a gRPC message
   field, an audit record, or an error message. A process that only
   forwards the descriptor never reads it.
4. **Audited without the value.** Audit records that a path was used:
   who, when, which credential by name (SEC10-audit). They never hold
   the value or a hash of it.
5. **Dropped, or burned and replaced.** At the end event the recipient
   discards its copy, and a process that holds it only for that path
   exits. Go and Swift can't reliably wipe memory, so the bound that
   counts is the process's life. A credential that reached a place
   that may be hostile counts as burned, and is replaced before it is
   used again.
6. **Into a guest only before project code, or as a burn.** A
   credential that opens anything outside one VM never enters a guest,
   which gets a placeholder (SEC04-no-guest-secrets). A guest admin
   password enters its own guest at first contact, before any project
   code has run, or in a break-glass use, which burns it, because
   SEC11-root-gains-nothing assumes the agent may have guest root.

### Crossings

Every known case where a credential leaves its nominal holder:

| Credential | From, to | Channel | Ends | Status |
|---|---|---|---|---|
| Binding secret | secret store to `wb-proxyd` | platform API | per use | instance. The access-list fallback until a signing identity exists is X09-keychain-unsigned and I61 |
| Binding secret | `wb-proxyd` to the binding's upstream host | TLS, checked against system roots only | per request | instance (S07-egress-gateway, "Credential replacement") |
| OAuth refresh token | `wb-proxyd` to the binding's token endpoint | TLS | per exchange | instance, once a binding names its endpoint (open question 4) |
| Forge token for git | `wb-proxyd` to the forge | as a binding secret | per request | instance. The guest pushes over HTTPS through the gateway with no credential of its own (S08-workspace-and-git) |
| CA signing key | none: the hardware key can't be exported | | | instance. Secure Enclave keys for an ad-hoc signed binary are X09-keychain-unsigned |
| Binding secret, when set | the user to `wb`, then to `wb-proxyd` | open | one command | open (question 4) |
| Password in a host remote URL | the host repository to `wb` | `git remote get-url` | removed at once | open (question 5) |
| Claude subscription login | open | | | open (X01-model-credential) |
| Guest admin password | see below | | | instance, with open parts |

The CA certificate and `ghcr.io`'s fixed anonymous token are public
values, not credentials, so the pattern doesn't apply to them.

### Worked example: the guest admin password

The provisioning user (`wbprov`) is the guest's volume owner and holds
its only secure token, and macOS refuses to delete it
(X17-image-build). It stays in the image as a host-administered
account. The host keeps its password, the secure token keeps working,
and the host alone can unlock it. The host already has guest root
through `wb-guestd`, so holding the guest's admin password gives it no
more than it has. SEC11-root-gains-nothing is about guest root
reaching the host's enforcement, secrets and policy, and the host
staying the guest's administrator fits it.

`wb-proxyd` generates each password and keeps it in its own secret
store item. Its paths:

| Path | Recipient | Channel | Ends |
|---|---|---|---|
| Build: provisioning options | the build's `wb-vmd`, then Apple's Virtualization service and the build guest | pipe, inherited through `wb-launcher` | the start call returns |
| Build: install session | the build's SSH client (I139), then the guest's `sudo` | pipe, then standard input | the session ends and the client exits |
| First contact: rotation | `wb-guestd`, then `sysadminctl` | a host-guest connection of its own, then standard input | one rotation |
| Break-glass console login | the user, at the VM's console | open (question 3) | one login, and the password is burned |

- **One password per VM.** Every VM's system disk is a clone of the
  image, so at first contact in each fresh clone, before any other
  request and before any project code, `wb-guestd` rotates the
  password: it sets a new one from `wb-proxyd` and authorizes the
  change with the old one, both on standard input
  (`sysadminctl -resetPasswordFor wbprov -newPassword - -adminUser wbprov -adminPassword -`).
  The build's provisioning password is the first instance, and the
  build VM's first contact retires it in favor of the image's
  password. Each VM's first contact retires the image's password in
  favor of the VM's. A password burned in one VM then opens nothing
  elsewhere. `wb-hostd` doesn't start a session in a VM whose rotation
  hasn't succeeded, and logs why.
- **Channel.** For rotation, `wb-hostd` has `wb-vmd` open a connection
  to a port of `wb-guestd`'s below 1024 that serves only this, and
  hands the descriptor to `wb-proxyd`. `wb-proxyd` writes the two
  passwords and closes it. It doesn't read from that connection, so
  `wb-proxyd` doesn't parse anything new from the guest. `wb-guestd` reports the result to `wb-hostd`
  on its usual connection. For the build, `wb-hostd` creates the pipes
  and has `wb-launcher` hand their ends over at spawn (open question 1).
- **Refused outside its path.** `wb-vmd` refuses provisioning options
  for any VM that isn't a new build bundle under `<data>/images/`.
  `wb-proxyd` refuses a rotation write for a VM that has already rotated
  since its system disk was cloned. Both log the refusal with its rule.
- **Never readable in the image or on the disk.** No automatic login
  and no `/etc/kcpassword`, which the sealing scan checks
  (S06-vm-lifecycle, "Sealing"). The guest stores only what macOS
  keeps for any account.
- **Break-glass.** When `wb-guestd` is dead or wedged, the user can log
  in at the VM's console as `wbprov`. The password then counts as
  burned, and the VM must be rotated or rebuilt before its next session
  (open question 2).
- **Images and VMs removed.** Removing an image deletes its item,
  replacing a VM's system disk deletes that VM's item, and the next
  first contact creates a new one.
- **What I142 still has to show.** That guest root can't extract the
  host-held password from the guest, and can't reset its way into the
  secure token, for example by a password reset as root. Until I142
  answers, the host-held token is assumed safe from guest root, and
  B145-least-privilege marks it assumed.

## Open questions

1. **Build pipe.** Leaning: `wb-hostd` creates one pipe each for the
   build's `wb-vmd` and its SSH client, and `wb-launcher` hands the read
   ends over at spawn and the write ends to `wb-proxyd`. It needs the
   build `wb-vmd` to be a launcher entry of its own (X25-vmd-sandbox
   decision 2), and the SSH client to be one (I139). A gRPC field is
   rejected by rule 3. Maintainer decision.
2. **After break-glass.** Leaning: replace the VM's system disk with a
   fresh clone at its next stop, which leaves the data disk alone and
   rotates at first contact. Rotating in place hands the new password
   to a guest that SEC11-root-gains-nothing treats as rooted, which
   burns it again. Maintainer decision.
3. **Break-glass delivery, and `admin`.** Leaning: `wb vm console`
   gets the password from `wb-proxyd` on a descriptor and shows it
   once, and the user types it. The alternative is `wb-vmd` typing it
   as keyboard input. Whether `wbprov` stays in `admin`, enabled and
   with a shell depends on what that login needs, and the sealing scan
   today requires it disabled, hidden, without a shell and out of
   `admin` (I146). Maintainer decision.
4. **Setting and refreshing binding secrets.** `wb cred set` reads the
   value from a prompt or standard input (S09-policy-credentials-audit),
   and no spec says how it reaches the secret store. Leaning: `wb`
   passes it to `wb-proxyd` on a descriptor, and `wb-proxyd` writes the
   item, so only `wb-proxyd` is ever on its access list and `wb-hostd`
   never sees it. A binding should name its OAuth token endpoint, so
   that the endpoint is a named recipient. Maintainer decision.
5. **Credentials in host remote URLs.** `wb` removes the user name and
   password from each remote URL it gives the guest, and sends each
   withheld URL to the audit writer (S08-workspace-and-git, "Remote
   URLs"). Leaning: remove them from withheld URLs too, before the
   record, as rule 4 requires. Maintainer decision.
6. **The provisioning password in the VM bundle.** Whether
   Virtualization writes the password into the build bundle is
   unchecked, and rule 3 forbids it on disk. Leaning: check it in the
   I139 work, by searching the bundle for the password before the
   build's first contact. If it is there, the build deletes it before
   sealing.

## Out of scope

- Which login Wraith Box owns for a Claude subscription, and how it is
  refreshed (X01-model-credential).
- Project users' accounts in the guest, which have no password the host
  keeps (S06-vm-lifecycle).
- The other `SEC*` controls.

**Status:** Draft
