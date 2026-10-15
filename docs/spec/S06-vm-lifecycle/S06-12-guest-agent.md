# S06-12 - Guest Agent

**Purpose:** What `wb-guestd`, the service in the guest, does for the host, and
how it updates itself.

## Design

### `wb-guestd`

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
- install the VM's CA certificates: write the trust files and set the
  toolchain trust variables, and on Linux and Windows guests also add
  them to the system trust store, which a macOS guest refuses without a
  user (S09-07-guest-trust);
- act as the guest end of the git transport (S08-workspace-and-git);
- relay the flow labels of the guest Network Extension, once spike
  X14-flow-attribution allows it (S13-guest-confinement). The host uses them only to narrow rules.

### Updates of `wb-guestd`

`wb-guestd` updates itself from a read-only disk image attached by
`wb-hostd`, never from the network. The host treats every response from
`wb-guestd` as untrusted input (SEC11-root-gains-nothing).

### Guest tools disk

The bundle holds a disk image with `wb-guestd`
and the other programs Wraith Box puts in the guest, built with the
bundle and stamped with its build version (S10-tech-stack). `wb-vmd`
attaches no disk file outside the VM's bundle (S04-02-vm-provider, "The device set is fixed and checked"), so before each cold start
`wb-hostd` clones the bundle's image into the VM's bundle (an APFS
clone on macOS), replacing the copy there. So a change that `wb-vmd`,
which can write the VM's bundle, made to the copy is gone at the next
cold start. `wb-vmd` attaches it read-only. A restore keeps the copy the
state was saved with, and a saved state of another build version is
deleted (below).

### Update at start

When it starts, `wb-guestd` compares its own
build version with the one recorded on the disk. When they differ, it installs the
disk's programs and has the service manager start it again, before
it listens for the host.

### The host checks, not the guest

`wb-hostd` accepts only a
`wb-guestd` of its own build version and refuses any other before
any session request (S04-08-records-and-bounds, "Version skew"). A guest that
skips the update doesn't get a session. A saved state written under
another build version is deleted and the VM cold boots, as after a
configuration change (S06-06-devices-and-warm-start, "Warm start"), because the restored guest would
run the old `wb-guestd`.

**Status:** Draft
