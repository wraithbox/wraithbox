# S06-03 - Image Updates

**Purpose:** How a VM gets a new image's system disk while its data disk and
project users stay, and the guest admin password of each VM.

## Design

### Updates

A VM's system disk is replaced by a fresh clone of the new
image, and its auxiliary storage by the new image's, so nothing on
the data disk may depend on either. Project data is on a separate
data disk and survives (S06-05-disks). On the first boot of the new system
disk, `wb-guestd` mounts the data volume
(S06-05-disks), then recreates every project user of the VM from the host
state below, before any session starts, and leaves the homes as they
are (X08-data-disk). To recreate them, `wb-hostd` keeps per project
user of a VM, in `state.db`:
- the user name, UID and GID, and the user's own group's name;
- the user's and the group's `GeneratedUID`, the UUID that ACL
  entries name. With a new one, the ACL entries in the home stop
  applying, and a deny entry then allows;
- the home path.

Project users have no password to keep.
- *Allocation.* `wb-hostd` allocates every UID and GID from a counter
  in `state.db`, from 10000 up, a range the image never uses. It
  commits the counter before the record goes to the guest. It
  generates every `GeneratedUID` itself and never records one the
  guest reports. A UID, GID or `GeneratedUID` is never reused for any
  project on any data disk of this host, so a recreated user can't
  own another project's home (SEC08-proj-isolation).
- *Creation.* `wb-guestd` refuses to create a record whose name, UID
  or GID already exists, because `dscl . -create` on an existing
  record changes it instead, and refuses a home that exists and is
  owned by another UID. It logs the refusal and the rule (I138). It
  makes the record with `GeneratedUID` as its first attribute: once a
  record has a UID and a primary group, macOS refuses to change its
  `GeneratedUID` or home path.
- *Attaching a home.* A home on the data disk is given only to a
  record with every ID the host kept for it. When the host has no
  state for a home, `wb-guestd` attaches it to nobody, and the session
  start is refused with a rule that names `wb project rm`, or the
  command that gives the VM a new data disk (I229,
  NFR06-explained-refusals).

A guest root that wrote to the data disk keeps what it wrote after
the swap, because only the system disk is replaced.

### One guest admin password per VM

At first contact in each fresh
clone of an image, before any project code, `wbadmin`'s password is
rotated to one that is generated for that VM and kept in the secret
store, so a password burned in one VM opens nothing in another.
Rotation comes before any other request: before `wb-hostd` sends the
time (S06-06-devices-and-warm-start, "Time and sleep"), and before `wb-guestd` lists its sessions
(S06-11-reconnect-and-recovery, "Reconnect"). `wb-guestd` enables `wbadmin` for the reset, with the
old and new password on standard input, and disables it again. `wb-hostd` doesn't start a session in a VM
whose rotation hasn't succeeded. When `wb-guestd` is dead or wedged,
`wb-hostd` replaces the VM's system disk with a fresh clone, and
nobody logs in as `wbadmin`. The store items and the channel are in
S15-least-privilege.

**Status:** Draft
