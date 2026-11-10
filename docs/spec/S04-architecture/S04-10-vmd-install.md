# S04-10 - Installing macOS in `wb-vmd`

**Purpose:** The `wb-vmd-install` entry of an image build: its install
rules, the restore image it reads, the build bundle it installs into,
and its `Install` call. The other slots are in S04-04-vm-processes.

## Design

### `wb-vmd-install`

An image build installs macOS through a third fixed entry,
`wb-vmd-install`. Its compiled-in profile is the `wb-vmd` profile plus
the three rules an install needs:

- reading the restore image;
- a read extension for it;
- the installation service, which calls Apple (S17-04-restore-and-release).

Only the image build needs those rules. The session VMs' `wb-vmd`
instances don't have them (SEC12-least-privilege).

### The restore image

The install reads the restore image only at the fixed path
`<data>/images/macos-restore.ipsw`. `wb-download` fetches it, and
`wb-hostd` checks its hash before it starts `wb-vmd-install`
(S17-03-downloads).

Before it confines itself, `wb-vmd-install` also refuses to start, logs
the rule and exits unless both hold (`vmd-restore-image-mismatch`):

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
isn't `build`, `layer` included, refuses them and logs `vmd-provisioning-refused`
(S15-04-guest-admin, "Refused outside the path"). Until I629 puts the
provisioning check in the start path, the build slot refuses them too,
with the same rule. A start call with
the Xcode flag in any slot but `layer` is refused before any device is
created, and logs `vmd-xcode-disk-refused` (S06-14-xcode-image).

### The build password pipe

In the `build` slot, `wb-vmd` inherits descriptor 5, the read end of
the build password pipe (S04-03-self-sandboxing, "How `wb-vmd` is
started"). `wb-vmd` reads the password from it only for a start with
provisioning set. The password is read to end of file within a
deadline, and must be 1 to 256 bytes of printable ASCII. Anything else
refuses the start with `vmd-provisioning-password-invalid`. `wb-vmd`
reads the pipe once and closes it. The password never reaches a log,
an error, or a file (SEC04-no-guest-secrets). Until I629 puts the
provisioning check in the start path, every slot refuses a start with
provisioning set (`vmd-provisioning-refused`), and `wb-vmd` leaves
the pipe unread.

### The install call

`Install` installs macOS from the restore image into the build bundle.
Its request holds the CPU count and memory, as a start's does. It
doesn't name a path or an image. The call streams the install's
progress in whole percents, one message per step at most. The last
message says 100 once the install is done. Only
the build slot serves it. Every other slot refuses it before it touches
a file, and logs `vmd-install-refused`.

In the build slot, the call:

1. refuses a call while an install runs or a VM starts or runs
   (`vmd-install-refused`);
2. checks the CPU count and memory against the host's range
   (`vm-resources`);
3. refuses a second install in the same instance, and a bundle that
   already holds `hw.bin`, `mid.bin`, `aux.wbstore` or `sys.wbdisk`
   (`vmd-install-refused`);
4. reads the restore image at the fixed path, and refuses one this
   host can't run with `vmd-restore-image-unsupported`;
5. creates `hw.bin` with the image's hardware model, `mid.bin` with a
   new machine identifier, `aux.wbstore`, and a sparse 64 GiB
   `sys.wbdisk`, each with `O_EXCL`;
6. installs with the build slot's fixed set, which has no network
   device. `wb-hostd` puts `tools.img` and an empty `data.wbdisk` in
   the bundle first. With them, the set passes its check
   (S04-02-vm-provider, "The device set is fixed and checked").

A framework error ends the stream with a message that holds its
`VMError`, and the call is refused with `vm-install-failed`. The
instance then serves boots of the bundle with `Start`, each a new
generation, as the other slots do. A closed lifeline during an
install ends `wb-vmd` within its exit deadline, and the install with
it. The build then fails, and `wb-hostd` deletes the build bundle
(S06-02-image-build, "The build bundle").

## Decisions

### A separate entry for the install

The image build's install runs as its own entry, `wb-vmd-install`,
with the install rules that the session VMs' instances don't have.

- **Decided:** I64, B64-vmd-sandbox.

**Status:** Draft
