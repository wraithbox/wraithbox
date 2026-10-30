# S04-04 - Per-VM Processes

**Purpose:** One `wb-vmd` for each VM, the `wb-vmd-install` entry of an image
build, and `wb-diskfmt`, which formats a VM's new data disk.

## Design

### One `wb-vmd` per VM

Each VM has its own `wb-vmd`. `wb-launcher` has two fixed entries for
them, `wb-vmd-work` and `wb-vmd-isolated`:

- Each has that VM's bundle as its profile parameter:
  `<data>/vms/macos-work.noindex` or `<data>/vms/macos-isolated.noindex`.
- Each has an instance cap of one.

While both VMs run, two `wb-vmd` instances run.

### The slot

Each instance learns its slot from the `WB_VMD_SLOT` environment
variable. The variable is part of its launcher entry's fixed
environment:

| Entry | `WB_VMD_SLOT` |
|---|---|
| `wb-vmd-work` | `work` |
| `wb-vmd-isolated` | `isolated` |
| `wb-vmd-install` ("`wb-vmd-install`", below) | `build` |
| `wb-vmd-layer` ("`wb-vmd-layer`", below) | `build` |

The slot never comes from the program path or a request. So the slot
and the bundle come from the same table entry. On hosts without
`wb-launcher`, `wb-hostd` sets `WB_VMD_SLOT` when it starts `wb-vmd`,
and the same refusals apply.

`wb-hostd` creates each bundle directory, never as a symbolic link,
before it asks `wb-launcher` to start the entry. It checks the path
with `lstat` and never follows it. A symbolic link, or anything but a
directory, refuses the start with `vm-bundle-not-directory`. It creates
a new, empty build bundle before each build.

### Slot checks

Before it confines itself, `wb-vmd` refuses to start, logs the rule
and exits:

- when the variable is missing (`vmd-slot-missing`);
- when it holds any other value (`vmd-slot-unknown`);
- when its slot's bundle path doesn't exist or isn't a directory
  (`vmd-bundle-missing`);
- when the real path of that bundle isn't that slot's bundle under the
  real path of `<data>` (`vmd-slot-bundle-mismatch`). An example is a
  bundle that is a symbolic link to another VM's bundle.

So it never confines itself to a bundle its slot doesn't own
(SEC08-proj-isolation). C11-host-daemons has a test for each rule.

Until the image build is built (V1-M7-image-build), `wb-vmd` also
refuses the `build` slot, with `vmd-slot-build-unsupported`. Only
`wb-vmd-install` and `wb-vmd-layer` serve that slot.
`wb-vmd-install` has its install rules and its restore image check
("`wb-vmd-install`", below). `wb-vmd-layer` has neither
("`wb-vmd-layer`", below).

### Reaching an instance

`wb-hostd` reaches each instance only over the channels it handed that
instance at spawn: its control channel and its hand-off socketpair
(S04-02-vm-provider, "The VM provider passes descriptors, not bytes").
The channel a message arrives on says which VM it's about. No request
names a VM, a slot, or a path.

`wb-hostd` cuts each text a `wb-vmd` returns, in a refusal or a
framework error, to a fixed bound before it logs it or passes it on.

`wb-hostd` treats the contents of every VM bundle as written by a
possibly compromised `wb-vmd`. It opens, clones and deletes files in a
bundle without following symbolic links. It checks a file's identity
before it uses one, as for the sealed disk of an image build
(S06-02-image-build, "Built by Wraith Box").

### `wb-vmd-install`

An image build installs macOS through a third fixed entry,
`wb-vmd-install`. Its compiled-in profile is the `wb-vmd` profile plus
the three rules an install needs:

- reading the restore image;
- a read extension for it;
- the installation service.

Only the image build needs those rules. The session VMs' `wb-vmd`
instances don't have them (SEC12-least-privilege).

### The restore image

The install reads the restore image only at the fixed path
`<data>/images/macos-restore.ipsw`. `wb-download` fetches it, and
`wb-hostd` checks its hash before it starts `wb-vmd-install`
(S17-03-downloads).

Before it confines itself, `wb-vmd-install` also refuses to start,
logs the rule and exits unless both hold
(`vmd-restore-image-mismatch`):

- the real path of the restore image is `images/macos-restore.ipsw`
  under the real path of `<data>`;
