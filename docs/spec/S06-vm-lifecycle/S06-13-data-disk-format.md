# S06-13 - Data Disk Format

**Purpose:** How the host formats a VM's new data disk, the one
exception SEC15-no-guest-disk-mount makes, and the checks before a VM
starts with its disk.

## Design

### Formatting a data disk

The host formats a new data disk before any guest has run with it
(S06-05-disks, "Disks").

- A volume the guest formats APFS is encrypted at rest. A fresh system
  disk with fresh auxiliary storage couldn't unwrap its key, so it
  doesn't mount after a swap.
- A volume the host makes on the image isn't encrypted at rest.

I228 measures three things about a host-made volume: that it stays
unencrypted, that it gives the space the guest frees back to the host,
and that a confined `diskutil` can format it.

### The shim

The formatting runs in `wb-diskfmt`, a shim. `wb-hostd` starts it
through the fixed `wb-launcher` entry of the VM's slot,
`wb-diskfmt-work` or `wb-diskfmt-isolated`. The entry's profile
parameter is that VM's bundle (S04-architecture). The shim never takes
a path from the request. It confines itself to that bundle
(S04-04-vm-processes, "`wb-diskfmt`"), and then:

1. It creates the image under a temporary name in the bundle, with
   `O_CREAT|O_EXCL` and mode 0600. It makes the image sparse at the
   data disk's size, and keeps that descriptor open.
2. It checks the file's identity. `lstat` on the temporary name and
   `fstat` on the descriptor must give the same `st_dev` and `st_ino`,
   with `st_nlink` 1 and `st_blocks` 0.
3. It attaches the file with `diskutil image attach --noMount`. It
   checks that the backing image path the attach reports for the
   device it got is the temporary name.
4. Right before it runs `diskutil eraseDisk APFS` on that device, it
   repeats the identity check and the backing image path check. The
   backing path then comes from the device's properties in the I/O
   Registry, read in the shim's own process: `diskutil` has no command
   that reports it, and the shim doesn't start another program. A name
   replaced before the attach opened it, or after, fails the identity
   check here, and the device is detached before any format.
5. The format mounts the new, empty volume on the host once. It is a
   volume the host just made, not one a guest wrote.
6. `wb-diskfmt` reports the volume's UUID, unmounts the volume, and
   detaches the image.
7. `wb-hostd` records the volume UUID and the temporary name in
   `state.db`.
8. The shim repeats the identity check on the kept descriptor,
   without the block count, and renames the file to its final name
   with `renamex_np` and `RENAME_EXCL`. That fails with the rule when
   a file has that name.
9. `wb-hostd` records the disk as formatted.

When a check fails, the shim fails with the rule. It detaches only a
device it attached.

### The temporary and final names

The final name is `data.wbdisk`, and the temporary name
`data.wbdisk.tmp`, both in the VM's bundle (S06-05-disks, "Bundle
files").

- The host never attaches a file at the final name.
- A leftover temporary file is deleted, never attached.
- A file can have its final name while `state.db` doesn't record it as
  formatted, as after a crash between the rename and the record.
  `wb-hostd` then refuses to start the VM with `data-disk-unrecorded`.
  The refusal names `wb vm reset-data`. `wb-hostd` never deletes the
  file.
- The final name has no extension that Launch Services opens as a disk
  image (S06-05-disks, "Names on the host"). So a double click doesn't
  mount it.

A data disk is unencrypted at rest, except by the host's own
FileVault. A user who mounts it by hand gives the host kernel a
guest-written filesystem.

### Before a VM starts

`wb-hostd` checks the slot's record and the final name with `lstat`,
and never opens the file:

- a disk recorded as formatted, and a file at the final name: the VM
  starts;
- a disk recorded as formatted, and no file: the start is refused with
  `data-disk-missing`, which names `wb vm reset-data`. `wb-hostd`
  doesn't make a new disk on its own (S06-05-disks, "After a crash");
- a file at the final name not recorded as formatted:
  `data-disk-unrecorded`, as above;
- no file at the final name and no formatted disk recorded: `wb-diskfmt`
  makes a new disk, deleting a leftover temporary file first.

### The one exception

Formatting a blank image the host itself created, before any guest has
run with it, is the one exception SEC15-no-guest-disk-mount makes. The
steps above keep it to that file:

- the shim attaches only the file it just created under its temporary
  name;
- a VM starts only with a disk that `state.db` records as formatted.

The exception ends when the VM first starts with the disk.

The VM's system disk, auxiliary storage and saved state are
guest-written too. The Virtualization API in `wb-vmd` is the one host
code that opens them.

## Decisions

### A host-formatted data disk on an image file

- **Rejected:** a volume the guest formats, because it doesn't mount
  after a swap.
- **Rejected:** a USB data disk. Without a grant, TCC denies every
  guest process access to it (X08-data-disk).

**Status:** Draft
