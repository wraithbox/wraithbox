# S06-06 - Devices, Warm Start and Time

**Purpose:** A VM's devices and resources, saving and restoring its
state, and how the guest clock follows the host.

## Design

### Devices

A VM has these devices:

- one virtio network device, whose packet transport leads to `wb-netd`
  (S07-egress-gateway, S12-platforms). On macOS that is a file-handle
  attachment;
- one host-guest socket device (vsock);
- storage: the system disk, the data disk, and the guest tools disk,
  read-only (S06-12-guest-agent, "`wb-guestd`");
- entropy;
- a Mac graphics device with one display, and a USB keyboard and
  pointer.

V1 has no shared directories, no host audio input, and no USB or
serial passthrough (SEC02-no-host-fs-share).

`wb-vmd` refuses any device not in this list (S04-02-vm-provider, "The device set is fixed and checked").

#### No network device yet

Until V1-M3-network-floor, the set has no network device, and `wb-vmd`
refuses every one, a file-handle one included (I160). The set is fixed
per `wb-vmd` slot (S04-04-vm-processes, "The slot"). The work and
isolated VMs get the network device with V1-M3-network-floor, and the
build VM its build network with V1-M7-image-build. Each other kind of
network attachment stays refused in every slot.

#### The graphics device

The graphics device, keyboard and pointer stay in the set for now
(I160). X02-warm-start, X18-vsock-handoff and X25-vmd-sandbox measured
that configuration, and no macOS guest has booted without them. Some
of the sandbox extensions in `wb-vmd`'s profile serve these devices
(X25-vmd-sandbox), and they give the two VMs a shared graphics cache
(T21-shared-graphics-cache).

V1-M6-release-gate checks whether a guest boots and works without
these devices. If they leave the set, `wb-vmd`'s profile is ablated
again against it (S04-03-self-sandboxing).

### Resources

CPU, memory, and disk are capped per VM (SEC13-bounded-resources). The
defaults are 4 vCPU and 8 GB, and they are configurable.

### Warm start

`wb-hostd` can have `wb-vmd` start the work VM at login. After a
configurable idle period, the VM's state is saved and the VM stops. The
next session restores from saved state (NFR01-startup,
NFR03-footprint).

The idle period runs only while the VM has no session that is
starting, running, paused, or ending. So a VM with such a session is
never saved for idleness (S06-09-session-lifecycle, "Idle suspend").

Measured times (these goals aren't a release gate):

- A restore of an idle guest takes about 4 s (X02-warm-start). That is
  inside the 7 s goal of NFR01-startup for a suspended VM.
- A cold boot takes 10.9 s (p50) and 13.5 s (p95) to the guest
  daemon's vsock listener. That is over the 10 s goal
  (X18-vsock-handoff).

#### Disks and identity

- A saved state is restored only onto the disks and auxiliary storage
  it was saved with. Once the VM has run on from them, the state is
  deleted. The framework doesn't check this, and would restore old
  guest memory over newer disks.
- Restoring one state more than once needs APFS clones of the state,
  both disks and the auxiliary storage. They are taken together at the
  save, with a fresh clone for each restore.
- The restored VM has the machine identifier and MAC address it was
  saved with. No other VM with that identifier runs during the
  restore.

#### Locked host

Saving needs the host user's session unlocked. When the idle period
ends while the host is locked, the VM keeps running. It is saved after
the next unlock.

Restoring needs the session unlocked too. A restore while locked fails
with "permission denied" (X18-vsock-handoff).

#### Connections

A restore ends every host-guest connection. `wb-hostd` connects to
`wb-guestd` again after each one. `wb-guestd` answers within about
0.2 s of `resume` (X18-vsock-handoff).

#### Failed restores

A saved state works only on the host that wrote it. It may stop working
after a host update.

- A restore that fails for one of these permanent reasons deletes the
  state, and `wb-vmd` cold boots the VM.
- A restore that fails for a temporary reason keeps the state. Examples
  are another VM with the same identifier still running, or a locked
  host.

Every restore failure has the same error code, `VZErrorRestore` (12),
in Apple's `VZVirtualMachine.h`. Only the failure reason tells them
apart. So `wb-hostd` never matches on the code alone.

- "Permission denied" comes from a locked host (X18-vsock-handoff), and
  from a state written on another host.
- "Invalid argument" comes from an identifier in use (X02-warm-start),
  from a host update, and from a state saved by a `wb-vmd` whose
  sandbox profile let it read different host facts, such as the CPU
  name (X25-vmd-sandbox).

#### Deciding permanence

`wb-hostd` decides permanence from evidence it owns. With each saved
state, it records in `state.db`:

- the host's hardware identity, the `IOPlatformUUID`;
- the macOS build;
- Wraith Box's build version.

A mismatch with the running host is permanent, whatever the error. A
build version mismatch deletes the state before any restore is tried
(S06-12-guest-agent, "The host checks, not the guest").

With a match:

- "permission denied" is temporary;
- "invalid argument" is temporary while a VM with that identifier
  runs, and permanent otherwise.

After a failure, `wb-hostd` reads the lock state, and checks whether a
VM with that identifier runs. It does both after the failure, not
before the attempt.

After a temporary failure, it retries a bounded number of times within
a bounded wall time. It reports the reason when it gives up
(NFR06-explained-refusals).

`wb-vmd` passes the error code, the failure reason, any underlying
errno, and the description. `wb-hostd` logs them with its
classification.

#### Configuration changes

When `wb-hostd` changes a VM's configuration, its resources or
devices, it deletes the saved state itself and logs why. It doesn't
wait to learn of the change from "invalid argument" at the next
restore.

### Time and sleep

The guest has no network time. NTP is UDP, and `wb-netd` drops it
(S07-egress-gateway, X03-network-path). The guest's clock comes only
from `wb-hostd`. `wb-hostd` sends its own time to `wb-guestd`, and
`wb-guestd` sets the guest clock from it.

#### When the host sends the time

`wb-hostd` starts every update. It sends the time:

- after every cold boot;
- after every restore from saved state;
- after every host wake;
- every 60 seconds while the VM runs.

After a boot, restore or wake, it sends the time before the steps that
depend on it (S09-06-ca-keys). Without that first update, a
cold-booted guest starts from the virtual clock. A restored guest lags
by the time it spent saved (X18-vsock-handoff). The periodic update
corrects drift and a change of the host clock within a minute.

`wb-hostd` never reads or uses a time value from the guest. The guest
has no message that asks for the time (S04-01-design-principles, "Everything in the guest is untrusted"). So the guest can't make
`wb-hostd` send more updates than this schedule.

#### Step or slew

`wb-guestd` steps the clock to the host's time:

- after a cold boot, a restore or a host wake;
- when the guest is more than 1 second off.

Otherwise it slews the clock, so a running build never sees the wall
clock go backward.

Drift between two updates 60 seconds apart stays far below 1 second. A
larger offset means the host clock changed or an update was missed.
Slewing it off at NTP's limit of 500 ppm would take more than half an
hour.

## Decisions

### Save after the next unlock

When the idle period ends while the host is locked, the VM keeps
running, and is saved after the next unlock.

- **Decided:** I16 (B16-warm-start).

**Status:** Draft
