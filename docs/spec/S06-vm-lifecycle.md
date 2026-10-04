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
- **Disks.** A system disk (clone of the image) and a data disk holding
  project users' homes and workspaces. The data disk can be rebuilt from
  host state plus git, so it may use relaxed write-through settings for
  speed (NFR02-fs-speed).
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
  Whether a saved state can be reused more than once is open
  (X02-warm-start).
- **Time and sleep.** After host sleep or VM restore, `wb-guestd`
  resynchronizes the guest clock from `wb-hostd`.

## Projects and sessions inside a VM

- Each project gets a dedicated **guest** user account, created by
  `wb-guestd`; its home on the data disk holds the project clone, Claude
  Code state (FR13-claude-state), and tool caches. Guest users cannot read each other's
  homes (SEC08-proj-isolation). No host user accounts are created (SEC12-least-privilege).
- Each session gets its own git worktree of the project clone, so
  parallel sessions in one project do not collide (FR05-parallel-sessions). Per-session build
  outputs (for example Xcode DerivedData) are kept inside the worktree.
- Ephemeral sessions use a throwaway guest user removed at session end
  (FR14-ephemeral).

## `wb-guestd`

A Go service running with full privileges in the guest (a root
LaunchDaemon on macOS; S12-platforms for other guests), serving gRPC over the
host-guest socket to `wb-hostd` only. One code base for every guest OS,
with OS-specific parts behind interfaces as on the host. Responsibilities:

- create, lock, and remove project users;
- run commands as a project user with a PTY or pipes, a sanitized
  environment (explicit allowlist; placeholders for credentials),
  per-session resource limits, and the session's Seatbelt profile
  (S13-guest-confinement);
- install the egress gateway's CA certificate into the guest trust
  store and toolchain-specific trust settings (S09-policy-credentials-audit);
- act as the guest end of the git transport (S08-workspace-and-git);
- report listening TCP ports so `wb-hostd` can forward them to host
  loopback (FR11-port-forward), and optionally bridge text clipboard (FR12-clipboard);
- relay the flow labels of the guest Network Extension, once spike
  X14-flow-attribution allows it (S13-guest-confinement). The host uses them only to narrow rules.

`wb-guestd` updates itself from a read-only disk image attached by
`wb-hostd`, never from the network. The host treats every response from
`wb-guestd` as untrusted input (SEC11-root-gains-nothing).

**Status:** Draft
