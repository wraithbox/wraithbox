# S15-04 - The Guest Admin User

**Purpose:** How the host keeps the password of the guest admin user
`wbadmin`, from the image build to each VM's first contact.

## Design

### The guest admin user

The provisioning options create the user `wbadmin`. It is the volume's
only secure-token holder, which macOS refuses to delete
(X17-image-build).

`wbadmin` stays in the image as a host-administered account
(S06-02-image-build, "Sealing"):

- disabled;
- hidden;
- without a shell;
- out of `admin`.

The host keeps its password in the store, and the secure token keeps
working.

That keeps SEC11-root-gains-nothing: guest root gains no control over
the host, because the asset never leaves its VM.

- The password and the token unlock only that VM's disk.
- Nothing they unlock crosses the Virtualization boundary.
- No VM can reach another VM's `wbadmin`.
- Guest root can't read the password back. The guest keeps only what
  macOS keeps for any account, and the build deletes `wbadmin`'s
  keychains before sealing.

`wb-guestadmin` (S04-architecture) does each step below, and only it
reads or writes `wbadmin/*`.

The admin path for the person at the host's terminal doesn't use
`wbadmin` or its password (S15-05-admin-path).

### Build

`wb-guestadmin-build` generates the build password and writes
`wbadmin/build/<build>`. Then:

1. `wb-hostd` creates two pipes.
2. `wb-launcher` hands one read end to the build's `wb-vmd`, and the
   other to `wb-build-ssh` as its standard input.
3. `wb-guestadmin-build` writes the password to the first, and one
   newline-terminated copy to the second.

### The build's SSH session

`wb-build-ssh` (S04-05-helper-programs) speaks SSH in its own process,
with Go's SSH library. It doesn't run an `ssh` program or an
askpass helper.

- It logs in as `wbadmin` with SSH password authentication, using the
  line from its standard input, and offers no other method.
- It writes the same password into the session's standard input, where
  the guest's `sudo -S` reads it.
- It doesn't request a terminal.

The session runs only on the build VM's own file-handle network,
through the host's userspace stack. `wb-netd` connects to the guest's
port 22 for the build VM only, and hands the socket on
(S07-18-build-network). No third party can join
that network, so the channel authenticates the guest
(S06-vm-lifecycle).

`wb-build-ssh` therefore doesn't check the guest's host key. It
accepts the key the guest offers, and logs its fingerprint. The design
relies on this ("What first contact relies on").

A session that doesn't complete, or that passes one of its deadlines
(S04-05-helper-programs, "Deadlines"), burns the build password:

- the build fails;
- its bundle is deleted;
- cleanup removes its items.

The build never writes the password into the build bundle, and its
test searches the bundle for it.

### One password per VM

Every system disk is a clone of the image. At first contact in each
fresh clone, `wb-guestadmin-rotate` runs before any other request. It
runs before `wb-hostd` sends the time, and before `wb-guestd` lists
its sessions (S06-vm-lifecycle).

1. It refuses when the store already holds `wbadmin/vm/<vm>/<disk>`,
   whatever `wb-hostd` or the guest reports.
2. Otherwise it fetches the image's item, and generates the new
   password.
3. It writes `wbadmin/vm/<vm>/<disk>`.
4. It sends the old and new password to `wb-guestd`, on a host-guest
   connection of its own. That connection goes to a port below 1024
   that serves only this. It never reads from that connection.
5. `wb-guestd`, as root, enables `wbadmin`. It runs `sysadminctl
   -resetPasswordFor wbadmin -newPassword - -adminUser wbadmin
   -adminPassword -` with both passwords on standard input. Then it
   disables `wbadmin` again, and reports the result to `wb-hostd`.
6. On success, `wb-hostd` marks the VM item current.

The build VM rotates the same way, from the build password to the
image password.

The layer VM of a Claude Code layer build rotates the same way, from
the parent's password to the new image's (S06-01-image-layers, "The
Claude Code layer"). There, `wb-guestadmin-rotate`:

- refuses when the store already holds the new image's item;
- fetches the parent's item, and generates the new image's password;
- writes `wbadmin/image/<image>` for the new image, in place of a VM
  item, before it sends both passwords.

So each image has a password of its own, and the parent's password
opens nothing in the images built on it, or in their VMs.

### Rotation failure

On a failed report or no answer:

- both items stay;
- `wb-hostd` marks the disk "rotation unknown";
- every session start in that VM is refused with the rule
  `admin-rotation-unknown`.

Recovery replaces the system disk, and cleanup removes the disk's
item.

### What first contact relies on

`wb-guestd` is honest at first contact. The disk is a fresh clone of a
sealed image, and no project code has run on it.

Nothing from the data disk runs before rotation. `wb-guestd` starts no
project process and no session until rotation has succeeded, and no
user is logged in.

`wbadmin` is enabled only during the reset, when nothing else runs
that could use it. C09-guest-and-sessions has the conformance case.

The build's SSH session relies on its channel, not on a host key. The
channel is the build VM's own file-handle attachment to `wb-netd`'s
stack, so the only peer `wb-build-ssh` can reach is the build guest
behind that stack. To sit in the middle, an attacker would already
have to control a host process on that path: `wb-vmd`, `wb-netd` or
`wb-hostd`. A fresh build
guest also has no host key the host could know in advance.

### Break-glass

When `wb-guestd` is dead or wedged, `wb-hostd` replaces the VM's
system disk with a fresh clone of the image. The data disk stays, and
first contact rotates again. No password is fetched and nobody logs
in. `wb-guestadmin-cleanup` removes the old disk's item.

What guest root wrote to the data disk stays through break-glass
(T18-data-disk-persistence).

The same replacement runs at the next start of a VM whose system
disk the admin path marked (S15-05-admin-path, "Marked"). To look
inside a VM before its disk is replaced, the person at the host's
terminal uses the admin path, whose guest end runs apart from
`wb-guestd`.

### Refused outside the path

`wb-vmd` refuses provisioning options unless its slot is `build`. That
is the slot of `wb-vmd-install`, whose only bundle is the new build
bundle under `<data>/images/`. It logs `vmd-provisioning-refused`
(S04-architecture).

`wb-launcher` and `wb-guestadmin` accept only requests whose fields
are item names in the fixed format of S15-03-access-rules, "Declared access". Each refusal is logged with its rule.

No step of this page goes through `wb-guestroot`'s port, and the admin
path fetches no `wbadmin/*` item (S15-05-admin-path).

### Not readable in the image

The image has no automatic login, no `/etc/kcpassword`, and no
keychain of `wbadmin`'s. The sealing scan checks that
(S06-vm-lifecycle).

## Decisions

### The password on standard input, not through a terminal

`wb-build-ssh` gets the build password on its standard input, logs in
with it, and writes it into the session's standard input for
`sudo -S`. It never answers a prompt through a terminal.

- **Why:** the password then never passes through a terminal stream,
  and no askpass program sits between the pipe and the login.

**Status:** Draft
