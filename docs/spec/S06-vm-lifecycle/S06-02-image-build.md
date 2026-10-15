# S06-02 - Image Build

**Purpose:** How `wb image build` makes a guest image: the build VM,
its two provisioning stages, terminfo, sealing, and storage.

## Design

### Built by Wraith Box

`wb image build` installs macOS into a new VM from a restore image in a
local file. Then it provisions the VM in two stages (X17-image-build).
Builds are scripted and repeatable. Nobody configures an image by hand.

The build VM runs in the fixed build bundle
`<data>/images/macos-build.noindex`. It never runs in a session VM's
bundle, or in a path from a request.

Its `wb-vmd` is started through the fixed `wb-launcher` entry
`wb-vmd-install`. That entry is confined to the build bundle, and is
the only `wb-vmd` with the install rules (S04-architecture). The same
instance:

1. installs macOS;
2. runs the provisioning boot and the boots of the second stage;
3. exits when the build ends.

The install reads only the restore image at the fixed path
`<data>/images/macos-restore.ipsw`.

- `wb-download` fetches it from Apple's catalog.
- `wb-hostd` checks its hash before it starts `wb-vmd-install`
  (S17-03-downloads).
- `wb-vmd-install` checks the path again itself (S04-architecture).

A build that seals its image deletes the restore image.

#### The build bundle

Each build starts with a new, empty build bundle. Before it starts
`wb-vmd-install`, `wb-hostd`:

1. deletes what an earlier build left there, without following
   symbolic links;
2. creates the bundle directory.

