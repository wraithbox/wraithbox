# S04-03 - Self-Sandboxing

**Purpose:** How each host daemon confines itself at startup, and the
`wb-launcher` that starts confined programs on macOS.

## Design

### Each host daemon is self-sandboxed

Host processes confine themselves at startup with the platform's
mechanism (S12-platforms). Each then gets only what it needs:

| Process | What it keeps |
|---|---|
| `wb-netd` | no filesystem, and no network beyond inherited descriptors |
| `wb-proxyd` | outbound network, certificate checks, and its credential store items ("`wb-proxyd` and the secret store on macOS") |
| `wb-download` | outbound HTTPS and its inherited descriptors |
| `wb-guestadmin` | its `wbadmin/*` store items and inherited descriptors |
| `wb-credset` | its `binding/*` store items and its standard streams (S15-02-secret-store, "Keychain") |
| `wb-hostd` | its state directories; on macOS also read access to the installed bundle's `Info.plist` (S04-07-supervision, "Upgrades") and to each helper bundle's `embedded.provisionprofile` (S12-03-host-prerequisites, "Development builds on macOS") |
| `wb-vmd` | the hypervisor and its VM's bundle |

This hardens our own code. It is not how the agent is contained: the
VM is. The sections below say how it is done (X23-sandboxed-daemons,
SEC12-least-privilege).

### Confined first thing in `main`

A daemon confines itself first thing in `main`, from a profile
compiled into it, and exits if that fails. Nothing before that reads
an inherited descriptor, the arguments or the environment. Right
after, it tries one operation its profile denies, and exits if that
succeeds.

One shared call in `internal/platform/selfsandbox` confines the
process and runs the probe, so no daemon can leave the probe out:

- Before it confines, it refuses a profile whose parameter names don't
  equal the names of the values given, and a value that is empty or
  holds a NUL byte. The process is then still unconfined, and the
  daemon exits.
- The probe reads `/etc/passwd`, which every profile denies. The read
  has to fail with `EPERM`. Success, or any other error, makes the
  daemon exit. The probe shows that the profile applied. It doesn't
  show that the profile denies all it should: each daemon's own
  self-checks and the profile's review do that.

`wb-vmd` is written in Swift, so it has its own call, `Confinement` in
`WraithBoxVMD`. Its parameter names are fixed in code. It refuses an
empty value or one with a NUL byte with `vmd-sandbox-param-invalid`,
and runs the same probe.

### `wb-hostd` before it confines itself

`wb-hostd` is one exception. Before it confines itself, it:

1. reads its own configuration;
2. sets `GOMAXPROCS`;
3. loads the local time zone;
4. starts `wb-launcher` (on macOS);
5. resolves the git binary from `<config>` or the fixed platform path,
   never from `PATH`;
6. checks that git's version is at least the minimum
   (S08-08-host-git), and that git honors `GIT_ALLOC_LIMIT`
   (S08-04-push-checks, "Pack scanner");
7. works out the paths for its profile.

None of that reads anything the guest sent.

### `wb-vmd` before it confines itself

`wb-vmd` on macOS is a second, bounded exception. Before it confines
itself, it:

- calls `confstr(_CS_DARWIN_USER_CACHE_DIR)` once. The framework needs
  the per-user cache directory, and the lookup goes to a system service
  the profile denies. libSystem keeps the answer.
- reads its slot from the `WB_VMD_SLOT` environment variable that its
  launcher entry sets (S04-04-vm-processes);
- looks up the user's home directory with `getpwuid(getuid())`, never
  from `HOME`;
- resolves its slot's bundle path with `realpath`, to get its profile
  parameter.

It doesn't read anything from the guest or `wb-hostd` before it
confines itself.

### How `wb-vmd` is started

`wb-vmd` takes no arguments, and refuses any with `vmd-arguments`. It
inherits four descriptors:

| Descriptor | What it is |
|---|---|
| 0 | the lifeline: a pipe or socket from its spawner that it only reads |
| 2 | the log pipe ("Daemon logs") |
| 3 | a listening `AF_UNIX` stream socket for gRPC |
| 4 | the hand-off socket: `AF_UNIX`, `SOCK_DGRAM` (S04-02-vm-provider, "Hand-off on macOS") |

