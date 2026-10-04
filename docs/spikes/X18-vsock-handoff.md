# X18 - Host-guest socket and descriptor hand-off

**Purpose:** Whether `wb-guestd` in Go can use vsock in a macOS guest, and whether `wb-vmd` can hand vsock connections and the network endpoint to other processes as descriptors, also across save and restore.

Brief: B29-vsock-handoff

## For review

- **Decides:** the answer to X18-vsock-handoff (I29), and how the
  hand-off works on macOS, which S04-architecture, S06-vm-lifecycle
  and S12-platforms now state: `wb-vmd` passes each vsock connection
  and the host end of the network socket pair with `SCM_RIGHTS` and
  closes its own copy, and every host-guest connection ends at a
  restore, so `wb-hostd` reconnects to `wb-guestd` after each one.
- **You are approving:** the answer "yes, with conditions", the
  measurements, and the spec text in this pull request. A suspended
  idle guest answers gRPC from `wb-guestd` 4.1 s (p50) after the restore
  starts, which leaves about 2.9 s of the 7 s in NFR01-startup for
  starting Claude Code.
- **Controls touched:** none weakened. The hand-off keeps guest bytes
  out of `wb-vmd`'s code path (S04-architecture, SEC12-least-privilege).
  They still pass through Apple's Virtualization service process, as
  they always did. SEC08-proj-isolation and SEC10-audit depend on
  `wb-hostd` routing only on the destination port, because the source
  port of a guest-initiated connection is the guest's choice
  (condition 1). SEC13-bounded-resources gets connection caps
  (condition 5).
- **Assumed:** that a working guest restores about as fast as this
  idle one to `wb-guestd` (the state file was 1.44 GB). The time-to-prompt
  benchmark of S11-verification-and-spikes measures a working guest.
  That a non-root guest process can open `AF_VSOCK`, and that a
  Seatbelt profile can deny it (S13-guest-confinement). Neither was
  tried.
- **Open decisions:**
  1. Guest-initiated connections: refuse them, so the host opens every
     connection (recommended, and enough for everything the spike
     needed), or bind each to a session with a one-time nonce from the
     host and close it on a mismatch. S04-architecture states both
     until this is decided.
- **Brief:** B29-vsock-handoff

## Question

From X00-index: can `wb-guestd`, written in Go, open `AF_VSOCK` sockets
in a macOS guest? Do the file descriptors of a vsock connection and of
the file-handle network attachment keep working after `wb-vmd` passes
them to another process (`SCM_RIGHTS`), and across save and restore?
The maintainer's comment on I29 adds: measure the time from
`restoreMachineStateFrom` to the Go listener answering, from a fresh
APFS clone of the state and disks.

## Answer

**Yes, with conditions.** Each part, with the evidence in "Measurements":

1. **Go opens vsock in a macOS guest: yes.** `golang.org/x/sys/unix`
   has `AF_VSOCK`, `SockaddrVM` and `IOCTL_VM_SOCKETS_GET_LOCAL_CID` for
   darwin, so `wb-guestd` doesn't need cgo or a raw-syscall shim. A Go
   LaunchDaemon in the guest listened on port 1024, accepted
   connections, dialed the host (CID 2), and served gRPC on both. The
   guest's CID was 3. The spike wraps each socket in an `os.File`,
   which the Go runtime poller handles. On the host, `socket(AF_VSOCK)`
   fails with "operation not supported by device": host processes
   reach the guest only through Virtualization.
2. **A passed vsock descriptor works: yes.** The descriptor that
   `VZVirtioSocketConnection` gives is not a vsock socket. It is a
   Unix-domain stream socket whose peer is Virtualization's service
   process (`com.apple.Virtualization.VirtualMachine`), which moves
   the bytes to the virtual device. `wb-vmd` passed it to a Go process,
   and the Go process kept working in each of these cases:
   - `wb-vmd` kept the connection object open;
   - `wb-vmd` called `close()` on it right after the pass;
   - `wb-vmd` dropped its last reference without `close()`;
   - `wb-vmd`'s main thread was blocked during the transfer;
   - the whole `wb-vmd` process was stopped with `SIGSTOP` while the Go
     process echoed 8 MiB over gRPC (43 ms) and the network answered.

   Connections the guest opens, accepted with `VZVirtioSocketListener`,
   behave the same. Half-close works in both directions: either side
   can `shutdown(SHUT_WR)`, the other reads to EOF and can still write
   (the assumption of X07-git-round-trip).
3. **A passed network descriptor works: yes.** `wb-vmd` passed the
   host end of the `SOCK_DGRAM` socket pair behind
   `VZFileHandleNetworkDeviceAttachment` and closed its own copy at
   once. The Go process exchanged frames with the guest from then on.
