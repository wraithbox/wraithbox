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

- It sets a receive cap of 4 KiB as a call option on `Hello`, so gRPC
  refuses a larger answer before it decodes it, and it waits at most
  5 s. Later calls on the connection take the 64 KiB cap of every guest
  connection (S04-08-records-and-bounds, "Host work the guest can
  cause").
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

- It creates and locks project users (S13-01-process-hardening,
  "Locked users"). It removes a project user's home, and keeps its
  record, locked and disabled (S06-08-projects-and-sessions, "Removing a
  home").
- It kills a project user's processes, and keeps the result as a
  cleanup record (S06-10-leftover-processes). It stops every project
  user's processes when the link drops and when it starts
  (S13-01-process-hardening, "When the link drops").
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
- It sets the guest's clock from the host's time, with
  `GuestService.SetTime` (S06-06-devices-and-warm-start, "Time and
  sleep").
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
bundle as `tools.img`, replacing the copy there. On macOS that is an
APFS clone.

`wb-vmd` may have changed anything in the VM's bundle
(S04-04-vm-processes, "Reaching an instance"). So `wb-hostd`:

- clones to a temporary name in the bundle, created exclusively, and
  renames it over `tools.img`;
- follows no symbolic link at either name. A link at `tools.img` is
  replaced, and its target is left alone;
- never opens or reads the old copy (SEC15-no-guest-disk-mount);
- deletes a temporary file an earlier crash left, without following it.

A failure refuses the start with `tools-disk-clone-failed`, and
`wb-hostd` deletes the temporary file.

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
2. runs the installed `wb-guestd` in its own place.

On macOS, in this order:

1. **It waits for the disk.** Disk Arbitration mounts the disk's
   volume, `wb-guest-tools`, at `/Volumes/wb-guest-tools` during boot,
   which can be after launchd starts `wb-guestd`. `wb-guestd` looks
   again until the disk is there, for a bounded time.
2. **It checks the mount.** The volume there has to be APFS, read-only,
   and mounted by root. Disk Arbitration mounts a disk attached at boot
   as root. A disk image that a project user attaches is mounted as that
   user, so it is refused. While the tools disk holds the path, such an
   image gets another one. `wb-guestd` checks the mount of the directory
   it opened, and reads the disk only through that directory. So a
   volume mounted at the path after the check isn't what it installs.
3. **It reads `VERSION`.** It has to be a regular file that holds a
   build version in the form of S04-08-records-and-bounds, "The guest's
   version is a label", and nothing else. `wb-guestd` follows no
   symbolic link on the disk. When it equals `wb-guestd`'s own version, the update
   ends here.
4. **It checks every program before it copies any.** It reads only the
   programs that the bundle's layout table puts on the disk
   (S17-01-bundle, "The entitlement check"), and no other file. Each
   has to be a regular file under a size cap.
   Their owners and modes on the disk don't count. The host's user
   built the disk, and `wb-guestd` sets both when it installs.
5. **It installs them** into `/Library/WraithBox/libexec`, where the
   LaunchDaemon runs `wb-guestd` from. It makes the directory when it
   is missing. The directory and each parent of it have to be
   directories that root owns and that neither their group nor others
   can write. A symbolic link there is refused. Each program is copied to a new file
   created exclusively in the directory, made mode 0755 and synced, and
   renamed over the old one, so a link at either name is replaced and
   not followed. `wb-guestd` comes last. After a failure, the next start
   finds the old `wb-guestd` and installs again.
6. **It runs the installed `wb-guestd` with `execve`.** The process ID
   stays, so launchd doesn't see an exit, and its throttle doesn't hold
   the start back ("Exec after the install" below). When `execve` fails,
   `wb-guestd` exits, and launchd starts the installed one.

The new `wb-guestd` compares again. When the disk's version still isn't
its own, while the installed `wb-guestd` already holds the disk's
bytes, the disk's `VERSION` doesn't match its program. `wb-guestd`
doesn't install again then, so it can't loop.

`wb-guestd` installs every program on the disk but `wb-guestroot`.
`wb-guestroot` (S15-05-admin-path) runs the same steps at its own
start for itself only, and in step 6 runs the installed
`wb-guestroot`. So each program has one installer.

Each refusal is logged with its rule, and `wb-guestd` then serves as it
is. The values are in code: the paths and the checks of the mount and
the directory in `internal/platform/guesttools`, and the wait, the size
cap and the rest in `internal/guestupdate`.

### The host checks, not the guest

`wb-hostd` accepts only a `wb-guestd` of its own build version. It
refuses any other before any session request
(S04-08-records-and-bounds, "Version skew"). A guest that skips the
update doesn't get a session.

A saved state written under another build version is deleted, and the
VM cold boots, as after a configuration change
(S06-06-devices-and-warm-start, "Warm start"). The restored guest
would run the old `wb-guestd`.

## Decisions

### Exec after the install

After an install, `wb-guestd` replaces itself with the installed
program through `execve`, rather than having the service manager start
it again.

- **Why:** launchd starts a `KeepAlive` job at most once every 10 s.
  The update runs during boot, right after launchd started
  `wb-guestd`, so an exit would wait out most of those 10 s before the
  host gets its hello. Cold boot to `wb-guestd` is above the
  NFR01-startup ceiling already (I86). After `execve` the process keeps
  its ID, and its memory and threads are the new program's.
- **Rejected:** an exit and the `KeepAlive` restart, for the throttle.
  `launchctl kickstart -k` on its own job, which puts the job's label
  in `wb-guestd`, and kills the process that asked. A shorter
  `ThrottleInterval` in the LaunchDaemon, which also shortens the pause
  of a crash loop.
- **Decided:** I379.

### The mount says which disk it is

`wb-guestd` takes the tools disk from its mount point, and trusts it
only when root mounted it read-only.

- **Why:** a project user can't mount a volume as root, and the tools
  disk is the only read-only disk Wraith Box attaches. The update runs
  before the host link, so `wb-guestd` can't ask the host which disk it
  is.
- **Rejected:** finding the disk by its volume name alone, which any
  disk image can have. Mounting it again at a path of `wb-guestd`'s
  own, as the data volume is (X08-data-disk). The checks above already
  refuse what that would keep out, and the programs run from the
  install directory.
- **Decided:** I379.

**Status:** Draft
