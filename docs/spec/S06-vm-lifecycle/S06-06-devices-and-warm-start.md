# S06-06 - Devices, Warm Start and Time

**Purpose:** A VM's devices and resources, saving and restoring its state, and
how the guest clock follows the host.

## Design

### Devices

One virtio network device whose packet transport leads
to `wb-netd` (on macOS a file-handle attachment; S07-egress-gateway, S12-platforms);
one host-guest socket device (vsock); storage: the system disk, the
data disk, and the guest tools disk, read-only (S06-12-guest-agent, "`wb-guestd`"); entropy.
No shared directories, no host audio input, no USB or serial
passthrough in v1 (SEC02-no-host-fs-share). The spikes so far also
gave each VM a Mac graphics device with one display and a USB
keyboard and pointer (X02-warm-start, X18-vsock-handoff), and several
of the sandbox extensions in `wb-vmd`'s profile serve them
(X25-vmd-sandbox). Whether v1 keeps them is open, and
`wb-vmd`'s profile is ablated again against the set chosen here.
`wb-vmd` refuses any device not in this list
(S04-02-vm-provider, "The device set is fixed and checked").

### Resources

CPU, memory, and disk are capped per VM; defaults
4 vCPU / 8 GB, configurable (SEC13-bounded-resources).

### Warm start

`wb-hostd` can have `wb-vmd` start the work VM at
login. After a configurable idle period the VM's state is saved and
the VM stops; the next session restores from saved state (NFR01-startup, NFR03-footprint).
The idle period runs only while the VM has no session that is
starting, running, paused or ending, so a VM with one is never saved
for idleness (S06-09-session-lifecycle, "Idle suspend").
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
  tried (S06-12-guest-agent, "The host checks, not the guest"). With a match, "permission
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

### Time and sleep

The guest has no network time: NTP is UDP, and
`wb-netd` drops it (S07-egress-gateway, X03-network-path). Its clock
comes only from `wb-hostd`, which sends its own time to `wb-guestd`,
and `wb-guestd` sets the guest clock from it.
- `wb-hostd` starts every update. It sends the time after every cold
  boot, every restore from saved state and every host wake, before
  the steps that depend on the time (S09-05-ca-keys), and every 60 seconds while
  the VM runs. Without the first update, a cold-booted guest starts
  from the virtual clock, and a restored guest lags by the time it
  spent saved (X18-vsock-handoff). The periodic update corrects drift
  and a change of the host clock within a minute.
- `wb-hostd` never reads or uses a time value from the guest, and the
  guest has no message that asks for the time (S04-01-design-principles, "Everything in the guest is untrusted"). The guest can't make
  `wb-hostd` send more updates than this schedule.
- `wb-guestd` steps the clock to the host's time after a cold boot, a
  restore or a host wake, and when the guest is more than 1 second
  off. Otherwise it slews the clock, so a running build never sees
  the wall clock go backward. Drift between two updates 60 seconds
  apart stays far below 1 second. A larger offset means the host
  clock changed or an update was missed, and slewing it off at NTP's
  limit of 500 ppm would take more than half an hour.

**Status:** Draft