4. **Save and restore: yes, with conditions.**
   - The VM saves with open connections. Resumed in the same VM, every
     passed descriptor and an open gRPC stream keep working.
   - **A restore ends every connection.** When the saved VM stops,
     every passed vsock descriptor of it reads EOF. In the restored VM,
     the guest sees EOF on every connection it had at the moment it
     resumes, while its listener keeps listening. A new connection is
     accepted 5 ms after `resume`, on the first try.
   - The restored VM gets a new network socket pair in its
     configuration (as in X02-warm-start). Frames written to the old
     pair get no answer, and neither a read nor a write on it reports
     an error while the old VM object exists.
   - From a fresh APFS clone, the guest listener accepts 3.97 s (p50)
     and 4.15 s (p95) after the restore starts, and `wb-guestd`'s gRPC
     health check answers through the Go process at 4.13 s and 4.21 s.

The conditions:

1. **`wb-hostd` routes only on the destination port.** The receiving
   process sees a Unix socket, so it can't read the vsock ports from
   it, and `wb-vmd` sends them with the descriptor. The destination
   port is one `wb-hostd` chose: a guest port it asked `wb-vmd` to
   connect to, or a host listener registered at its request. The
   source port of a guest-initiated connection is chosen by whatever
   guest process dialed, so it is only logged. Any guest process that
   can open `AF_VSOCK` can dial a host listener, so guest-initiated
   connections are refused (recommended) or bound to a session by a
   one-time nonce from the host (open decision 1).
   S13-guest-confinement also denies `AF_VSOCK` to project processes,
   as a second layer.
2. **The hand-off channel carries one descriptor per message.** The
   spike framed requests as lines on a stream socket and attached the
   descriptor to the first line, which can pair a descriptor with the
   wrong request when two arrive together. The product uses a
   `SOCK_SEQPACKET` or `SOCK_DGRAM` socketpair inherited at spawn, with
   exactly one descriptor and the fields request id, VM generation,
   direction, source port and destination port in each message. The
   receiver refuses, closes and logs on a wrong descriptor count, a
   wrong socket type or a destination port it didn't ask for
   (S04-architecture, contract in I52).
3. **Every restore starts new connections and a new VM generation.**
   `wb-hostd` drops its `wb-guestd` connections when a VM is saved, and
   connects again after the restore. Before `resume`, it passes the new
   network descriptor to `wb-netd` and resets that VM's TCP flows and their `wb-proxyd` streams (not the guest's DHCP lease),
   because the old descriptor never reports an error. Hand-off messages and
   audit entries carry the VM generation, and a message from an older
   generation is refused.
4. **`wb-vmd` may close its copy of every passed descriptor at once.**
   Nothing in `wb-vmd` has to remain for a passed descriptor to
   work, except the VM itself.
5. **Connections are capped.** `wb-vmd` caps the connections it has
   accepted but not yet passed, per listener and in total, and
   `wb-hostd` caps them per VM and per project. Both log each refusal
   (SEC13-bounded-resources).
6. **The device set is fixed in `wb-vmd`'s code.** The contract never
   names a device type, so a request can't add a NAT network or a
   shared directory to a VM.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Go 1.27.1,
`golang.org/x/sys` 0.48.0, gRPC-Go 1.84.0. Guest: macOS 27.0.1, 4 vCPU,
4 GiB, the X02-warm-start configuration (`VZVirtioSocketDevice`,
`VZFileHandleNetworkDeviceAttachment` on a datagram socket pair, a
64 GiB system disk and an 8 GiB data disk, both sparse). The guest was
provisioned once (see "How the listener got into the guest") and then
sat at the login window. The clock starts before the
`VZVirtualMachine` is created, as in X02-warm-start.

| Measurement | p50 | p95 | Runs |
|---|---|---|---|
| Restore, fresh clone: `restoreMachineStateFrom` done | 3.76 s | 3.95 s | 20 |
| Restore, fresh clone: `resume` done | 3.97 s | 4.15 s | 20 |
| Restore, fresh clone: guest listener accepts | 3.97 s | 4.15 s | 20 |
| Restore, fresh clone: `wb-guestd` gRPC answers through Go | 4.13 s | 4.21 s | 20 |
| Restore, fresh clone: guest answers on the network | 3.98 s | 4.16 s | 20 |
| First gRPC health check after restore | 155 ms | 188 ms | 20 |
| Cold boot, fresh clone: guest listener accepts | 10.93 s | 13.48 s | 10 |
| `saveMachineStateTo`, 4 GiB, idle guest | 3.06 s | | 1 |

