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
`wb-vmd-install` serves that slot, with its install rules and its
restore image check ("`wb-vmd-install`", below).

### Reaching an instance

`wb-hostd` reaches each instance only over the channels it handed that
instance at spawn: its control channel and its hand-off socketpair
(S04-02-vm-provider, "The VM provider passes descriptors, not bytes").
The channel a message arrives on says which VM it's about. No request
names a VM, a slot, or a path.

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

`wb-diskfmt` formats a VM's new data disk (S06-05-disks,
X08-data-disk). `wb-launcher` has two fixed entries,
`wb-diskfmt-work` and `wb-diskfmt-isolated`. Each has that VM's bundle
as its profile parameter, so the shim learns which bundle to format
without arguments.

1. As `wb-vmd` does, it resolves the bundle path with `realpath` to get
   its profile parameter. It reads nothing from `wb-hostd` before it
   confines itself.
2. After that, it receives the disk's size from `wb-hostd` over its
   inherited pipe.
3. It sends the volume UUID back over the same pipe.

The compiled-in profile allows:

- reading and writing that one bundle path;
- that one inherited descriptor;
- starting `diskutil`, and nothing else;
- the DiskImages, Disk Arbitration and disk management services that
  `diskutil` uses.

It denies the network and every other file. `wb-diskfmt` exits after
one format, and is never restarted.

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
