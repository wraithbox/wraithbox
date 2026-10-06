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
  and settings. (3) *Project layer*: the project's Brewfile, reconciled
  inside the project user's environment at session start (FR07-toolchain-manifest).
- **Built by Wraith Box.** `wb image build` installs macOS into a new
  VM, then provisions it through `wb-guestd` (no SSH, no guest network
  credentials). Builds are scripted and repeatable; nobody configures an
  image by hand.
- **Guest confinement.** The base image includes the Network Extension
  of S13-guest-confinement, approved during the build, once spike X14-flow-attribution allows it.
- **Sealing.** Before an image is usable it is scanned for anything that
  looks like a secret (keychain items, tokens in dotfiles, SSH keys,
  shell history). A non-empty result fails the build (SEC04-no-guest-secrets).
  The scan also fails the build on an enabled account in the `admin`
  or `wheel` group other than root, and on a sudoers entry beyond the
  ones macOS ships, so any admin account the build creates is disabled
  or removed before the image is sealed (X27-vsock-confinement).
- **Storage and distribution.** Images are stored locally and cloned
  copy-on-write (APFS clones on macOS; S12-platforms) into VM bundles.
  Sharing images between machines as OCI artifacts in a registry is a
  later addition.
- **Updates.** A VM's system disk is replaced by a fresh clone of the new
  image. Project data is on a separate data disk and survives (below).

## VMs

- **Two slots per guest OS.** The **work VM** hosts every trusted
  project. The **isolated VM** hosts untrusted repositories
  (`wb --isolated claude`, or a project marked untrusted) and work that
  needs a GUI login session. macOS allows at most two running macOS
  guests, including any started by other software: `wb-hostd` performs
  admission control and reports a clear error when a slot is unavailable
  (NFR05-two-macos-vms).
- **Isolated slot to itself.** Projects in one VM share its network
  grants and credential bindings (S07-egress-gateway, "Enforced per
  VM", T11-shared-vm-grants). A project whose settings put it in the
  isolated slot gets the isolated VM to itself, unless its settings
  mark it shared (S09-policy-credentials-audit, "Settings contents").
  `wb-hostd` refuses a session start that would put another project or
  an untrusted repository in the isolated VM while such a project has
  a session there, and refuses that project's session start while
  another project or untrusted repository has one there. The error
  names the other project or repository (NFR06-explained-refusals).
  Untrusted repositories, and projects marked shared, may share the
  isolated VM with each other, and then share their grants.
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
  one host-guest socket device (vsock); storage; entropy.
  No shared directories, no host audio input, no USB or serial
  passthrough in v1 (SEC02-no-host-fs-share).
- **Resources.** CPU, memory, and disk are capped per VM; defaults
  4 vCPU / 8 GB, configurable (SEC13-bounded-resources).
- **Warm start.** `wb-hostd` can have `wb-vmd` start the work VM at
  login. After a configurable idle period the VM's state is saved and
  the VM stops; the next session restores from saved state (NFR01-startup, NFR03-footprint).
  A restore takes about 4 s (X02-warm-start), under these rules:
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
    from an identifier in use (X02-warm-start) and from a host update.
  - `wb-hostd` decides permanence from evidence it owns. It records the
    host's hardware identity (the `IOPlatformUUID`) and the macOS build
    with each saved state in `state.db`. A mismatch with the running
    host is permanent, whatever the error. With a match, "permission
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
- **Time and sleep.** After host sleep or VM restore, `wb-guestd`
  resynchronizes the guest clock from `wb-hostd`.

## Projects and sessions inside a VM

- Each project gets a dedicated **guest** user account, created by
  `wb-guestd`; its home on the data disk holds the project clone, Claude
  Code state (FR13-claude-state), and tool caches. Guest users cannot read each other's
  homes (SEC08-proj-isolation). No host user accounts are created (SEC12-least-privilege).
- A project user is a standard account, outside the `admin` and
  `wheel` groups, with no sudoers rule. Guest root can bind
  the host-guest socket port of `wb-guestd`, so a project user must not
  reach root through `sudo` (X27-vsock-confinement). X20-shared-homebrew
  has to work within this.
- Each session gets its own git worktree of the project clone, so
  parallel sessions in one project do not collide (FR05-parallel-sessions). Per-session build
  outputs (for example Xcode DerivedData) are kept inside the worktree.
- Ephemeral sessions use a throwaway guest user removed at session end
  (FR14-ephemeral).

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

**Status:** Draft
