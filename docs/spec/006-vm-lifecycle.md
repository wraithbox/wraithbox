# 006 - VM Lifecycle, Images and the Guest Agent

**Purpose:** How guest images are built, how VMs are kept warm, how
projects and sessions map onto guests, and what `wb-guestd` does.

**Requirements:** F2, F5–F7, F11–F14, S1, S2, S8, S11, S13, N1–N5.

## Images

- **Layers.** (1) *Base*: macOS installed from an Apple restore image,
  plus `wb-guestd`, Homebrew, and Xcode command line tools; an Xcode
  variant adds full Xcode. (2) *Org layer* (optional): a shared Brewfile
  and settings. (3) *Project layer*: the project's Brewfile, reconciled
  inside the project user's environment at session start (F7).
- **Built by Wraith Box.** `wbctl image build` installs macOS into a new
  VM, then provisions it through `wb-guestd` (no SSH, no guest network
  credentials). Builds are scripted and repeatable; nobody configures an
  image by hand.
- **Sealing.** Before an image is usable it is scanned for anything that
  looks like a secret (keychain items, tokens in dotfiles, SSH keys,
  shell history). A non-empty result fails the build (S4).
- **Storage and distribution.** Images are stored locally and cloned
  copy-on-write (APFS clones) into VM bundles. Sharing images between
  machines as OCI artifacts in a registry is a later addition.
- **Updates.** A VM's system disk is replaced by a fresh clone of the new
  image; project data lives on a separate data disk and survives (below).

## VMs

- **Two slots.** The **work VM** hosts every trusted project. The
  **isolated VM** hosts untrusted repositories (`--wb-isolated`, or a
  project marked untrusted) and work that needs a GUI login session.
  macOS allows at most two running macOS guests, including any started
  by other software: `wb-hostd` performs admission control and reports a
  clear error when a slot is unavailable (N5).
- **Disks.** A system disk (clone of the image) and a data disk holding
  project users' homes and workspaces. The data disk can be rebuilt from
  host state plus git, so it may use relaxed write-through settings for
  speed (N2).
- **Devices.** One virtio network device using a file-handle attachment
  connected to `wb-netd` (spec 007); one vsock device; storage; entropy.
  No shared directories, no host audio input, no USB or serial
  passthrough in v1 (S2).
- **Resources.** CPU, memory and disk are capped per VM; defaults
  4 vCPU / 8 GB, configurable (S13).
- **Warm start.** `wb-hostd` can start the work VM at login. After a
  configurable idle period the VM's state is saved and the VM stops;
  the next session restores from saved state (N1, N3). Whether a saved
  state can be reused more than once is open (spec 011, spike S2).
- **Time and sleep.** After host sleep or VM restore, `wb-guestd`
  resynchronises the guest clock from `wb-hostd`.

## Projects and sessions inside a VM

- Each project gets a dedicated **guest** user account, created by
  `wb-guestd`; its home on the data disk holds the project clone, Claude
  Code state (F13) and tool caches. Guest users cannot read each other's
  homes (S8). No host user accounts are created (N4).
- Each session gets its own git worktree of the project clone, so
  parallel sessions in one project do not collide (F5). Per-session build
  outputs (for example Xcode DerivedData) are kept inside the worktree.
- Ephemeral sessions use a throwaway guest user removed at session end
  (F14).

## `wb-guestd`

A root LaunchDaemon in the guest, serving gRPC over vsock to `wb-hostd`
only. Responsibilities:

- create, lock and remove project users;
- run commands as a project user with a PTY or pipes, a sanitised
  environment (explicit allowlist; placeholders for credentials), and
  per-session resource limits;
- install the egress gateway's CA certificate into the guest trust
  store and toolchain-specific trust settings (spec 009);
- act as the guest end of the git transport (spec 008);
- report listening TCP ports so `wb-hostd` can forward them to host
  loopback (F11), and optionally bridge text clipboard (F12);
- report process attribution for connections (informational only).

`wb-guestd` updates itself from a read-only disk image attached by
`wb-hostd`, never from the network. The host treats every response from
`wb-guestd` as untrusted input (S11).

**Status:** Draft