Its profile allows no path outside its bundle, so it can't bind a
socket. The spawner binds it in `<data>/run/` (S04-architecture,
"Inter-process contracts") and passes the listening descriptor. The
spawner is `wb-hostd` through `wb-launcher`, or a test. `wb-hostd`
names the socket `vmd-<slot>-<generation>.sock`, so no two runs share
a path, and removes it when the run ends.

The socket follows these endpoint rules of S04-architecture,
"Inter-process contracts":

- The socket is in the run directory, which `wb-hostd` makes and
  checks before it serves, and which the dialer checks before each
  connect.
- The socket file has mode `0600` after the bind.
- Each end checks the other's user ID: `wb-hostd` dials through
  `localipc`, and `wb-vmd` checks each peer it accepts.

The socket has no `.lock` of its own. `wb-hostd` holds the lock of its
own endpoint for its life, so one `wb-hostd` at a time makes and
removes `vmd-*.sock` files. When it stops, it keeps the lock after it
has closed its own socket, until every `wb-vmd` it spawned has ended.
Before the bind, `wb-hostd` refuses any file already at the path, a
symbolic link included, with `ipc-endpoint-unsafe` and leaves it,
because `bind(2)` follows a dangling link.

When `wb-hostd` crashes, its sockets stay. The next `wb-hostd` removes
every `vmd-*.sock` that is a socket once it holds its endpoint's lock,
before it builds the VM manager. It doesn't probe them with a connect,
because the `wb-vmd` processes of the crashed `wb-hostd` exited
when their lifelines closed. A file of that name that isn't a socket is
left and logged with `ipc-endpoint-unsafe`.

Right after the deny probe, `wb-vmd` checks each descriptor: open, and
of the kind in the table. Descriptors 0 and 2 must be a pipe or a
socket, and descriptor 0 must block. Descriptor 3 must have a path of
its own and no peer. Anything else is refused with `vmd-descriptor-invalid`. So it
never serves on a descriptor its spawner didn't mean.

- **The lifeline.** End of file or an error on descriptor 0 means its
  spawner is gone. `wb-vmd` then stops its VM at once, logs
  `lifeline-closed`, and exits 0. That is its control channel of
  S04-07-supervision, "When `wb-hostd` stops", so a VM never outlives
  the process that asked for it.
- **Each connection.** On every accepted connection, `wb-vmd` first
  reads the peer's user ID with `LOCAL_PEERCRED`. A peer of another
  user ID, or one whose credential can't be read, is closed with
  `ipc-peer-uid` before a byte is read or written. Then comes the
  version exchange of S04-08-records-and-bounds, "Between host
  processes". gRPC reads nothing until both pass, and the peer gets
  nothing but the version record until then.

### The `wb-vmd` profile

The profile's parameters are:

- the bundle path, from the fixed platform path (S12-01-platform-interfaces, "Paths") resolved to its real path;
- the cache directory from `confstr`.

The gRPC contract carries no path.

The profile allows:

- reading and writing that one bundle;
- the Virtualization service;
- the `sysctl` values and system files the framework checks. The CPU
  name is among them: without it, saved states don't restore across
  profiles.
- the sandbox extensions the framework hands to its service process.
  The cache directory extension is limited to its `com.apple.metal-`
  and `com.apple.paravirtualizedgraphics-` entries.
- reading `Virtualization.framework`. Without it, a configuration
  error can't load its message text, and the process ends in an
  uncaught exception instead of returning the error.
- the metadata of `/var` and `/private/var/folders`, where the
  framework finds the cache directory whose path `confstr` gave before
  confinement.

It denies the user's other files, the other VMs' bundles among them.
It also denies the network, starting programs, and other services
(X25-vmd-sandbox).

