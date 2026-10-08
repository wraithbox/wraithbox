# S06 - VM Lifecycle, Images and the Guest Agent

**Purpose:** How guest images are built, how VMs are kept warm, how
projects and sessions map onto guests, and what `wb-guestd` does.

**Requirements:** FR02-any-repo, FR05-parallel-sessions to FR07-toolchain-manifest, FR11-port-forward to FR14-ephemeral, SEC01-separate-kernel, SEC02-no-host-fs-share, SEC08-proj-isolation, SEC11-root-gains-nothing, SEC13-bounded-resources, NFR01-startup to NFR05-two-macos-vms.

This spec describes macOS guests on a macOS host, the v1 target. The
structure (image layers, sealing, work and isolated VMs, one guest user
per project, worktree per session, the duties of `wb-guestd`) is the
same for every guest OS. What differs per guest and per host is listed
in S12-platforms.

## Images

- **Layers.** (1) *Base*: macOS installed from an Apple restore image,
  plus `wb-guestd`, Homebrew, and Xcode command line tools; an Xcode
  variant adds full Xcode. (2) *Org layer* (optional): a shared Brewfile
  and settings. (3) *Project layer*: the formulae the project declares,
  installed by `wb-guestd` as the toolchain user into the toolchain
  prefix before the session starts (FR07-toolchain-manifest, "Toolchain
  prefix" below).
  The image doesn't contain a Wraith Box CA. `wb-guestd` installs the
  VM's CAs at runtime (S09-policy-credentials-audit, "TLS inspection certificate
  authority").
- **Toolchain prefix** (X20-shared-homebrew). A guest has one tool
  prefix, `/opt/homebrew` on macOS, owned by the toolchain user (I144).
  That user isn't an admin, has neither a password nor a shell, and
  has a home of mode 700. Only `wb-guestd` runs commands as it: through
  `sudo -u` from root (I144), or by setting the child's user and group
  IDs itself (`syscall.Credential` in Go, with the toolchain group as
  the only supplementary group), which the implementation may prefer
  because then `wb-guestd` alone sets the environment.
  Project users can read and run the prefix, and can't write to it or
  install packages. That includes the agent. They install language
  packages into their own home instead. Who may write the prefix,
  and that a manifest is names only, keep one project from changing
  the tools of another (SEC08-proj-isolation). The session `PATH` rule
  below keeps sessions reproducible, and isn't a control. Projects still
  share the prefix's contents (T15-shared-toolchain).
  - *Who may write.* Every file in the prefix belongs to the toolchain
    user and to its own group, which has no other members. Others
    have no write permission on any of them.
  - *The manifest is data.* A Brewfile is Ruby, and `brew bundle` runs
    it as the toolchain user, so a project could plant a file in the
    prefix that every other project runs from. `wb-hostd` reads the
    manifest as a list of formula names. Each line is `brew "<name>"`,
    a comment, or blank, and each name matches
    `^[a-z0-9][a-z0-9+._@-]*$`, doesn't end in `.rb`, and is at most
    64 characters long. A manifest has at most 1000 lines and 200
    names. `wb-hostd` refuses any other manifest, such as one with a
    `tap`, a `cask`, options, `system`, a name with a `/` (a tap's
    formula, so `homebrew/core` only) or a name starting with `-`,
    and logs the line and the rule. `wb-guestd` checks the names
    against the same rules before it runs anything. It never runs
    `brew bundle` and never hands Homebrew a file a project wrote.
  - *The install* (untested, I180). `wb-guestd` resolves the names'
    whole runtime closure over the formula data snapshot, as the
    toolchain user (`brew deps --formula -n --union -- <names>`),
    checks every name in it against the same rules, and runs
    `brew install --formula --force-bottle -- <closure>` by argument
    list, with no shell, in the toolchain user's home. Homebrew
    applies `--force-bottle` and `HOMEBREW_NO_INSTALL_UPGRADE` only to
    the formulae on the command line, so every keg is named there.
    Any nonzero exit of a `brew` command refuses the session, and
    nothing relies on what a failed run left in the prefix.
  - *Bottles only.* Before the install, `wb-guestd` checks the
    formula data for a bottle for the guest of every formula in the
    closure. When one has none, the session is refused with its name
    before anything is installed, and nothing is built from source.
    When the formula data snapshot is missing or empty, `wb-guestd`
    refuses the reconcile, because Homebrew would download new data
    (untested, I180). At the start deadline
    ("Session lifecycle"), `wb-guestd` kills the reconcile's process
    group.
  - *The environment* (untested, I180). `wb-guestd` builds the
    toolchain user's environment from an allowlist: `HOME` and the working directory
    in its home, umask 022, `PATH` with the prefix's `bin` and the
    system folders, `HOMEBREW_NO_AUTO_UPDATE`,
    `HOMEBREW_NO_INSTALL_UPGRADE`,
    `HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK`,
    `HOMEBREW_NO_INSTALL_CLEANUP`, `HOMEBREW_NO_ANALYTICS`,
    `HOMEBREW_NO_ENV_HINTS`, `HOMEBREW_FORBID_PACKAGES_FROM_PATHS=1`,
    and `HOMEBREW_TEMP` and `HOMEBREW_CACHE` in its home, and the trust
    variables of S09-policy-credentials-audit, "Guest trust", with the
    same constant values project users get and never appended to an
    inherited value. Never `HOMEBREW_DEVELOPER`.
  - *A reconcile only adds* (untested, I180). Homebrew upgrades an
    outdated dependency of a formula it installs, whatever
    `HOMEBREW_NO_INSTALL_UPGRADE` says, unless that dependency is named on the
    command line, which "The install" makes sure of. What keeps dependencies, such as `openssl@3`, from moving
    under a running session is the formula data: the image build
    writes a snapshot of Homebrew's formula API into the toolchain
    user's cache, and `HOMEBREW_NO_AUTO_UPDATE` keeps it. Then nothing
    installed is outdated, and a reconcile only adds kegs.
  - *The session's `PATH`* (untested, I180). Which version a name like
    `python3` runs in the prefix's shared `bin` depends on every
    project's formulae. The session's `PATH` has `opt/<formula>/bin`
    (and Python's `libexec/bin`) for each formula of the image's
    recipe (I150) and of the project's manifest, and not the shared
    `bin`. A project can add the shared `bin` back, so this is for
    reproducible sessions only. `wb-guestd` sets `NPM_CONFIG_PREFIX`
    to a folder in the home, because `npm -g` writes to the prefix by
    default.
  - *Conflicts.* When Homebrew refuses a formula because a conflicting
    one is installed (`mysql` and `mariadb`), the reconcile fails, and
    `wb` refuses the session, names both formulae, and points to
    `--isolated` (NFR06-explained-refusals).
  - The prefix is on the system disk, so a new system disk loses what
    project layers added, and the next session start installs it again.
- **Built by Wraith Box.** `wb image build` installs macOS into a new
  VM from a restore image in a local file, then provisions it in two
  stages (X17-image-build). Builds are scripted and repeatable, and
  nobody configures an image by hand.
  1. *Provisioning boot.* The first boot sets the macOS provisioning
     options, which are new in macOS 27, so `wb image build` runs on a
     macOS 27 host. The options create the user `wbadmin` and turn
     Remote Login on, and get the guest past Setup Assistant with no
     clicks. One SSH session as `wbadmin` installs `wb-guestd` as a
     root LaunchDaemon and nothing else. That session runs on the build
     VM's own file-handle network through the host's userspace stack,
     which nothing but the host and the build guest can join, so the
     channel authenticates the guest. A session that doesn't complete
     fails the build.

     *The guest admin password.* The provisioning options give
     `wbadmin` a password, and the build's one SSH session needs it. The host keeps that user's password in the configured
     secret store from then on, and S15-least-privilege declares who
     may write, fetch or receive it:
     - `wb-guestadmin` (S04-architecture) generates a password for
       each build, writes it to the store, and hands it on pipes to the
       build's `wb-vmd` until the start call returns and to
       `wb-build-ssh` for its one session;
     - provisioning options for a session VM are refused, and the
       refusal is logged with its rule;
     - the password is never in arguments, never written to disk
       outside the store, and never in a log, a gRPC message, or an
       audit record;
     - the guest gets it only on standard input.
  2. *Through `wb-guestd`.* Everything after that goes over the
     host-guest socket to `wb-guestd`. At first contact, before any
     other request, `wbadmin`'s password is rotated from the build's
     password to the image's, which `wb-guestadmin` writes to the store
     first. Both reach `wb-guestd` on a connection of their own, and
     `sysadminctl` on standard input. The secure token keeps working,
     and the host alone can unlock it. The build fails if the rotation
     fails. It then turns Remote Login and automatic login off.
     macOS refuses to delete `wbadmin`, because it holds the volume's
     only secure token. The image keeps it as a host-administered
     account, and the build takes it out of every group, disables it,
     and deletes its keychains. Then the base layer is installed
     through `wb-guestd`, and the image is sealed.

  The host never mounts a guest disk to build an image. An
  unprivileged host process can't create a root-owned file on it, so
  launchd refuses anything the host would write there, and root on the
  host is ruled out by SEC12-least-privilege (X17-image-build).
  `wb-guestd` provisions through named operations: it has no operation
  that runs an arbitrary command as root, and it takes secrets on
  standard input, never in arguments. Every clone of an image starts
  with the same per-machine keys, so sealing deletes the SSH host keys
  (`/etc/ssh/ssh_host_*_key`). The local Kerberos realm (`LKDC`) of `wbadmin` is
  also the same in every clone. An image is rebuilt from a newer
  restore image, never updated in place.
- **Guest confinement.** The base image includes the Network Extension
  of S13-guest-confinement, approved during the build, once spike X14-flow-attribution allows it.
- **Sealing.** Before an image is usable, `wb-guestd` scans it as root,
  before any project code has run. The host parses no guest
  filesystem. Any finding fails the build:
  - anything that looks like a secret (keychain items, tokens in
    dotfiles, SSH keys, shell history) and `/etc/kcpassword`
    (SEC04-no-guest-secrets);
  - an enabled account in the `admin` or `wheel` group, other than
    root and the system accounts macOS ships in it, with membership
    checked by `dsmemberutil checkmembership` (which sees both
    `GroupMembership` and `GroupMembers`), and any sudoers entry
    beyond macOS's own (X27-vsock-confinement);
  - a non-system account, except `wbadmin` if all of these
    hold (X17-image-build): it is the volume's only crypto user
    (`diskutil apfs listCryptoUsers` lists one) and its
    `AuthenticationAuthority` has `;SecureToken;`; it is disabled,
    hidden (`IsHidden`), and without a shell; it is in no groups but
    `staff`, `everyone`, `localaccounts`, `_lpoperator`,
    `com.apple.sharepoint.group.*`, and `com.apple.access_disabled`
    (the group `pwpolicy` adds when it disables an account); its
    `~/Library/LaunchAgents` is empty; and its `~/Library/Keychains`
    is empty;
  - Remote Login (`com.openssh.sshd` enabled or loaded, or a listener
    on TCP port 22), screen sharing or remote management, and
    automatic login (SEC04-no-guest-secrets, SEC05-default-deny);
  - an `~/.ssh/authorized_keys` of any user, root included, a change to
    `sshd_config` or a file in `sshd_config.d` that macOS doesn't
    ship, and SSH host keys left in `/etc/ssh`;
  - files outside `/Users` owned by a non-system user id, other than the
    temporary folders of `wbadmin`, and launchd jobs outside an
    allowlist of what macOS ships (`com.apple.*` and `amsdstat.plist`
    in `/Library/LaunchDaemons`) plus `wb-guestd`'s.

  So any admin account the build creates is disabled or removed before
  the image is sealed.
- **Storage and distribution.** Images are stored locally and cloned
  copy-on-write (APFS clones on macOS; S12-platforms) into VM bundles.
  Sharing images between machines as OCI artifacts in a registry is a
  later addition.
- **Updates.** A VM's system disk is replaced by a fresh clone of the new
  image. Project data is on a separate data disk and survives (below).
- **One guest admin password per VM.** At first contact in each fresh
  clone of an image, before any project code, `wbadmin`'s password is
  rotated to one that is generated for that VM and kept in the secret
  store, so a password burned in one VM opens nothing in another.
  Rotation comes before any other request: before `wb-hostd` sends the
  time ("Time and sleep"), and before `wb-guestd` lists its sessions
  ("Reconnect"). `wb-guestd` enables `wbadmin` for the reset, with the
  old and new password on standard input, and disables it again. `wb-hostd` doesn't start a session in a VM
  whose rotation hasn't succeeded. When `wb-guestd` is dead or wedged,
  `wb-hostd` replaces the VM's system disk with a fresh clone, and
  nobody logs in as `wbadmin`. The store items and the channel are in
  S15-least-privilege.

## VMs

- **Two slots per guest OS.** The **work VM** hosts every project whose
  `placement` is `work`. The **isolated VM** hosts projects whose
  `placement` is `isolated`, sessions started with
  `wb --isolated claude`, and work that needs a GUI login session.
  Every `xcodebuild test` of a macOS target, a SwiftPM package's
  included, and launching a macOS app need a GUI login session, which
  the work VM doesn't have. Xcode builds, `swift build` and
  `swift test`, and signing to run locally run without one under the
  Layer 2 profiles of S13-guest-confinement. X06-guest-xcode also ran
  iOS simulators and their tests for a project user without a GUI
  session, but under the Layer 2 profiles specified there they don't
  run.
  macOS allows at most two running macOS guests, including any started
  by other software: `wb-hostd` performs admission control and reports
  a clear error when a slot is unavailable (NFR05-two-macos-vms).
- **Placement and configuration trust.** `placement` (`work` or
  `isolated`) is a project setting (S09-policy-credentials-audit,
  "Settings contents"),
  and `wb project place` changes it (S05-cli). `--isolated` overrides
  it for one session. `config_trust` (`wb trust`) only decides whether
  `.wraithbox/` is read, and never moves a project between VMs.
- **New projects start in the work VM.** A newly registered project
  (FR02-any-repo) has `placement = work`, so it shares the grants of
  every work-VM project with a session (T13-new-project-grants). For a
  repository they don't trust, the user passes `--isolated` or sets
  `placement = isolated`. The maintainer decided this on I42
  (B42-trust-placement): projects in the work VM still run as separate
  guest users.
- **Changing placement.** A new `placement` applies from the next
  session start. `wb project place` is refused while the project has a
  session, and the refusal names the session. A project user's home,
  with its Claude Code state (FR13-claude-state) and commits not yet
  pushed, is on the data disk of the VM it ran in ("Disks"). Moving a
  home to the other VM's data disk needs the data-disk design of I44.
  Until I44 is decided and built, `wb project place` is refused for a
  project that has a home on any VM's data disk. The refusal names I44
  and today's way to isolate such a project: `wb project rm`, then
  `wb project place isolated` in the repository, which registers it
  again with no home, at the cost of its Claude Code state and
  approvals (NFR06-explained-refusals). So no home is left on the old VM,
  exposed to its other projects (T05-cross-proj-clones), and no
  session starts with an empty home without saying so. A new clone,
  which has no home yet, can still be placed before its first session.
- **Isolated slot to itself.** Projects in one VM share its network
  grants and credential bindings (S07-egress-gateway, "Enforced per
  VM", T11-shared-vm-grants). A project whose `placement` is
  `isolated` gets the isolated VM to itself, unless its settings mark
  it shared (S09-policy-credentials-audit, "Settings contents").
  `wb-hostd` refuses a session start that would put another project,
  or an `--isolated` session, in the isolated VM while such a project
  has a session there, and refuses that project's session start while
  another project has one there. The error names the other project
  (NFR06-explained-refusals). Sessions started with `--isolated`, and
  projects marked shared, may share the isolated VM with each other.
  They then share their grants, and after guest root their clones
  (T05-cross-proj-clones).
- **Disks.** A system disk (clone of the image) and a data disk holding
  project users' homes and workspaces, including Claude Code state
  (FR13-claude-state) and commits not yet pushed. The data disk is a
  raw sparse image file, attached with automatic host caching and full
  synchronization (on macOS, `VZDiskImageStorageDeviceAttachment` with
  `VZDiskImageCachingModeAutomatic` and
  `VZDiskImageSynchronizationModeFull`), so every write the guest
  flushes reaches permanent storage on the host. The relaxed modes are
  not used: X05-fs-benchmark found they don't speed up the
  NFR02-fs-speed workloads, and Apple's header says an image without
  synchronization "cannot safely be reused" after a host crash or
  power loss. A raw image gives the host back the space of data the
  guest deletes, and an ASIF image does not (X05-fs-benchmark).
  With these settings, `npm ci` on the data disk ran at about 71% of
  the host on a loaded host, under the 80% goal of NFR02-fs-speed. The
  goal is not a release gate.
- **Devices.** One virtio network device whose packet transport leads
  to `wb-netd` (on macOS a file-handle attachment; S07-egress-gateway, S12-platforms);
  one host-guest socket device (vsock); storage: the system disk, the
  data disk, and the guest tools disk, read-only ("`wb-guestd`"); entropy.
  No shared directories, no host audio input, no USB or serial
  passthrough in v1 (SEC02-no-host-fs-share). The spikes so far also
  gave each VM a Mac graphics device with one display and a USB
  keyboard and pointer (X02-warm-start, X18-vsock-handoff), and several
  of the sandbox extensions in `wb-vmd`'s profile serve them
  (X25-vmd-sandbox). Whether v1 keeps them is open, and
  `wb-vmd`'s profile is ablated again against the set chosen here.
  `wb-vmd` refuses any device not in this list
  (S04-architecture, "The device set is fixed and checked").
- **Resources.** CPU, memory, and disk are capped per VM; defaults
  4 vCPU / 8 GB, configurable (SEC13-bounded-resources).
- **Warm start.** `wb-hostd` can have `wb-vmd` start the work VM at
  login. After a configurable idle period the VM's state is saved and
  the VM stops; the next session restores from saved state (NFR01-startup, NFR03-footprint).
  The idle period runs only while the VM has no session that is
  starting, running, paused or ending, so a VM with one is never saved
  for idleness ("Session lifecycle", "Idle suspend").
  A restore of an idle guest takes about 4 s (X02-warm-start), inside
  the 7 s goal of NFR01-startup for a suspended VM. A cold boot takes
  10.9 s (p50) and 13.5 s (p95) to the guest daemon's vsock
  listener, over the 10 s goal (X18-vsock-handoff). These goals aren't
  a release gate. A restore runs under these rules:
  - A saved state is restored only onto the disks and auxiliary
    storage it was saved with. Once the VM has run on from them, the
    state is deleted. The framework does not check this, and would
    restore old guest memory over newer disks. Restoring one state
    more than once needs APFS clones of the state, both disks and the
    auxiliary storage, taken together at the save, and a fresh clone
    for each restore.
  - The restored VM has the machine identifier and MAC address it was
    saved with, and no other VM with that identifier runs during the
    restore.
  - Saving needs the host user's session unlocked. When the idle
    period ends while the host is locked, the VM keeps running and is
    saved after the next unlock (decided on I16, B16-warm-start).
    Restoring needs it unlocked too: a restore while locked fails with
    "permission denied" (X18-vsock-handoff).
  - A restore ends every host-guest connection. `wb-hostd` connects
    to `wb-guestd` again after each one, which answers within about
    0.2 s of `resume` (X18-vsock-handoff).
  - A saved state works only on the host that wrote it, and may stop
    working after a host update. A restore that fails for one of these
    permanent reasons deletes the state, and `wb-vmd` cold boots the
    VM. A restore that fails for a temporary reason, such as another
    VM with the same identifier still running or a locked host, keeps
    the state.
  - Every restore failure has the same error code, `VZErrorRestore`
    (12), in Apple's `VZVirtualMachine.h`. Only the failure reason
    tells them apart, so `wb-hostd` never matches on the code alone.
    "Permission denied" comes from a locked host (X18-vsock-handoff)
    and from a state written on another host. "Invalid argument" comes
    from an identifier in use (X02-warm-start), from a host update, and
    from a state saved by a `wb-vmd` whose sandbox profile let it read
    different host facts, such as the CPU name (X25-vmd-sandbox).
  - `wb-hostd` decides permanence from evidence it owns. It records the
    host's hardware identity (the `IOPlatformUUID`), the macOS build
    and Wraith Box's build version with each saved state in `state.db`.
    A mismatch with the running host is permanent, whatever the error.
    A build version mismatch deletes the state before any restore is
    tried ("`wb-guestd`", "The host checks, not the guest"). With a match, "permission
    denied" is temporary, and "invalid argument" is temporary while a
    VM with that identifier runs and permanent otherwise.
  - After a failure, `wb-hostd` reads the lock state and checks whether
    a VM with that identifier runs (both after the failure, not before
    the attempt). After a temporary failure, it retries a bounded number of
    times within a bounded wall time, and reports the reason when it
    gives up (NFR06-explained-refusals). `wb-vmd` passes the error
    code, the failure reason, any underlying errno and the
    description, and `wb-hostd` logs them with its classification.
  - When `wb-hostd` changes a VM's configuration (resources or
    devices), it deletes the saved state itself and logs why, rather
    than learning of the change from "invalid argument" at the next
    restore.
- **Time and sleep.** The guest has no network time: NTP is UDP, and
  `wb-netd` drops it (S07-egress-gateway, X03-network-path). Its clock
  comes only from `wb-hostd`, which sends its own time to `wb-guestd`,
  and `wb-guestd` sets the guest clock from it.
  - `wb-hostd` starts every update. It sends the time after every cold
    boot, every restore from saved state and every host wake, before
    the steps that depend on the time (S09-policy-credentials-audit,
    "TLS inspection certificate authority"), and every 60 seconds while
    the VM runs. Without the first update, a cold-booted guest starts
    from the virtual clock, and a restored guest lags by the time it
    spent saved (X18-vsock-handoff). The periodic update corrects drift
    and a change of the host clock within a minute.
  - `wb-hostd` never reads or uses a time value from the guest, and the
    guest has no message that asks for the time (S04-architecture,
    "Everything in the guest is untrusted"). The guest can't make
    `wb-hostd` send more updates than this schedule.
  - `wb-guestd` steps the clock to the host's time after a cold boot, a
    restore or a host wake, and when the guest is more than 1 second
    off. Otherwise it slews the clock, so a running build never sees
    the wall clock go backward. Drift between two updates 60 seconds
    apart stays far below 1 second. A larger offset means the host
    clock changed or an update was missed, and slewing it off at NTP's
    limit of 500 ppm would take more than half an hour.

## Projects and sessions inside a VM

- Each project gets a dedicated **guest** user account, created by
  `wb-guestd`; its home on the data disk holds the project clone, Claude
  Code state (FR13-claude-state), and tool caches. Guest users cannot read each other's
  homes (SEC08-proj-isolation). No host user accounts are created (SEC12-least-privilege).
- A project user is a standard account, outside the `admin` and
  `wheel` groups, with no sudoers rule. Guest root can bind
  the host-guest socket port of `wb-guestd`, so a project user must not
  reach root through `sudo` (X27-vsock-confinement). Packages come from
  the toolchain prefix, which only `wb-guestd` writes ("Images",
  "Toolchain prefix", X20-shared-homebrew).
- Each session gets its own git worktree of the project clone, at
  `<home>/sessions/<session-id>` in the project user's home, so
  parallel sessions in one project do not collide (FR05-parallel-sessions). Per-session build
  outputs (for example Xcode DerivedData) are kept inside the worktree.
- Claude Code keeps its conversations per working directory:
  `claude --continue` resumes "the most recent conversation in the
  current directory" (`claude --help`, Claude Code 2.1.289), under
  `~/.claude/projects/<directory>`, where `<directory>` is the path
  with each character other than a letter or digit replaced by `-`.
  Each session has a new worktree path, so before it starts `claude`,
  `wb-guestd`, running as the project user, makes that directory for
  the new worktree a symbolic link to one directory per project,
  `~/.claude/projects/wb-project`. `--continue` and `--resume` then
  find the conversations of the project's earlier sessions
  (FR13-claude-state). The link is made as the project user ("Session
  lifecycle", "As the project user"), so `wb-guestd` never follows a
  link the project user made with root's rights. `wb discard` removes
  the session's link with its worktree, and leaves the per-project
  directory. Parallel sessions share that directory, so `--continue` in
  one can pick the conversation another session is running, and two
  sessions can then write one conversation. Whether Claude Code allows
  that is open. The layout was read on a macOS host, not checked in a
  guest, and I135 checks it there. I135 also checks Claude Code's
  prompt to trust a folder, which a new worktree path per session may
  bring up at every session start, unlike `claude` (FR01-drop-in).
  Rejected: one worktree path for every session, which a parallel
  session, or an ended session that isn't discarded yet, still holds.
- Ephemeral sessions use a throwaway guest user removed at session end
  (FR14-ephemeral).

## Session lifecycle

A session runs from `wb claude` to its summary. `wb-hostd` keeps each
session's record and state in `state.db` (S04-architecture, "Host
state"), with the VM and the VM generation it runs in. `wb-guestd`
holds the session's processes and, in interactive use, its terminal
device (PTY). The maintainer decided on I46 (B46-session-lifecycle)
that closing the terminal ends the session. V1 has no detach and no
`wb attach`, and a session has one client, the `wb` process that
started it.

![Session states. A session starts, runs, and ends with a WIP commit and push. A start that fails or times out ends at once. A dropped host-guest link pauses a running session, which resumes or finishes when the link returns. A stopped VM or daemon, or a pause past 30 seconds, loses a session, and a lost session ends by recovery, by wb discard, or at its deadline.](S06-vm-lifecycle.svg)

| State | What holds | Link drops | VM or daemon stops | Other exits |
|---|---|---|---|---|
| starting | `wb-hostd` admits the session, starts or restores the VM, and `wb-guestd` creates the worktree and applies the carry-in (S08-workspace-and-git) | ended, as a failed start | lost | running when `claude` starts. ended, as a failed start, on a refusal, a failure or the deadline |
| running | `claude` runs, and `wb` relays its terminal or its streams (S05-cli) | paused | lost | ending when `claude` exits or the client goes |
| paused | the link is down, and the guest's project processes are stopped (S13-guest-confinement, "Layer 1") | stays paused | lost | running or ending when the link returns. lost at the pause limit |
| ending | `claude` has exited or was killed, and `wb-guestd` commits WIP and pushes (S08-workspace-and-git, "Session end") | stays ending, and finishes again when the link returns | lost | ended when done, or as a failure at the deadline |
| lost | the session's VM, its `wb-guestd`, or `wb-hostd` stopped under it | | | ended by recovery, `wb discard`, `wb project rm`, or the deadline |
| ended | the exit status and the summary are in `wb sessions`, and the returned work stays until `wb land` or `wb discard` | | | |

`wb-hostd` logs every transition to paused, lost and ended, and each
answer it gives `wb-guestd` below, with the rule that made it, in the
audit log (SEC10-audit).

- **As the project user.** Every operation under a project user's home
  (creating and removing the worktree, the carry-in, the history link
  of "Projects and sessions inside a VM", the removal of a stale
  `index.lock` that a killed WIP commit left, by that fixed file name
  and never by a pattern, the WIP commit and the push) runs in a
  child process as that user, with the session's environment
  allowlist (S09-policy-credentials-audit). `wb-guestd`, running as
  root, never opens a path under a project user's home. So a project
  user's `.git/config` or hooks never run as root.
- **Session helper.** `wb-guestd` starts each session through a small
  helper that runs as the project user. The helper starts a new POSIX
  session (`setsid`). In interactive use it makes the PTY its
  controlling terminal, and it starts `claude` in a process group of
  its own as the terminal's foreground group, as a shell does for a
  job. It waits with `WUNTRACED`, so it sees `claude` stop, and reports
  that, and `claude`'s exit status, to `wb-guestd`. It ignores the
  `SIGHUP` it gets when the PTY's other end closes, so it can still
  report `claude`'s exit after a hangup. The session's processes are
  the processes in the helper's POSIX session. `wb-guestd` records
  that POSIX session id in a root-owned directory on the data disk,
  outside every project user's home, and uses a recorded id only for
  processes whose user ID is this session's project user. A process
  that leaves it with `setsid` is out of the session (S13-guest-confinement,
  "Known gaps"). That job control works through this helper is not
  checked yet (I135).
- **Closing the terminal ends the session.** When `wb` gets `SIGHUP`,
  or its connection to `wb-hostd` closes for any reason (`wb` was
  killed or crashed), `wb-hostd` has `wb-guestd` hang up the session.
  `wb-hostd` detects this by the closed connection, never by a missed
  heartbeat, so a `wb` stopped with job control keeps its session. In
  interactive use `wb-guestd` closes its end of the session's PTY, and
  the guest kernel sends `SIGHUP` as it does for a closed terminal.
  Otherwise the helper sends `SIGHUP` to `claude`'s process group and
  closes its standard input. A stopped group also gets `SIGCONT`, so
  the hangup is delivered. When `claude` hasn't exited 10 seconds
  later, `wb-guestd` kills the session's processes, and the project
  user's other processes as S13-guest-confinement ("Layer 1") says.
  Either way the session goes on to ending, as on a normal exit.
  `wb claude --continue` then starts a new session at the host's
  `HEAD`, which resumes the conversation ("Projects and sessions
  inside a VM"). The files the old session left uncommitted are only
  in its WIP commit, on its session branch.
- **Ending.** The session ends when `claude` exits, or after a hangup.
  `wb-guestd` commits and pushes WIP (S08-workspace-and-git, "Session
  end") and reports `claude`'s exit status, an exit code or the signal
  that ended it. The report is guest input (SEC11-root-gains-nothing):
  `wb-hostd` accepts an exit code from 0 to 255 or a signal number
  from 1 to 64. Anything else is recorded as a failure with the rule,
  and `wb` exits as for a Wraith Box failure (S05-cli, "Exit status
  and signals"). When `wb` is still connected, it prints the summary
  and exits. `wb-guestd` keeps each finished session's report (its
  exit status and whether the WIP commit and push succeeded) in its
  root-owned directory until `wb-hostd` acknowledges it, and sends it
  again after a reconnect. So a report lost in a dropped link still
  ends the session normally, not as lost. `wb-guestd` deletes a
  report when it is acknowledged, and a report or a recorded POSIX
  session id when it gets "end" for that session ("Reconnect"), so
  the directory holds at most the 64 sessions of one VM.
- **Leftover processes.** The kill loop of S13-guest-confinement
  ("Layer 1") ends clean or with a count of processes left. Where
  `wb-hostd` triggers the loop (when it ends a project's last session
  in the VM, and the kill of users with no lost session at the first
  connection), `wb-guestd` writes a cleanup record to its root-owned
  directory: the project ID, which kill it was, and the result. It
  keeps the record until `wb-hostd` acknowledges it, and lists it at
  reconnect next to the session reports, inside the same 64-entry
  bound and duplicate rule ("Reconnect"). Where the loop runs because
  a session's client went and no session of that user is left, the
  result is a field of that session's report ("Ending"). So there is
  at most one result per project per kill loop.
  `wb-hostd` accepts a result of processes left only when all of these
  hold, and otherwise drops it and logs the rule:
  - the project ID passes the project ID format check of S05-cli
    ("Naming");
  - it matches a kill that `wb-hostd` itself sent for that project in
    this VM and this VM generation, or the session whose report
    carries it is that project's;
  - it arrives within the ending deadline of that kill or session
    ("Deadlines").

  For an accepted result, `wb-hostd`:
  - writes an audit event with the rule (SEC10-audit) and tells the
    user in `wb status` and at the next `wb` command. Both name the
    project from `wb-hostd`'s own table, never from the guest's
    string, and show the count capped ("more than 1000");
  - refuses every new session in that VM, of any project, and learn
    mode, until the VM is cold-booted (NFR06-explained-refusals).
    Sessions that already run continue;
  - never saves the VM: idle suspend doesn't run, and `wb vm suspend`
    is refused with the reason. Its next start is a cold boot, and a
    saved state from before the result is deleted. Only a cold boot
    clears the refusals, never a suspend and restore;
  - offers `wb vm stop` followed by `wb vm start` (S05-cli), which
    work once no session runs in the VM.

  Guest root can fake a result or hold it back. A faked one only makes
  `wb-hostd` stricter, a cost T11-shared-vm-grants records. A withheld
  one leaves the residual of S07-egress-gateway, "Leftover processes",
  which a cold boot and the review of the learn list cover.
- **Returned work comes from the host.** The summary's commit count
  and its "(+ WIP)" mark come from `landing.git`, not from the
  report. A report that claims a push that `wb-hostd`'s `receive-pack`
  didn't accept for that session's branch is recorded as a failed
  push, and `wb` exits with 255, with the rule logged. A report of
  nothing to commit is taken as reported.
- **Changes left in the guest.** When a session ends at a deadline,
  or its WIP commit or push failed, its changes stay in the guest
  worktree. The summary and `wb sessions` then give the worktree's
  path in the guest and `wb shell --project P` to reach it, and say
  that `wb discard` deletes it (NFR06-explained-refusals).
- **Deadlines.** Each state that waits on the guest has a deadline on
  the host, counted in host time awake: starting has 10 minutes from
  admission, which includes the toolchain reconcile (FR07-toolchain-manifest),
  and ending and recovery have 7 minutes each, which hold the 10
  seconds of the hangup, the WIP commit and the 5-minute push
  watchdog (S08-workspace-and-git, "Bounds"). Past its deadline,
  `wb-hostd` has `wb-guestd` end the session (below) and records it
  as ended with a Wraith Box failure, and `wb` exits with 255. The
  worktree keeps its changes until `wb discard`. So a guest that never
  reports can't hold a session, or its VM, past the deadline
  (SEC13-bounded-resources).
- **Reconnect.** When the host-guest link drops, `wb-guestd` stops the
  project processes (S13-guest-confinement, "Layer 1"), and `wb-hostd`
  connects to it again (S04-architecture). It waits 0.1 s before the
  first try, doubles the wait after each failed one up to 5 s, and
  opens at most 20 connections to one VM in a minute
  (SEC13-bounded-resources). After it connects, `wb-guestd` lists the
  sessions it holds, the reports of finished sessions and the cleanup
  records that `wb-hostd` hasn't acknowledged ("Ending", "Leftover
  processes"). The list is guest input
  (SEC11-root-gains-nothing). `wb-hostd` refuses a list of more than
  64 entries, held sessions, reports and cleanup records counted
  together, whole. It
  also refuses a list that names one id twice, as a held session and
  as a report or twice as either. It logs the refusal and closes the
  connection, which counts as a failed try. It runs at most 64 sessions in one VM. For each
  entry it checks the session ID format of S05-cli ("Naming") first,
  then looks for a session of this VM and this VM generation only, and
  answers:
  - "resume" for a running or paused session whose `wb` is still
    connected. It goes back to running.
  - "finish" for a running or paused session whose `wb` has gone, and
    for an ending session. It hangs up as above if `claude` still
    runs. Then it kills every process left in the session, a stopped
    WIP commit or push included, removes the stale `index.lock` a
    killed commit left ("As the project user"), and commits and
    pushes WIP again. It goes to ending. A lost session of this VM
    gets "finish" from recovery (below).
  - "acknowledged" for a report of a session of this VM and this VM
    generation that `wb-hostd` has as running, paused, ending or
    lost. The session ends with that report as at a normal end, under
    the checks of "Ending" and "Returned work comes from the host".
  - "acknowledged" again for a report of a session `wb-hostd` already
    has as ended, which it logs and doesn't change.
  - "end" for every other entry, a report included: a starting session (the start has
    failed), a session of another VM, a running, paused or ending
    session of another VM generation, an ended session, a malformed
    id, and an id it has no record of. `wb-guestd` kills every
    process left in that session, a stopped WIP commit or push
    included, and doesn't commit or push. It deletes the entry's
    report and recorded POSIX session id. For
    a session whose start failed, or that was discarded, it also
    removes the worktree and its history link. `wb-hostd` logs each
    such entry with the rule.

  Every entry gets one of these answers, so every report is cleared.

  `wb-guestd` resumes a session only on "resume". A session of this VM
  that `wb-hostd` has as running, paused or ending and the list
  doesn't hold, as a held session or as a report, is lost. A starting
  session isn't in that rule: a link drop already failed its start. A false list can only stop, keep or end
  sessions in that guest. `wb` doesn't print anything while a session
  is paused.
- **Pause limit.** A session that is paused for 30 seconds of host
  time awake is lost. Time the host spends asleep doesn't count. A
  restore reconnects in about 0.2 s (X18-vsock-handoff), and a
  `wb-guestd` restart loses the session anyway ("Lost"), so the limit
  only has to outlast a slow reconnect.
- **Lost.** A session is lost when `wb-vmd` reports its VM stopped,
  when its `wb-guestd` exits (the reconnected `wb-guestd` doesn't list
  it, and a new `wb-guestd` process never takes over an earlier one's
  sessions), when `wb-hostd` exits, which stops every VM (decided on
  I48), or at the pause limit. A `wb-guestd` exit closes the PTYs it
  holds, so `claude` gets a hangup from the guest kernel. `wb` prints a
  `wraith box:` line that names the session and says its WIP returns
  at the next connection to the VM, and exits with 255 (S05-cli). When
  `wb-hostd` starts, it marks every session it has as starting,
  running, paused or ending as lost. For the effective policy of
  S07-egress-gateway, and for `wb vm stop` and `wb vm suspend`, a lost
  session counts as ended: its project's grants leave the VM when no
  other session of the project is starting, running, paused or ending
  there. For the lock and the kill of a project's last session
  (S13-guest-confinement, "Layer 1"), a lost session still counts as
  a session, so the project user is locked only after its lost
  sessions have ended, by recovery, `wb discard`, `wb project rm` or
  the deadline.
- **Recovery.** A lost session's worktree stays on the data disk. At
  the next connection to a `wb-guestd` in that VM, after a restart or
  the next VM start, `wb-hostd` sends "finish" for each lost session
  of the VM that it has a record of, whatever else the list holds.
  Recovery skips a lost session whose report the list holds, which
  "acknowledged" ends instead ("Reconnect"). On "finish",
  `wb-guestd` kills what is left of the session's processes, then
  commits and pushes WIP as at a normal end. During recovery
  `wb-hostd` accepts a push for the lost session only to its own
  branch (S08-workspace-and-git, "Ref restriction"), and gives no
  network grants on account of the lost session. The session is then
  ended, with "lost" as its exit status in `wb sessions`. When this
  was the project's last session in the VM, the project user is then
  locked and its processes killed (S13-guest-confinement, "Layer 1"). A lost session stays live for
  `export.git` until it ends (S08-workspace-and-git, "Export
  repository"), so its WIP push still has its base.
- **Discarding a lost session.** `wb discard` on a lost session, and
  `wb project rm` for each lost session of the project, end it with no
  recovery push. `wb-hostd` takes the project's landing lock
  (S08-workspace-and-git), records the session as ended and discarded,
  and only then deletes its refs. A recovery push that already holds
  the lock finishes first, and its refs are deleted after it. A later
  one is refused, because the session is no longer live. At the next
  connection to the VM, `wb-guestd` gets "end" for it, and removes
  the worktree and its history link.
- **Host sleep.** The VM sleeps with the host, and on wake `wb-hostd`
  updates the guest clock ("Time and sleep"). The session runs on as
  long as `wb` still has its terminal. When the host-guest link drops
  across the sleep, the session pauses and resumes as above. `wb` over
  SSH gets `SIGHUP` when the SSH connection drops, and that ends the
  session.
- **Idle suspend.** The idle period of "Warm start" runs only while the
  VM has no session that is starting, running, paused or ending, debug
  shells included. A lost or ended session doesn't count. So a VM is
  never saved for idleness with such a session in it, and no restore
  has a session to resume. A VM with an accepted leftover result is
  never saved ("Leftover processes"). `wb vm suspend` and `wb vm stop` are
  refused while the VM has a starting, running, paused or ending
  session. The refusal names each one with its state, the process ID
  of its `wb`, and how it ends:
  close its terminal for a running or paused session, or wait for its
  deadline for a starting or ending one, and `wb sessions` lists them
  (NFR06-explained-refusals). A terminal left open on a session keeps
  its VM running (NFR03-footprint) and holds the VM's slot
  (NFR05-two-macos-vms).
  Rejected: saving a VM whose sessions have been quiet, which needs a
  restore on the next keystroke and turns every restore into a pause.
- **Parallel sessions.** Each session has its own id, worktree, client,
  exit status and summary (FR05-parallel-sessions). Ending one session
  doesn't touch the others, except that the project user's processes
  are all killed and the user is locked when its project's last
  session ends (S13-guest-confinement, "Layer 1").

## `wb-guestd`

A Go service running with full privileges in the guest (a root
LaunchDaemon on macOS; S12-platforms for other guests), serving gRPC over the
host-guest socket to `wb-hostd` only. On guests that use vsock (macOS
and Linux), it listens on a port below 1024. On macOS a process needs
effective user ID 0 to bind one, and on Linux it needs
`CAP_NET_BIND_SERVICE`, so a project user can't take the port
while the service manager restarts it (S04-architecture, X27-vsock-confinement).
How a Windows guest's vsock driver, and Hyper-V sockets on a Windows
host, keep a project user from posing as `wb-guestd` is open in
S12-platforms. One code base for every guest OS,
with OS-specific parts behind interfaces as on the host. Responsibilities:

- create, lock, and remove project users;
- run commands as a project user with a PTY or pipes, a sanitized
  environment (explicit allowlist; placeholders for credentials),
  per-session resource limits, and the session's Seatbelt profile
  (S13-guest-confinement);
- install the egress gateway's CA certificate into the guest trust
  store and toolchain-specific trust settings (S09-policy-credentials-audit);
- act as the guest end of the git transport (S08-workspace-and-git);
- relay the flow labels of the guest Network Extension, once spike
  X14-flow-attribution allows it (S13-guest-confinement). The host uses them only to narrow rules.

Port forwarding (FR11-port-forward) and the clipboard (FR12-clipboard)
are out of scope for V1 (V1-08-no-forward-clipboard, decided on I38).
`wb-guestd` does not report listening ports or bridge the clipboard,
and nothing crosses the VM boundary for either. The analysis in
B38-port-forward-clipboard is the input for their design once they are
scheduled.

`wb-guestd` updates itself from a read-only disk image attached by
`wb-hostd`, never from the network. The host treats every response from
`wb-guestd` as untrusted input (SEC11-root-gains-nothing).

- **Guest tools disk.** The bundle holds a disk image with `wb-guestd`
  and the other programs Wraith Box puts in the guest, built with the
  bundle and stamped with its build version (S10-tech-stack). `wb-vmd`
  attaches no disk file outside the VM's bundle (S04-architecture,
  "The device set is fixed and checked"), so before each cold start
  `wb-hostd` clones the bundle's image into the VM's bundle (an APFS
  clone on macOS), replacing the copy there. So a change that `wb-vmd`,
  which can write the VM's bundle, made to the copy is gone at the next
  cold start. `wb-vmd` attaches it read-only. A restore keeps the copy the
  state was saved with, and a saved state of another build version is
  deleted (below).
- **Update at start.** When it starts, `wb-guestd` compares its own
  build version with the one recorded on the disk. When they differ, it installs the
  disk's programs and has the service manager start it again, before
  it listens for the host.
- **The host checks, not the guest.** `wb-hostd` accepts only a
  `wb-guestd` of its own build version and refuses any other before
  any session request (S04-architecture, "Version skew"). A guest that
  skips the update doesn't get a session. A saved state written under
  another build version is deleted and the VM cold boots, as after a
  configuration change ("Warm start"), because the restored guest would
  run the old `wb-guestd`.

**Status:** Draft