- `lstat` shows a regular file.

### `wb-vmd-layer`

The Claude Code layer build boots a clone of a sealed parent image
(S06-01-image-layers, "The Claude Code layer"). It installs nothing
from a restore image, so it doesn't run in `wb-vmd-install`. It runs
in a fourth fixed entry, `wb-vmd-layer`:

- Its compiled-in profile is the `wb-vmd` profile of the session VMs,
  without any of the install rules (SEC12-least-privilege).
- Its profile parameter is the build bundle,
  `<data>/images/macos-build.noindex`, and its `WB_VMD_SLOT` is
  `build`. It doesn't check for a restore image.
- Its instance cap is one. `wb-hostd` never starts it while
  `wb-vmd-install` runs, because the two share the bundle and one
  maintenance VM runs at a time (S06-07-slot-admission).
- The VM it boots has no network device. `wb-guestd` gets the binary
  over the host-guest socket.
- It refuses provisioning options, and logs `vmd-provisioning-refused`,
  as a session VM's `wb-vmd` does.

`wb-hostd` creates a new, empty build bundle before it starts the
entry, and clones the parent into it with the identity check of
S06-02-image-build, "Taking the sealed image".

### The build bundle

The install target is the fixed build bundle
`<data>/images/macos-build.noindex`. That is the entry's profile
parameter, never a path from the request. So the install can't write
into the work VM's or the isolated VM's bundle. The request doesn't
choose the mode, the image, or a path.

The same instance then runs the build VM's provisioning boot and the
rest of the build in that bundle, confined to it (S06-02-image-build, "Built by Wraith Box"). It exits when the build ends. Its instance cap
is one, because one maintenance VM runs at a time
(S06-07-slot-admission).

Only this entry accepts provisioning options. An instance whose slot
isn't `build` refuses them and logs `vmd-provisioning-refused`
(S15-04-guest-admin, "Refused outside the path").

### `wb-diskfmt`

