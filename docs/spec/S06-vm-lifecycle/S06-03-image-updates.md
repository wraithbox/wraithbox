# S06-03 - Image Updates

**Purpose:** How a VM gets a new image's system disk while its data
disk and project users stay, and the guest admin password of each VM.

## Design

### Updates

An update replaces a VM's system disk with a fresh clone of the new
image. It replaces the VM's auxiliary storage with the new image's. So
nothing on the data disk may depend on either.

A new Claude Code release also comes as a new image, with only its
Claude Code layer rebuilt (S06-01-image-layers, "The Claude Code
layer"). A VM gets it by the same update.

Project data is on a separate data disk, and survives (S06-05-disks).
On the first boot of the new system disk, `wb-guestd`:

1. mounts the data volume (S06-05-disks);
2. recreates every project user of the VM from the host state below,
   before any session starts;
3. leaves the homes as they are (X08-data-disk).

A guest root that wrote to the data disk keeps what it wrote after the
swap, because only the system disk is replaced
(T18-data-disk-persistence).

`wb-hostd` doesn't track which boot is the first. At every connection,
after the guest has mounted the data volume, it sends every project
user it holds for that data disk with `EnsureUsers`, split into
requests that each fit `wb-guestd`'s receive limit. The guest answers
OK for a user that exists with every value of the request. A resend
doesn't change a user.

`wb-hostd` reads each reply as untrusted (SEC11-root-gains-nothing). It
drops a whole reply that names a UID it didn't send, names one twice,
has a result it doesn't know, or has a rule that isn't 1 to 64
characters of `[a-z0-9-]`, and logs `guest-users-reply-invalid`. A
user without an accepted OK is refused for that VM generation: a
session start for its project is refused with the rule, and the next
VM start asks again. A request the guest doesn't answer drops the
link, and the next connection sends it again.

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

- `wb-hostd` allocates every UID and GID from one counter in `state.db`,
  from 10000 up. The image never uses that range. The user and its
  group get the same number, and the group has the user's name.
- The counter stops at 999,999,999, below `nobody` and 2^31. Past it
  every new allocation is refused with `project-user-ids-exhausted`.
- It commits the counter and the user's row before the record goes to
  the guest. The counter doesn't go down when a project is removed.
- The user is named `wbp<uid>`, and its home is that name under the
  data volume's mount point, `/private/var/wbhomes` (S06-05-disks).
- It generates every `GeneratedUID` itself, from a cryptographic random
  source as an uppercase version 4 UUID, and never records one the
  guest reports.
- Each project has one row per data disk. A new data disk gets new
  users.
- A UID, GID or `GeneratedUID` is never reused for any project on any
  data disk of this host. So a recreated user can't own another
  project's home (SEC08-proj-isolation).

#### Creation

`wb-guestd` first checks each user's values against the allocation
above: the `wbp<uid>` name, a group of the same name, one number for
the UID and the GID in the project range, two distinct `GeneratedUID`s,
and the home path. It refuses a user that fails with
`project-user-request-invalid`, before it reads anything.

It then reads the user's and the group's records and compares them
with the request. Every check below comes before the first write,
because `dscl . -create` on an existing record changes it:

- **Same user:** both records hold every value of the request, and
  the home is valid. The user is kept, and nothing is written.
- **New user:** neither record exists. `wb-guestd` makes the group,
  then the user, then the home.
- **Partial record:** every value of the request that a record holds
  matches, and the record has no `AuthenticationAuthority`, shadow
  hash, members or nested groups. A creation that stopped halfway
  leaves such a record. Other attributes, such as the `RecordType` that
  macOS adds, aren't checked. A missing record or attribute is added,
  and so is a missing home of a user that exists. A record without its
  `GeneratedUID` isn't partial: that can't be added once the record has
  an ID.
- **Anything else** is refused with `project-user-conflict`, and the
  log names the attribute that differs. That is another value, a
  record name with an alias, an `AuthenticationAuthority`, a shadow
  hash, a member, a nested group, or an `IsHidden` under its standard
  name, since `wb-guestd` writes it as a native attribute. It is also
  another record that holds one of the user's names, IDs or
  `GeneratedUID`s, or lists the
  user as a member or the group as a nested group. A group that nests
  the project group, such as `admin`, would give the project user its
  rights. `GeneratedUID`s are compared without case.

Before it writes, it also refuses:

- a UID or GID that owns an entry at the top of the data volume other
  than the user's own home, `project-ids-own-entry`;
- a home that exists and is owned by another UID,
  `project-home-other-owner`;
- a home of the user that is a link, isn't a directory, has another
  group, a mode other than 700 or a setuid, setgid or sticky bit,
  `project-home-invalid`.

It writes `GeneratedUID` as each record's first attribute, and the
primary group and UID last. Once a record has a UID and a primary
group, macOS refuses to change its `GeneratedUID` or home path
(X08-data-disk). The password is `*`, and the user record has no
`AuthenticationAuthority` and no shadow hash, so nobody can log in as
it. Both records are written without members. `wb-guestd` never uses `sysadminctl
-addUser` or `createhomedir`, which add a password, a secure token or
group memberships.

It makes the home relative to the volume's root
without following a link, mode 700, then sets its owner and mode on
the open directory. When a step after the directory is made fails, it
removes the directory again. A guest that stops between those steps
leaves a home owned by root. That user is then refused with
`project-home-other-owner` until the directory is removed by hand.

After the writes, it reads both records back and checks the home. A
difference is refused with `project-user-directory` or
`project-home-invalid`. A volume root it can't open or read, or one
with more than 4096 entries, is `project-home-root`. It opens the root
once per request and checks with `fstatfs` that it is on the mount the
data mount check just passed (S06-05-disks, "Before every session
start"). Another mount is `data-mount-volume`. A request
still running after 50 seconds refuses its remaining users with
`project-user-deadline`. It logs each user's decision and rule (I138).

#### Attaching a home

A home on the data disk is given only to a record with every ID the
host kept for it.

When the host has no state for a home, `wb-guestd` attaches it to
nobody. The session start is then refused with a rule that names `wb
project rm`, or `wb vm reset-data`, which gives the VM a new data disk
(S05-03-management-commands, NFR06-explained-refusals).

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
