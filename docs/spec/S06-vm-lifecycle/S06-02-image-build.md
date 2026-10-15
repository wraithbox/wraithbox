# S06-02 - Image Build

**Purpose:** How `wb image build` makes a guest image: the build VM, its two
provisioning stages, terminfo, sealing and storage.

## Design

### Built by Wraith Box

`wb image build` installs macOS into a new
VM from a restore image in a local file, then provisions it in two
stages (X17-image-build). Builds are scripted and repeatable, and
nobody configures an image by hand.

The build VM runs in the fixed build bundle
`<data>/images/macos-build.noindex`, never in a session VM's bundle
and never in a path from a request. Its `wb-vmd` is started through
the fixed `wb-launcher` entry `wb-vmd-install`, which is confined to
that bundle and is the only `wb-vmd` with the install rules
(S04-architecture, decided on I64). The same instance installs macOS,
runs the provisioning boot and the boots of the second stage, and
exits when the build ends.
The install reads only the restore image at the fixed path
`<data>/images/macos-restore.ipsw`. `wb-download` fetches it from
Apple's catalog, and `wb-hostd` checks its hash before it starts
`wb-vmd-install` (S17-install-and-upgrade, "Downloads"), which checks
the path again itself (S04-architecture). A build that seals its
image deletes the restore image.

#### The build bundle

Each build starts with a new, empty build bundle. `wb-hostd` deletes
what an earlier build left there, without following symbolic links,
and creates the bundle directory before it starts `wb-vmd-install`.
When `wb-vmd-install` exits during a build, the build fails,
`wb-hostd` deletes the build bundle (S15-least-privilege, "The
build's SSH session"), and nothing restarts.

`wb-hostd` treats what is in the build bundle as written by a
possibly compromised `wb-vmd` (S04-architecture). It takes a sealed
image only after `wb-launcher` reports that `wb-vmd-install` has
exited, so no process of the build can still change the bundle. It
takes the system disk and the auxiliary storage, each at its fixed
name in the bundle, and for each:
- `lstat`s the name and requires a regular file with `st_nlink` 1,
  opens it with `O_NOFOLLOW`, and requires that `fstat` on the
  descriptor gives the same `st_dev` and `st_ino`, as `wb-diskfmt`
  checks its file (S06-05-disks);
- clones it into `<data>/images/` from that checked descriptor, with
  `fclonefileat` and `CLONE_NOFOLLOW`, and never acts on the path
  again.

It then deletes the build bundle without following symbolic links.
Any other result fails the build, and `wb-hostd` logs the rule
`build-bundle-not-regular`. A clone of a stored image from
`<data>/images/` into a VM bundle ("Storage and distribution",
and S06-03-image-updates) uses the same identity check, and clones from the checked
descriptor with `CLONE_NOFOLLOW`.

#### Provisioning stages

1. *Provisioning boot.* The first boot sets the macOS provisioning
   options, which are new in macOS 27, so `wb image build` runs on a
   macOS 27 host. The options create the user `wbadmin` and turn
   Remote Login on, and get the guest past Setup Assistant with no
   clicks. One SSH session as `wbadmin` installs `wb-guestd` as a
   root LaunchDaemon and nothing else. That session runs on the build
   VM's own file-handle network through the host's userspace stack,
   which nothing but the host and the build guest can join, so the
   channel authenticates the guest. A session that doesn't complete
   fails the build.

   *The guest admin password.* The provisioning options give
   `wbadmin` a password, and the build's one SSH session needs it. The host keeps that user's password in the configured
   secret store from then on, and S15-least-privilege declares who
   may write, fetch or receive it:
   - `wb-guestadmin` (S04-architecture) generates a password for
     each build, writes it to the store, and hands it on pipes to the
     build's `wb-vmd` until the start call returns and to
     `wb-build-ssh` for its one session;
   - provisioning options for a session VM are refused, and the
     refusal is logged with its rule;
   - the password is never in arguments, never written to disk
     outside the store, and never in a log, a gRPC message, or an
     audit record;
   - the guest gets it only on standard input.
2. *Through `wb-guestd`.* Everything after that goes over the
   host-guest socket to `wb-guestd`. At first contact, before any
   other request, `wbadmin`'s password is rotated from the build's
   password to the image's, which `wb-guestadmin` writes to the store
   first. Both reach `wb-guestd` on a connection of their own, and
   `sysadminctl` on standard input. The secure token keeps working,
   and the host alone can unlock it. The build fails if the rotation
   fails. It then turns Remote Login and automatic login off.
   macOS refuses to delete `wbadmin`, because it holds the volume's
   only secure token. The image keeps it as a host-administered
   account, and the build takes it out of every group, disables it,
   and deletes its keychains. Then the base layer is installed
   through `wb-guestd`, and the image is sealed.

#### No guest disk mounts

The host never mounts a guest disk to build an image
(SEC15-no-guest-disk-mount). An
unprivileged host process can't create a root-owned file on it, so
launchd refuses anything the host would write there, and root on the
host is ruled out by SEC12-least-privilege (X17-image-build).
`wb-guestd` provisions through named operations: it has no operation
that runs an arbitrary command as root, and it takes secrets on
standard input, never in arguments. Every clone of an image starts
with the same per-machine keys, so sealing deletes the SSH host keys
(`/etc/ssh/ssh_host_*_key`). The local Kerberos realm (`LKDC`) of `wbadmin` is
also the same in every clone. An image is rebuilt from a newer
restore image, never updated in place.

### Guest confinement

The base image includes the Network Extension
of S13-guest-confinement, approved during the build, once spike X14-flow-attribution allows it.

### Terminfo

(I83). Sessions get the host's `TERM` unchanged
(S16-terminal-stream, "Terminfo"), and the image describes it with a
terminfo database that the build compiles into
`/usr/local/share/terminfo`. Root owns it, and no other user can
write to it. The source is `misc/terminfo.src` from an ncurses
release tarball, 6.6 or later, pinned by version and SHA-256, with
two alias entries appended:

```text
xterm-ghostty|Ghostty terminal emulator,
	use=ghostty,
xterm-kitty|KovId's TTY,
	use=kitty,
```

Ghostty sets `TERM=xterm-ghostty` and kitty sets `TERM=xterm-kitty`,
but ncurses names their entries `ghostty` (first in release 6.6) and
`kitty` (first in release 6.2). The aliases point those names at
ncurses' reviewed entries instead of a terminfo source from an
emulator, which the image doesn't ship (kitty's is GPL-3.0,
S10-tech-stack). The guest compiles it, because the host never writes
the guest disk and a root-owned file has to be created in the guest
(above). For X17-image-build (I28): the host checks the tarball's
SHA-256 and sends the source to a named `wb-guestd` operation, which
compiles it with `tic -x` from the same ncurses release and installs
the result as root. The operation returns success or a failure with
a text. A failure fails the build, and the host cuts that text to
1024 bytes and escapes it wherever it shows it, as it does the
version `wb-guestd` reports (S04-08-records-and-bounds, "Version skew"). The build doesn't use macOS's own `tic`
(ncurses 6.0): on the raw 6.6 `terminfo.src` it stops with
`error writing` at `scrt`, and at `kitty` when limited to the
entries above.
`wb-guestd` sets `TERMINFO_DIRS=/usr/local/share/terminfo:` for
project users. macOS's ncurses also falls back to
`/usr/share/terminfo` without the empty last entry, which keeps that
fallback explicit.
- *macOS's ncurses.* `/usr/bin/less`, `vim` and `top` link macOS's
  own ncurses 6.0, which can't read an entry that `tic` from
  ncurses 6.1 or later writes in the extended number format, used
  for an entry with a number above 32767. `kitty` and `xterm-kitty`
  (`pairs#0x10000`) are among them. Those programs report an unknown
  terminal for kitty, and programs that link Homebrew's ncurses read
  the entry. For a name macOS ships, such as `xterm-256color`,
  macOS's ncurses skips the file it can't read and uses its own
  copy. Measured on a macOS 27 host with ncurses 6.6 (I83). I203
  decides whether `xterm-kitty` gets an entry macOS can read.