When `wb-vmd-install` exits during a build, the build fails.
`wb-hostd` deletes the build bundle (S15-least-privilege, "The build's
SSH session"), and nothing restarts.

#### Taking the sealed image

`wb-hostd` treats what is in the build bundle as written by a possibly
compromised `wb-vmd` (S04-architecture). It takes a sealed image only
after `wb-launcher` reports that `wb-vmd-install` has exited. No
process of the build can then still change the bundle.

It takes the system disk and the auxiliary storage, each at its fixed
name in the bundle. For each one it:

1. `lstat`s the name, and requires a regular file with `st_nlink` 1;
2. opens it with `O_NOFOLLOW`;
3. requires that `fstat` on the descriptor gives the same `st_dev` and
   `st_ino`, as `wb-diskfmt` checks its file (S06-05-disks);
4. clones it into `<data>/images/` from that checked descriptor, with
   `fclonefileat` and `CLONE_NOFOLLOW`, and never acts on the path
   again.

It then deletes the build bundle, without following symbolic links.
Any other result fails the build, and `wb-hostd` logs the rule
`build-bundle-not-regular`.

A clone of a stored image from `<data>/images/` into a VM bundle uses
the same identity check ("Storage and distribution" below, and
S06-03-image-updates). It clones from the checked descriptor with
`CLONE_NOFOLLOW`.

#### Provisioning stages

The first stage is the provisioning boot.

- The first boot sets the macOS provisioning options. They are new in
  macOS 27, so `wb image build` runs on a macOS 27 host.
- The options create the user `wbadmin`, and turn Remote Login on.
  They get the guest past Setup Assistant with no clicks.
- One SSH session as `wbadmin` installs `wb-guestd` as a root
  LaunchDaemon, and nothing else.
- That session runs on the build VM's own file-handle network, through
  the host's userspace stack. Only the host and the build guest can
  join that network. The channel therefore authenticates the guest.
- A session that doesn't complete fails the build.

The second stage goes through `wb-guestd`. Everything after the
provisioning boot goes over the host-guest socket to `wb-guestd`:

1. At first contact, before any other request, `wbadmin`'s password is
   rotated from the build's password to the image's.
   `wb-guestadmin` writes the image's password to the store first.
   Both passwords reach `wb-guestd` on a connection of their own, and
   `sysadminctl` on standard input. The build fails if the rotation
   fails.
2. The build turns Remote Login and automatic login off.
3. The build takes `wbadmin` out of every group, disables it, and
   deletes its keychains.
4. The base layer is installed through `wb-guestd`.
5. The image is sealed ("Sealing" below).

After the rotation the secure token keeps working, and the host alone
can unlock it.

macOS refuses to delete `wbadmin`, because it holds the volume's only
secure token. So the image keeps it as a host-administered account.

#### The guest admin password

The provisioning options give `wbadmin` a password, and the build's
one SSH session needs it. From then on the host keeps that user's
password in the configured secret store. S15-least-privilege declares
who may write, fetch, or receive it.

- `wb-guestadmin` (S04-architecture) generates a password for each
  build, and writes it to the store. It hands it on pipes to the
  build's `wb-vmd`, until the start call returns. It hands it to
  `wb-build-ssh` for its one session.
- Provisioning options for a session VM are refused. The refusal is
  logged with its rule.
- The password is never in arguments. It is never written to disk
  outside the store, and never in a log, a gRPC message, or an audit
  record.
- The guest gets it only on standard input.

#### No guest disk mounts

The host never mounts a guest disk to build an image
(SEC15-no-guest-disk-mount). `wb-guestd` provisions through named
operations instead:

- it has no operation that runs an arbitrary command as root;
- it takes secrets on standard input, never in arguments.

Every clone of an image starts with the same per-machine keys. So
sealing deletes the SSH host keys (`/etc/ssh/ssh_host_*_key`). The
local Kerberos realm (`LKDC`) of `wbadmin` is also the same in every
clone.

An image is rebuilt from a newer restore image, never updated in
place.

### Guest confinement

The base image includes the Network Extension of
S13-guest-confinement, approved during the build, once spike
X14-flow-attribution allows it.

### Terminfo

Sessions get the host's `TERM` unchanged (S16-03-around-the-stream, "Terminfo"). The image describes it with a terminfo database that the
build compiles into `/usr/local/share/terminfo`. Root owns it, and no
other user can write to it.

The source is `misc/terminfo.src` from an ncurses release tarball, 6.6
or later, pinned by version and SHA-256. The build appends two alias
entries:

```text
xterm-ghostty|Ghostty terminal emulator,
	use=ghostty,
xterm-kitty|KovId's TTY,
	use=kitty,
```

Ghostty sets `TERM=xterm-ghostty`, and kitty sets `TERM=xterm-kitty`.
But ncurses names their entries `ghostty` (first in release 6.6) and
`kitty` (first in release 6.2). The aliases point those names at
ncurses' reviewed entries.

The guest compiles the database, because the host never writes the
guest disk, and a root-owned file has to be created in the guest. For
X17-image-build (I28):

1. The host checks the tarball's SHA-256.
2. It sends the source to a named `wb-guestd` operation.
3. The operation compiles it with `tic -x` from the same ncurses
   release, and installs the result as root.
4. It returns success, or a failure with a text.

A failure fails the build. The host cuts that text to 1024 bytes, and
escapes it wherever it shows it. It does the same for the version
`wb-guestd` reports (S04-08-records-and-bounds, "Version skew").

`wb-guestd` sets `TERMINFO_DIRS=/usr/local/share/terminfo:` for
project users. macOS's ncurses also falls back to
`/usr/share/terminfo` without the empty last entry. The empty entry
keeps that fallback explicit.

#### macOS's ncurses

`/usr/bin/less`, `vim` and `top` link macOS's own ncurses 6.0. It
can't read an entry in the extended number format, which `tic` from
ncurses 6.1 or later writes for an entry with a number above 32767.
`kitty` and `xterm-kitty` (`pairs#0x10000`) are among them.

- Those programs report an unknown terminal for kitty.
- Programs that link Homebrew's ncurses read the entry.
- For a name macOS ships, such as `xterm-256color`, macOS's ncurses
  skips the file it can't read, and uses its own copy.

This was measured on a macOS 27 host with ncurses 6.6 (I83). I203
decides whether `xterm-kitty` gets an entry macOS can read.

### Sealing

Before an image is usable, `wb-guestd` scans it as root, before any
project code has run. The host parses no guest filesystem
(SEC15-no-guest-disk-mount). Any finding fails the build:

- anything that looks like a secret, such as keychain items, tokens in
  dotfiles, SSH keys and shell history, and `/etc/kcpassword`
  (SEC04-no-guest-secrets);
- an enabled account in the `admin` or `wheel` group, other than root
  and the system accounts macOS ships in it. Membership is checked
  with `dsmemberutil checkmembership`, which sees both
  `GroupMembership` and `GroupMembers` (X27-vsock-confinement);
- any sudoers entry beyond macOS's own (X27-vsock-confinement);
- a non-system account, except `wbadmin` when it meets the conditions
  below (X17-image-build);
- Remote Login (`com.openssh.sshd` enabled or loaded, or a listener on
  TCP port 22), screen sharing or remote management, and automatic
  login (SEC04-no-guest-secrets, SEC05-default-deny);
- an `~/.ssh/authorized_keys` of any user, root included;
- a change to `sshd_config`, or a file in `sshd_config.d` that macOS
  doesn't ship, and SSH host keys left in `/etc/ssh`;
- files outside `/Users` owned by a non-system user ID, other than the
  temporary folders of `wbadmin`;
- launchd jobs outside an allowlist: what macOS ships (`com.apple.*`
  and `amsdstat.plist` in `/Library/LaunchDaemons`), plus
  `wb-guestd`'s.

So any admin account the build creates is disabled or removed before
the image is sealed.

#### The `wbadmin` exception

The sealing scan accepts `wbadmin` only if all of these hold:

- It is the volume's only crypto user:
  `diskutil apfs listCryptoUsers` lists one.
- Its `AuthenticationAuthority` has `;SecureToken;`.
- It is disabled, hidden (`IsHidden`), and without a shell.
- It is in no groups but `staff`, `everyone`, `localaccounts`,
  `_lpoperator`, `com.apple.sharepoint.group.*`, and
  `com.apple.access_disabled`. The last is the group `pwpolicy` adds
  when it disables an account.
- Its `~/Library/LaunchAgents` is empty.
- Its `~/Library/Keychains` is empty.

### Storage and distribution

Images are stored locally. They are cloned copy-on-write into VM
bundles: APFS clones on macOS (S12-platforms). Sharing images between
machines as OCI artifacts in a registry is a later addition.

## Decisions

### A separate `wb-vmd-install`

Only the `wb-vmd` started as `wb-vmd-install` has the install rules,
and it is confined to the build bundle.

- **Decided:** I64.

### Provision through `wb-guestd`, never a mounted disk

- **Why:** an unprivileged host process can't create a root-owned file
  on a guest disk, so launchd refuses anything the host would write
  there. Root on the host is ruled out by SEC12-least-privilege.
- **Decided:** X17-image-build.

### Compile terminfo in the guest from ncurses

- **Why:** the aliases use ncurses' reviewed entries.
- **Rejected:** a terminfo source from an emulator, which the image
  doesn't ship. Kitty's is GPL-3.0 (S10-tech-stack).
- **Rejected:** macOS's own `tic` (ncurses 6.0). On the raw 6.6
  `terminfo.src` it stops with `error writing` at `scrt`. Limited to
  the entries above, it stops at `kitty`.
- **Decided:** I83.

**Status:** Draft
