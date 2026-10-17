# S06-12 - Guest Agent

**Purpose:** What `wb-guestd`, the service in the guest, does for the
host, and how it updates itself.

## Design

### `wb-guestd`

`wb-guestd` is a Go service that runs with full privileges in the
guest. On macOS it is a root LaunchDaemon. S12-platforms covers other
guests. It serves gRPC over the host-guest socket, to `wb-hostd` only.

It has one code base for every guest OS, with OS-specific parts behind
interfaces, as on the host.

#### The port

On guests that use vsock (macOS and Linux), `wb-guestd` listens on a
port below 1024.

- On macOS a process needs effective user ID 0 to bind one.
- On Linux it needs `CAP_NET_BIND_SERVICE`.

So a project user can't take the port while the service manager
restarts `wb-guestd` (S04-architecture, X27-vsock-confinement).

The port is 1023, the highest one only root can bind. The Go code of
guest and host takes it from one constant, `Port` in
`internal/platform/guestsock`. How the
listener keeps serving through aborted accepts, connect floods, and
restart races is in S04-02-vm-provider, "Guest ports below 1024".

#### The hello

The first call on every connection the host opens is
`GuestService.Hello`. `wb-guestd` answers with its build version and
the `.proto` packages it serves. `wb-hostd` checks the answer before
anything else uses the connection (S04-08-records-and-bounds, "With
`wb-guestd`"):

- It sets its receive cap of 4 KiB as a client option, so gRPC refuses
  a larger answer before it decodes it, and it waits at most 5 s.
- A timeout is refused with `guest-hello-timeout`. A failed call, an
  answer over the cap, or an error status is refused with
  `guest-hello-invalid`.
- The version and the package list get the checks of
  S04-08-records-and-bounds, each with its rule.

A refusal closes the connection and is logged with its rule, and guest
text in the record is escaped and cut. `wb-guestd`'s own receive cap is
64 KiB, because the host sends nothing large.

`wb-guestd` logs one JSON object per line on standard error, as
`wb-vmd` does. On macOS its LaunchDaemon sends that to a file in the
guest.

On a Windows guest or a Windows host, the spikes of
S12-01-platform-interfaces ("Host-guest socket: keeping `wb-guestd`'s
endpoint") decide how a project user is kept from posing as
`wb-guestd`.

#### What it does

- It creates, locks, and removes project users.
- It runs commands as a project user, with:
  - a PTY or pipes;
  - a sanitized environment: an explicit allowlist, with placeholders
    for credentials;
  - per-session resource limits;
  - the session's Seatbelt profile (S13-guest-confinement).
- It installs the VM's CA certificates (S09-08-guest-trust). It writes
  the trust files and sets the toolchain trust variables. On Linux and
  Windows guests it also adds them to the system trust store. A macOS
  guest refuses that without a user.
- It acts as the guest end of the git transport
  (S08-workspace-and-git).
- It relays the flow labels of the guest Network Extension, once spike
  X14-flow-attribution allows it (S13-guest-confinement). The host uses
  them only to narrow rules.

### Updates of `wb-guestd`

`wb-guestd` updates itself from a read-only disk image that `wb-hostd`
attaches, never from the network. The host treats every response from
`wb-guestd` as untrusted input (SEC11-root-gains-nothing).

### Guest tools disk

The bundle holds a disk image with `wb-guestd` and the other programs
Wraith Box puts in the guest. It is built with the bundle, and stamped
with its build version (S10-tech-stack).

`wb-vmd` attaches no disk file outside the VM's bundle
(S04-02-vm-provider, "The device set is fixed and checked"). So before
each cold start, `wb-hostd` clones the bundle's image into the VM's
bundle, replacing the copy there. On macOS that is an APFS clone.

`wb-vmd` can write the VM's bundle. A change it made to the copy is
gone at the next cold start. `wb-vmd` attaches the copy read-only.

A restore keeps the copy the state was saved with. A saved state of
another build version is deleted ("The host checks, not the guest"
below).

### Update at start

When it starts, `wb-guestd` compares its own build version with the
one recorded on the disk. When they differ, before it listens for the
host, it:

1. installs the disk's programs;
2. has the service manager start it again.

### The host checks, not the guest

`wb-hostd` accepts only a `wb-guestd` of its own build version. It
refuses any other before any session request
(S04-08-records-and-bounds, "Version skew"). A guest that skips the
update doesn't get a session.

A saved state written under another build version is deleted, and the
VM cold boots, as after a configuration change
(S06-06-devices-and-warm-start, "Warm start"). The restored guest
would run the old `wb-guestd`.

**Status:** Draft
