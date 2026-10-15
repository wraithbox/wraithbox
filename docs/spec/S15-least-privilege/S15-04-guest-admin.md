# S15-04 - The Guest Admin User

**Purpose:** How the host keeps the password of the guest admin user `wbadmin`,
from the image build to each VM's first contact.

## Design

### The guest admin user

The provisioning options create the user `wbadmin`, the volume's only
secure-token holder, which macOS refuses to delete (X17-image-build).
It stays in the image as a host-administered account: disabled,
hidden, without a shell and out of `admin` (S06-02-image-build, "Sealing"). The host keeps its password in the store, and the secure
token keeps working. That keeps SEC11-root-gains-nothing (guest root
gains no control over the host), because the asset never leaves its
VM. The password and the token unlock only that VM's disk, nothing
they unlock crosses the Virtualization boundary, and no VM can reach
another VM's `wbadmin`. Guest root can't read the password back,
because the guest keeps only what macOS keeps for any account, and the
build deletes `wbadmin`'s keychains before sealing. `wb-guestadmin`
(S04-architecture) does each step below, and only it reads or writes
`wbadmin/*`.

### Build and rotation

- **Build.** `wb-guestadmin-build` generates the build password and
  writes `wbadmin/build/<build>`. `wb-hostd` creates two pipes, and
  `wb-launcher` hands one read end to the build's `wb-vmd` and the other
  to `wb-build-ssh` as its standard input. `wb-guestadmin-build` writes
  the password to the first, and two newline-terminated copies to the
  second.
- **The build's SSH session.** `wb-build-ssh` (S04-architecture) runs
  the system `ssh` with `-F /dev/null -o UserKnownHostsFile=/dev/null
  -o IdentitiesOnly=yes`, with `SSH_ASKPASS_REQUIRE=force`, and with
  `SSH_ASKPASS` set to the path of `wb-askpass` in the bundle, a
  program path and not the value. `wb-askpass` reads one line from its
  standard input, the first copy, and prints it for the login. The
  second line reaches the guest's `sudo -S` on the session's standard
  input. Standard input was chosen over answering the prompt through a
  terminal, because the password then never passes through a terminal
  stream. The session runs only on the build VM's own file-handle
  network through the host's userspace stack, which no third party can
  join, so the channel authenticates the guest (S06-vm-lifecycle). A session that doesn't complete burns the build password.
  The build then fails, its bundle is deleted, and cleanup removes its
  items. The build never writes the password into the build bundle, and
  its test searches the bundle for it.
- **One password per VM.** Every system disk is a clone of the image.
  At first contact in each fresh clone, `wb-guestadmin-rotate` runs
  before any other request: before `wb-hostd` sends the time and before
  `wb-guestd` lists its sessions (S06-vm-lifecycle). It refuses when the
  store already holds `wbadmin/vm/<vm>/<disk>`, whatever `wb-hostd` or
  the guest reports. Otherwise it fetches the image's item, generates
  the new password, writes `wbadmin/vm/<vm>/<disk>`, and sends the old
  and new password to `wb-guestd` on a host-guest connection of its
  own, to a port below 1024 that serves only this. It never reads from
  that connection. `wb-guestd`, as root, enables `wbadmin`, runs
  `sysadminctl -resetPasswordFor wbadmin -newPassword - -adminUser wbadmin -adminPassword -`
  with both passwords on standard input, disables `wbadmin` again, and
  reports the result to `wb-hostd`. On success, `wb-hostd` marks the VM
  item current. The build VM rotates the same way, from the build
  password to the image password.
- **Rotation failure.** On a failed report or no answer, both items
  stay, `wb-hostd` marks the disk "rotation unknown", and every session
  start in that VM is refused with the rule. Recovery replaces the
  system disk, and cleanup removes the disk's item.
- **What first contact relies on.** `wb-guestd` is honest at first
  contact, because the disk is a fresh clone of a sealed image and no
  project code has run on it. Nothing from the data disk runs before
  rotation, because `wb-guestd` starts no project process and no
  session until rotation has succeeded, and no user is logged in.
  `wbadmin` is enabled only during the reset, when nothing else runs
  that could use it. C09-guest-and-sessions has the conformance
  case.
- **Break-glass.** When `wb-guestd` is dead or wedged, `wb-hostd`
  replaces the VM's system disk with a fresh clone of the image. The
  data disk stays, and first contact rotates again. No password is
  fetched and nobody logs in. `wb-guestadmin-cleanup` removes the old
  disk's item.
- **Refused outside the path.** `wb-vmd` refuses provisioning options
  unless its slot is `build`, the slot of `wb-vmd-install`, whose only
  bundle is the new build bundle under `<data>/images/`, and logs
  `vmd-provisioning-refused` (S04-architecture).
  `wb-launcher` and `wb-guestadmin` accept only requests whose fields
  are item names in the fixed format of S15-03-access-rules, "Declared access". Each refusal is logged with
  its rule.
- **Not readable in the image.** No automatic login, no
  `/etc/kcpassword`, and no keychain of `wbadmin`'s, which the sealing
  scan checks (S06-vm-lifecycle).

**Status:** Draft