`wb-diskfmt` formats a VM's new data disk (S06-13-data-disk-format,
X08-data-disk). `wb-launcher` has two fixed entries,
`wb-diskfmt-work` and `wb-diskfmt-isolated`. Each has that VM's bundle
as its profile parameter, so the shim learns which bundle to format
without arguments. Each has an instance cap of one, and its shim is
started as the leader of a new process group, so that its parent can kill
the shim with the `diskutil` it runs (S06-13-data-disk-format, "When
`wb-diskfmt` doesn't exit"). On hosts without
`wb-launcher`, `wb-hostd` starts the entry itself, with the entry's
fixed environment and descriptors, as it starts `wb-vmd`.

Its start interface:

- no arguments;
- `WB_DISKFMT_SLOT`, `work` or `isolated`, in the entry's fixed
  environment, as `WB_VMD_SLOT` is for `wb-vmd` ("The slot" above);
- descriptor 2, its log pipe (S04-03-self-sandboxing, "Daemon logs");
- descriptor 3, one end of a Unix datagram socketpair for the exchange
  with `wb-hostd`, `proto/wraithbox/diskfmt/v1`. Each datagram holds
  one message, and none carries a descriptor.

Before it confines itself, `wb-diskfmt` refuses to start, logs the
rule and exits:

- with an argument (`diskfmt-arguments`);
- when the variable is missing (`diskfmt-slot-missing`);
- when it holds any other value (`diskfmt-slot-unknown`);
- when its slot's bundle path doesn't exist or isn't a directory
  (`diskfmt-bundle-missing`);
- when the real path of that bundle isn't that slot's bundle under the
  real path of `<data>` (`diskfmt-slot-bundle-mismatch`).

It finds `<data>` from the home directory in the user database, never
`HOME`. C11-host-daemons has a test for each rule.

1. As `wb-vmd` does, it resolves the bundle path with `realpath` to get
   its profile parameter. It reads nothing from `wb-hostd` before it
   confines itself.
2. After that, it exchanges version records with `wb-hostd`
   (S04-08-records-and-bounds, "Between host processes"), and receives
   the disk's size in GiB. A size outside the bounds in code is refused
   with `diskfmt-size-invalid`.
3. It sends the volume UUID back on the same socketpair. It renames the
   file when `wb-hostd` asks, and then exits.

`wb-hostd` treats each answer as untrusted. It accepts a volume UUID
only in canonical form, and a name only when it is the fixed one, and
it cuts a refusal's text to a fixed bound.

#### `wb-diskfmt`'s profile

The compiled-in profile allows:

- reading and writing the data disk's temporary and final names in
  that bundle, and reading the bundle directory's metadata. The
  bundle's other files are guest-written, and the shim can't open
  them;
- its inherited descriptors;
- starting `/usr/sbin/diskutil` and no other program, with the
  system's libraries and settings read-only, and listing the root
  directory, which dyld reads to find the shared cache;
- the metadata of the `/dev/disk*` nodes, whole disks and slices, but
  not their contents;
- the services `diskutil` reaches: the disk image controller, Disk
  Arbitration, `storagekitd`, and Authorization Services
  (`com.apple.authd`), which the attach asks.

The file rules bind only the shim's own opens. The disk services run
outside the sandbox and act on the disk a request names, so a
subverted shim could erase, mount or unmount any disk the host user
may, and list the host's disks. Seatbelt can't filter what a request
to a service asks for, so the profile can't narrow that.

The `authd` rule lets a subverted shim ask for authorization rights.
A right that needs authentication shows a real administrator password
dialog, and gains nothing unless the user enters a password. A right
that doesn't is granted to an administrator's session without a
dialog, which is within the host user's own powers. `diskutil` can't
attach an image without that service (I353).

These rest on the shim's inputs: it takes no guest input, only
`wb-hostd`'s request, and it exits after one format. It denies the
network and every other file. `wb-diskfmt` exits after
one format, and is never restarted. The service names are in code.
They were measured on macOS 27 with the opt-in host test, which runs a
confined format (I228).

#### `wb-diskfmt`'s kill

`wb-hostd` keeps the wait for the shim's answer, and decides when a
shim that doesn't exit is killed (S06-13-data-disk-format, "When
`wb-diskfmt` doesn't exit"). On macOS it can't send the kill. Its
profile denies `signal` to other processes, and it isn't the parent,
so it can't tell whether the shim was reaped. So it asks `wb-launcher`
on the request channel, in one fixed-format request that names the
entry and the instance ID (S04-07-supervision, "Who starts whom").

- **Refused.** `wb-launcher` refuses a kill for an entry other than the
  `wb-diskfmt` entries, and for an instance ID it never returned. It
  signals nothing, and logs the refusal with the rule
  `launcher-kill-refused`.
- **Already exited.** For an instance it started and has reaped, it
  signals nothing. Instance IDs aren't reused (S04-07-supervision, "Who
  starts whom"), so that ID can't name a newer shim.
- **Sent.** Otherwise, while it hasn't reaped the shim, it sends
  `SIGKILL` to the shim's process group and to the shim, and logs the
  decision with the rule `launcher-kill`. The shim's ID, which is also
  the group's ID, isn't reused before that reap.
- **The answer.** Its answer is one of: sent, already exited, refused
  with its rule, or the signal failed. The exit report may come before
  the answer or after it, and `wb-hostd` matches the two by instance ID.

Where `wb-launcher` writes the lines for `launcher-kill` and
`launcher-kill-refused` is decided in M3.11. They don't go only
through `wb-hostd`, so the `wb-hostd` that asked for a kill can't hide
it.

`wb-hostd` bounds its wait for the answer and for the exit report. No
answer in time, or the end of the request channel, counts as a kill
that failed. `wb-hostd` then logs `data-disk-shim-exit` with that
outcome, as it does when its own kill fails today. Each outcome,
the launcher's rule included, is in that line
(NFR06-explained-refusals).

Without `wb-launcher`, `wb-hostd` is the parent, and sends the kill
itself under the same gate.

## Decisions

### One `wb-vmd` per VM, not one for both

- **Why:** one `wb-vmd` for both VMs would let a compromise that starts
  in the isolated VM reach the work VM's disks and saved state. It
  would get there through framework code that runs in `wb-vmd`'s
  process. That breaks the separation SEC08-proj-isolation puts the
  isolated VM there for.
- **Decided:** I64, B64-vmd-sandbox.

### A separate entry for the install

The image build's install runs as its own entry, `wb-vmd-install`,
with the install rules that the session VMs' instances don't have.

- **Decided:** I64, B64-vmd-sandbox.

**Status:** Draft
