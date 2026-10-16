# C11 - Host Daemons, Audit, and Disks

**Purpose:** The test cases for the host daemons' own checks:
`wb-vmd`'s device set and slots, the image build bundle, supervision,
the accounting and event channels, the audit writer, free space, and
the data disk steps.

**Verifies:** S04-02-vm-provider ("The device set is fixed and checked"),
S04-03-self-sandboxing ("Each host daemon is self-sandboxed"),
S04-07-supervision, S04-08-records-and-bounds ("Accounting and event channels"), S06-05-disks,
S09-11-audit

## Unit tests

### wb-vmd

- **Device set** (S04-02-vm-provider, "The device set is fixed and checked", X25-vmd-sandbox). Pass the check each of these, and see
  each refused with its rule: a NAT network, a bridged network, a
  shared directory, a console or SPICE device, an audio device, a disk
  or serial port file outside the VM's bundle, and a second network
  device. The fixed set of S06-vm-lifecycle is accepted.
- **Slot.** Start `wb-vmd` with `WB_VMD_SLOT` unset, with an unknown
  value, with its bundle a symbolic link to the other slot's bundle,
  and with its bundle missing. See `vmd-slot-missing`,
  `vmd-slot-unknown`, `vmd-slot-bundle-mismatch` and
  `vmd-bundle-missing` logged before `sandbox_init`.
- **Restore image.** A `wb-vmd-install` whose restore image is a
  symbolic link, or not at the fixed path, logs
  `vmd-restore-image-mismatch` before `sandbox_init`.
- **Provisioning.** An instance in the `work` or `isolated` slot
  refuses provisioning options with `vmd-provisioning-refused`.
- **Launcher table.** A table test on the `wb-launcher` entries checks
  that each `wb-vmd` entry's `WB_VMD_SLOT` value and its bundle
  parameter belong to the same slot.

### Image build

- Leave a symbolic link, a hard-linked file, or a directory at the
  sealed disk's name in the build bundle, and see the build fail with
  `build-bundle-not-regular`, nothing cloned into `<data>/images/`,
  and nothing outside the bundle deleted.

### Supervision

Spec: S04-07-supervision.

- Close each daemon's control channel, and see `wb-netd` and
  `wb-proxyd` exit, `wb-launcher` end what it started and exit, and a
  fake VM provider stop its VMs.
- On a fake clock, kill `wb-netd` five times in 10 minutes, and see:
  - the backoff;
  - each new `wb-netd` start with the wildcard budget and the
    synthetic name mapping its predecessor left, a debit written just
    before the kill included;
  - no restart after the fifth exit;
  - a session start then refused with `daemon-given-up`.
- Run the same test with the VM's audit event budget exceeded and its
  refusal events suppressed, and see every debit, every mapping entry
  and every release counted.
- Restart `wb-proxyd`, and see every running VM get a new hand-off
  socket.
- Send a running `wb-netd` a socket message with two descriptors, and
  one with another VM's generation, and see each refused.
- The type that holds `wb-hostd`'s copy of the packet transport
  descriptor has no read or write method, and the copy is closed at VM
  stop and at a restore.

### Accounting and event channels

Spec: S04-architecture.

- Fail an event write with `ENOBUFS` or `EAGAIN`, and see new work
  refused with `event-channel-blocked`, and the record written once the
  retry succeeds.
- Send a mapping entry with an uppercase or non-LDH name, a trailing
  dot, an address outside 198.18.0.0/15, or one past 131,072 live
  entries. See it logged with `mapping-entry-invalid`, and the
  `wb-netd` that sent it ended, as for a record received with
  `MSG_TRUNC`.
- See entries kept until their release record, with `wb-hostd`'s
  receipt time.
- Block an accounting write past 100 ms, and see the lookup refused
  with `accounting-write-failed`.

### Build versions

- Have `wb-guestd` report another build version, a version string that
  isn't ASCII, one longer than 64 bytes, and one not of the fixed
  form. Connect a host peer of another build version, and one of an
  unstamped build. See each refused before any other request, with the
  version escaped and cut in the log.
- Connect a host peer from a replaced bundle, and see `wb-hostd` exit
  with `bundle-replaced`. An unreadable installed version doesn't make
  it exit.

### Audit writer

Spec: S09-11-audit.

- Exceed the per-class budget, and see no 5019, 2004, approval,
  returned-work, store-access, VM or session record dropped, and
  `wb-netd` and `wb-proxyd` refuse with `audit-budget` until the window
  turns.
- Stop a VM with an open run of suppressed refusals, and see its count
  in the stop record.

### Free space

- On a fake volume, drop the free space below the floor, and see the
  VM whose data disk grew stopped before any write fails, and the
  reserve allocated again once space returns.

### Data disk steps

Spec: S06-05-disks.

With a fake `diskutil`. In each case the fake `diskutil` is never asked
to attach a file at the final name (SEC15-no-guest-disk-mount).

- Give `wb-hostd` a file at the final name that `state.db` doesn't
  record as formatted, and see the VM start refused with the file kept.
- Leave a temporary file, and see it deleted.
- Have the fake `diskutil` report another backing image path at the
  check after the attach, and at the check before `eraseDisk`.
- Replace the temporary file with another file at the same path, or
  add a hard link to it, before the attach and before the format.
- Put a file at the final name before the rename.

In the last three cases, see `wb-diskfmt` fail with the rule each time
and the file at the final name kept.

## Fuzzing

- The accounting records (debits, mapping entries and releases) and the
  event records `wb-hostd` reads from `wb-netd` and `wb-proxyd`
  (S04-08-records-and-bounds, "Accounting and event channels").
- Every vsock RPC handler in `wb-hostd`.
- The stream framing that multiplexes guest requests on a host-opened
  connection, with the project each request is attributed to
  (S04-architecture, B29-vsock-handoff).

**Status:** Draft