### Sealing

Before an image is usable, `wb-guestd` scans it as root,
before any project code has run. The host parses no guest
filesystem (SEC15-no-guest-disk-mount). Any finding fails the build:
- anything that looks like a secret (keychain items, tokens in
  dotfiles, SSH keys, shell history) and `/etc/kcpassword`
  (SEC04-no-guest-secrets);
- an enabled account in the `admin` or `wheel` group, other than
  root and the system accounts macOS ships in it, with membership
  checked by `dsmemberutil checkmembership` (which sees both
  `GroupMembership` and `GroupMembers`), and any sudoers entry
  beyond macOS's own (X27-vsock-confinement);
- a non-system account, except `wbadmin` if all of these
  hold (X17-image-build): it is the volume's only crypto user
  (`diskutil apfs listCryptoUsers` lists one) and its
  `AuthenticationAuthority` has `;SecureToken;`; it is disabled,
  hidden (`IsHidden`), and without a shell; it is in no groups but
  `staff`, `everyone`, `localaccounts`, `_lpoperator`,
  `com.apple.sharepoint.group.*`, and `com.apple.access_disabled`
  (the group `pwpolicy` adds when it disables an account); its
  `~/Library/LaunchAgents` is empty; and its `~/Library/Keychains`
  is empty;
- Remote Login (`com.openssh.sshd` enabled or loaded, or a listener
  on TCP port 22), screen sharing or remote management, and
  automatic login (SEC04-no-guest-secrets, SEC05-default-deny);
- an `~/.ssh/authorized_keys` of any user, root included, a change to
  `sshd_config` or a file in `sshd_config.d` that macOS doesn't
  ship, and SSH host keys left in `/etc/ssh`;
- files outside `/Users` owned by a non-system user id, other than the
  temporary folders of `wbadmin`, and launchd jobs outside an
  allowlist of what macOS ships (`com.apple.*` and `amsdstat.plist`
  in `/Library/LaunchDaemons`) plus `wb-guestd`'s.

So any admin account the build creates is disabled or removed before
the image is sealed.

### Storage and distribution

Images are stored locally and cloned
copy-on-write (APFS clones on macOS; S12-platforms) into VM bundles.
Sharing images between machines as OCI artifacts in a registry is a
later addition.

**Status:** Draft
