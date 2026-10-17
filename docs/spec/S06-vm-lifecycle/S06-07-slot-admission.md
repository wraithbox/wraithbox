# S06-07 - Slot Admission

**Purpose:** How `wb-hostd` admits every macOS VM it starts against the
limit of two, and lends the isolated VM's slot to a maintenance VM.

## Design

macOS runs at most two macOS guests per host (NFR05-two-macos-vms).
`wb-hostd` admits every macOS VM it starts against that limit.

Guests of other operating systems are limited only by resources
(NFR05-two-macos-vms). These rules don't apply to them.

### What takes a slot

These take a slot:

- the work VM;
- the isolated VM;
- a *maintenance VM*: one that `wb-hostd` starts for an image rather
  than for a session.

Today the only maintenance VM is the build VM of `wb image build`. It
runs in the build bundle `<data>/images/macos-build.noindex`, under
the `wb-vmd` of the `wb-launcher` entry `wb-vmd-install`
(S06-02-image-build, "Built by Wraith Box", and S04-architecture).

S06-03-image-updates has no boot that checks a new image before it
replaces a system disk. Such a boot would be a maintenance VM too.

Once its state is saved and it has stopped, a VM doesn't hold a slot.
Xcode work takes no slot of its own (S06-04-vms-and-placement, "Xcode in the session's VM").

### The work VM is never moved

`wb-hostd` never saves or stops the work VM to make room. A
maintenance VM never starts while, by `wb-hostd`'s count, it would take
the slot the work VM needs. That holds even when the work VM isn't
running.

So a maintenance VM runs only in the isolated VM's slot, and at most
one runs at a time.

### Lending the isolated slot

A maintenance VM may start in two cases:

- the isolated VM isn't running;
- the isolated VM has no session that is starting, running, paused or
  ending (S06-09-session-lifecycle, "Idle suspend").

A debug shell (`wb shell`) counts as a session here, as for idle
suspend. So one open in the isolated VM blocks a lend.

In the second case, `wb-hostd` first saves the isolated VM's state and
stops it, as at the end of its idle period
(S06-06-devices-and-warm-start, "Warm start").

A save isn't always allowed: when the host is locked, a kill is
pending, or there is a result of processes left
(S06-10-leftover-processes). `wb-hostd` then stops the isolated VM
cold instead. Those rules allow that without a session.

#### The return

When the maintenance VM stops:

- `wb-hostd` restores an isolated VM it saved, under the rules of
  S06-06-devices-and-warm-start, "Warm start".
- An isolated VM it stopped cold stays stopped, until its next session
  starts it.
- When the isolated VM wasn't running at the lend, `wb-hostd` doesn't
  start it afterward. A saved state left from an earlier idle suspend
  is kept unchanged for the next session.

`wb-hostd` logs each lend and return with the rule that allowed it.

### The lent slot is reserved

The lend starts when `wb-hostd` decides it, before it saves or stops
the isolated VM. When it ends depends on the case:

- For a VM it saved, the lend ends when the return restore succeeds.
  It also ends when `wb-hostd` gives up on the restore under
  S06-06-devices-and-warm-start, "Warm start". That is after the
  bounded retries, not at the first temporary failure.
- For a VM it stopped cold, and for an isolated VM that wasn't running
  at the lend, the lend ends when the maintenance VM stops.

While the lend lasts, `wb-hostd` refuses every other start or restore
of the isolated VM:

- for a session;
- for a debug shell;
- for the recovery of a lost session;
- for `wb vm start`.

The return restore is the one start the lend allows. So a refusal
names the build, never a VM outside Wraith Box ("Other software"
below).

### A maintenance VM gets nothing of a session VM

A maintenance VM never gets these of the work VM or the isolated VM:

- the system disk, data disk, saved state or auxiliary storage;
- the machine identifier or MAC address.

It doesn't get a network grant or credential binding of a project
(SEC08-proj-isolation). The build makes a new VM with its own network
(S06-02-image-build, "Built by Wraith Box").

Its `wb-vmd` is confined to the build bundle. So it can't open the
work VM's or the isolated VM's bundle either (S04-architecture).

Lending moves a slot, never a VM's disks or identity. The isolated
VM's `wb-vmd` (`wb-vmd-isolated`) never runs the build VM.

### Refusals

Each refusal names the rule, what holds the slots, and how to free one
(NFR06-explained-refusals). `wb-hostd` refuses:

- a maintenance VM, while the isolated VM has a session that is
  starting, running, paused or ending, a debug shell included. The
  refusal names each session with its state, the process ID of its
  `wb`, and how it ends. The refusal of `wb vm stop` does the same
  (S06-09-session-lifecycle, "Idle suspend");
- a second maintenance VM while one runs. The refusal names the first,
  and how long a build takes, about 7 minutes (X17-image-build);
- any start or restore of the isolated VM while its slot is lent ("The
  lent slot is reserved"). The refusal names the maintenance VM, and
  says that the start works once the lend ends. A session isn't
  queued.

### Other software

`wb-hostd` can't count macOS VMs that other programs run, such as UTM,
Tart, or spike code on a development Mac (I50). It learns of them only
when starting or restoring one of its own VMs fails on the
Virtualization framework's limit of running VMs
(`VZErrorVirtualMachineLimitExceeded` in `VZError.h`).

`wb-hostd` then refuses the start with `vm-limit-other-software`. It
matches the limit on the framework error's code, which `wb-vmd` passes
on. `wb` then:

- says that a macOS VM outside Wraith Box holds a slot;
- lists the VMs Wraith Box runs;
- says to quit the other one.

`wb-hostd` doesn't retry, and doesn't stop one of its own VMs to make
room.

## Decisions

### The work VM keeps its slot, a maintenance VM borrows the isolated one

- **Decided:** I43 (B43-vm-slot-admission).

**Status:** Draft
