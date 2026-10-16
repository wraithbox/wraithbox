# S06-03 - Image Updates

**Purpose:** How a VM gets a new image's system disk while its data
disk and project users stay, and the guest admin password of each VM.

## Design

### Updates

An update replaces a VM's system disk with a fresh clone of the new
image. It replaces the VM's auxiliary storage with the new image's. So
nothing on the data disk may depend on either.

Project data is on a separate data disk, and survives (S06-05-disks).
On the first boot of the new system disk, `wb-guestd`:

1. mounts the data volume (S06-05-disks);
2. recreates every project user of the VM from the host state below,
   before any session starts;
3. leaves the homes as they are (X08-data-disk).

A guest root that wrote to the data disk keeps what it wrote after the
swap, because only the system disk is replaced
(T18-data-disk-persistence).

### Host state per project user

To recreate the users, `wb-hostd` keeps this in `state.db` for each
project user of a VM:

- the user name, UID and GID, and the name of the user's own group;
- the user's and the group's `GeneratedUID`, the UUID that ACL entries
  name. With a new one, the ACL entries in the home stop applying, and
  a deny entry then allows;
- the home path.

Project users have no password to keep.

#### Allocation

- `wb-hostd` allocates every UID and GID from a counter in `state.db`,
  from 10000 up. The image never uses that range.
- It commits the counter before the record goes to the guest.
- It generates every `GeneratedUID` itself, and never records one the
  guest reports.
- A UID, GID or `GeneratedUID` is never reused for any project on any
  data disk of this host. So a recreated user can't own another
  project's home (SEC08-proj-isolation).

#### Creation

`wb-guestd` refuses to create a record whose name, UID or GID already
exists, because `dscl . -create` on an existing record changes it
instead. It also refuses a home that exists and is owned by another
UID. It logs the refusal and the rule (I138).

It makes the record with `GeneratedUID` as its first attribute. Once a
record has a UID and a primary group, macOS refuses to change its
`GeneratedUID` or home path.

#### Attaching a home

A home on the data disk is given only to a record with every ID the
host kept for it.

When the host has no state for a home, `wb-guestd` attaches it to
nobody. The session start is then refused with a rule that names `wb
project rm`, or the command that gives the VM a new data disk (I229,
NFR06-explained-refusals).

### One guest admin password per VM

At first contact in each fresh clone of an image, before any project
code, `wbadmin`'s password is rotated. The new one is generated for
that VM and kept in the secret store. So a password burned in one VM
opens nothing in another.

Rotation comes before any other request:

- before `wb-hostd` sends the time (S06-06-devices-and-warm-start, "Time and sleep");
- before `wb-guestd` lists its sessions (S06-11-reconnect-and-recovery, "Reconnect").

For the reset, `wb-guestd`:

1. enables `wbadmin`;
2. resets the password, with the old and new password on standard
   input;
3. disables `wbadmin` again.

`wb-hostd` doesn't start a session in a VM whose rotation hasn't
succeeded. When `wb-guestd` is dead or wedged, `wb-hostd` replaces the
VM's system disk with a fresh clone. Nobody logs in as `wbadmin`.

The store items and the channel are in S15-least-privilege.

**Status:** Draft