It doesn't allow reading the metadata of the bundle's parent
directories. So `realpath` of a bundle file fails once `wb-vmd` is
confined, and the bundle file checks take a file's real path from a
descriptor on it instead (S04-02-vm-provider, "The device set is fixed
and checked").

The extension classes were measured with the X18-vsock-handoff
configuration, which has the device set of S06-06-devices-and-warm-start.
Both session VMs' profiles allow the same two cache entry prefixes.
So does the build VM's, because the `wb-vmd-install` profile is this
profile plus the install rules (S04-04-vm-processes). The entries stay
on the host's disk between runs. The VMs share a graphics cache
through them, and a session VM can leave entries that a later image
build may read in the service process of the build VM
(T21-shared-graphics-cache).

V1-M6-release-gate checks whether the set can drop the graphics
device, keyboard and pointer (S06-06-devices-and-warm-start, "The
graphics device"). If it drops them, the profile is ablated again
against the new set.

### `wb-proxyd` and the secret store on macOS

On macOS, "the secret store service" in a profile is two Mach
services:

- `com.apple.securityd.xpc`, the data protection keychain;
- `com.apple.ctkd.token-client`, the Secure Enclave token.

Those two alone make the store-only profile of `wb-credset` and
`wb-guestadmin`. The profile can't name Keychain items. The access
groups in the program's code signature decide which items it reaches
(S15-02-secret-store). The profile decides only whether it reaches the
store at all.

The `wb-proxyd` profile allows outbound TCP to any port, DNS, the
resolver files and `mDNSResponder` as in S17-03-downloads
("Confinement"), certificate checks, and the two store services. Its
`BUNDLE` parameter is the real path of its own helper bundle,
`Contents/Helpers/wb-proxyd.app`. Certificate checks against the
system roots need the metadata read of that directory, with
`com.apple.trustd.agent`. `wb-proxyd` works the path out before it
confines itself.

The profile denies files, listening, starting programs, UDP other than
DNS, and the login keychain (X23-sandboxed-daemons, I61).

### Daemon logs

These write their logs to an inherited pipe or socket to `wb-hostd`:
`wb-netd`, `wb-vmd`, `wb-guestadmin`, `wb-build-ssh`, `wb-diskfmt`,
`wb-release-check` and `wb-download`. `wb-hostd` frames, attributes and
rate-limits each line. None of them holds a descriptor on any file in
`<logs>` or `<data>` for its log.

### `wb-launcher`

On macOS a confined process can't confine itself again, and its
children inherit its profile. So `wb-hostd` starts these through
`wb-launcher`, which has no profile: `wb-vmd`, `wb-netd`, `wb-proxyd`,
`wb-git`, `wb-prover`, `wb-guestadmin`, `wb-build-ssh`, `wb-diskfmt`,
`wb-release-check` and `wb-download`.

On Linux, Landlock and seccomp restrictions stack. A child can confine
itself further and needs no launcher. That is assumed from their
documentation, not tested, and is to be confirmed when Linux
self-sandboxing is built.

### What keeps `wb-launcher` narrow

`wb-launcher` doesn't widen what a compromised `wb-hostd` can do only
while all of these hold:

- **A small program of its own.** It uses only the Go standard
  library. It is not `wb-hostd` started in another mode.
- **A fixed request channel.** The channel is a `SOCK_SEQPACKET` or
  `SOCK_DGRAM` socketpair inherited at start, never a path. macOS has
  no `SOCK_SEQPACKET` for Unix domain sockets, so it uses `SOCK_DGRAM`.
  Each message is one fixed-format request. A request with unexpected
  descriptors has them closed and is refused.
- **Fixed starts.** It starts each program with no arguments and a
  fixed environment. It passes only the descriptors `wb-hostd` hands
  it. The two bounded exceptions are `wb-git` and `wb-build-ssh`
  (S04-06-git-confinement, S04-05-helper-programs).
- **Profiles not from the request.** Each program's profile and its
  parameters are compiled into it, or come from configuration
  `wb-hostd` read before it confined itself. They never come over the
  request channel.
- **Paths in the bundle.** Its program table holds absolute paths in
  the installed bundle, never under `<config>`, `<data>` or `<logs>`.
  It checks that when it starts.
- **Kills only its own `wb-diskfmt`.** A kill request names an
  instance ID of a `wb-diskfmt` entry that it started, never a process
  ID. It signals that shim and its process group only before it reaps
  the shim (S04-04-vm-processes, "`wb-diskfmt`'s kill").
- **Instance caps.** Each program has an instance cap
  (SEC13-bounded-resources). The `wb-download` entries also have a file
  size limit.

A program that holds a Keychain access group is the main executable of
its own helper bundle (S10-02-packaging-and-signing):

- the `wb-proxyd` entry names
  `Contents/Helpers/wb-proxyd.app/Contents/MacOS/wb-proxyd`;
- the `wb-guestadmin-build`, `wb-guestadmin-rotate` and
  `wb-guestadmin-cleanup` entries name
  `Contents/Helpers/wb-guestadmin.app/Contents/MacOS/wb-guestadmin`.

The entries of programs without a group name `Contents/MacOS`. The
paths are the same in every build, ad-hoc included.

**Status:** Draft