- Every restore connected on the first try, so the listener was up
  the moment the guest ran. The restore times match X02-warm-start's
  3.71 s and 3.92 s to the network within 0.3 s.
- The first gRPC call on a new connection after a restore took about
  155 ms. Later calls took about 1 ms or less. The spike didn't find out where
  the 155 ms goes.
- A cold boot reached the network at about 5 s and the listener at
  about 11 s. The guest daemon starts about 10 s into the boot, and its
  first `listen` succeeds.
- Throughput on a passed descriptor: 8 MiB echoed through gRPC in 43
  to 70 ms.
- **Restore fails while the host is locked.** In the first try of the
  restore series, all 20 restores failed with "The virtual machine
  failed to restore with error permission denied" (`VZErrorRestore`,
  code 12, the code of every restore failure), and the system log shows the Secure Enclave key refused
  (`-25308`, `errSecInteractionNotAllowed`) in Virtualization's service
  process (`results/restore-4g-failed-syslog.txt`). That is the error
  X02-warm-start saw when saving on a locked host. The second try
  waited for an unlock before each run and logged the lock state
  (`results/restore-4g.jsonl.lock`, unlocked for every run), and 20 of
  20 restores of the same state worked. X02-warm-start had assumed
  this.
- **The guest clock lags after a restore.** Right after one restore,
  the guest's wall clock was 9.5 s behind the host's, about the time
  the VM had been saved and stopped. S06-vm-lifecycle already has
  `wb-guestd` set the guest clock after a restore.

## How the listener got into the guest

A stock guest runs nothing on vsock (X02-warm-start). This spike used
`VZMacGuestProvisioningOptions`, new in macOS 27: the first boot after
installing creates a user with Remote Login on. The setup script then
connected over SSH, on a NAT network that only this setup boot had,
installed the daemon as a root LaunchDaemon with `sudo`, and shut the
guest down. It took 38 to 40 s from the first boot. No host process
mounted a guest disk. This is a shortcut for the spike, not the product
path, which X17-image-build decides. It is evidence for X17-image-build
that provisioning works on a macOS 27 host and guest. The provisioned
bundles have Remote Login on and an admin user with a password, so no
product image may descend from them.

## What was not measured

- **A working guest.** Every state came from a guest idle at the login
  window. The time-to-prompt benchmark of S11-verification-and-spikes
  restores a working guest's state.
- **`wb-vmd` exiting.** Virtualization runs the VM on behalf of the
  process that created it, so the VM is expected to stop with `wb-vmd`,
  and the passed descriptors to read EOF, which no run tried.
- **`net.FileConn` on a vsock descriptor in the guest.** The spike used
  `os.NewFile` and its own `net.Conn` wrapper. On the host, a passed
  descriptor is a Unix socket, which `net.FileConn` handles.
- **Restore after a host restart or update,** as in X02-warm-start.

## What it means for the specs

- **S04-architecture**, "The VM provider passes descriptors, not
  bytes": states what the macOS descriptor is, the hand-off channel,
  routing on the destination port, the two ways to handle
  guest-initiated connections, the connection caps, the fixed device
  set, and what a restore does (changed in this pull request).
- **S12-platforms**: the host-guest socket and packet transport rows
  for macOS name the mechanism and this spike, and agree with
  S07-egress-gateway that the network descriptor reaches `wb-netd`
  through `wb-hostd` (changed in this pull request).
- **S06-vm-lifecycle**, "Warm start": restore while locked is now
  measured, permanence of a failed restore is decided from the host identity and build recorded with the state, and `wb-hostd` reconnects
  to `wb-guestd` after each restore (changed in this pull request).
- **S13-guest-confinement**: the session profile denies `AF_VSOCK`
  (changed in this pull request).
- **NFR01-startup**: fits for an idle guest. 4.1 s to `wb-guestd`
  answering leaves about 2.9 s of the 7 s for a suspended VM. Its text
  is unchanged.
- **Cold boot:** about 11 s to `wb-guestd` (p95 13.5 s) is above the
  10 s ceiling of NFR01-startup, if a cold boot after a failed restore
  counts against it. NFR01-startup excludes only the first boot after
  a host restart. The image work of X17-image-build should look at how
  early the LaunchDaemon starts (I86).

## Spike code

Branch `spike/x18-vsock-handoff`, at
[6312bbc](https://github.com/wraithbox/wraithbox/tree/6312bbc6631528278c6ca77fca488f7e3963e1bc/spikes/x18-vsock-handoff):
the Swift `wb-vmd` stand-in, the Go guest daemon and host receiver, the
provisioning script, the series script, and the raw results in
`results/` with their p50 and p95 in `results/stats.txt`.

**Status:** Answered 2026-10-05: yes, with conditions
