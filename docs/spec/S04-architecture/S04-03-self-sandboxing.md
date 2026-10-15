# S04-03 - Self-Sandboxing

**Purpose:** How each host daemon confines itself at startup, and the
`wb-launcher` that starts confined programs on macOS.

## Design

### Each host daemon is self-sandboxed

Host processes confine
themselves at startup with the platform's mechanism (S12-platforms) so that
each gets only what it needs: `wb-netd` no filesystem and no network
beyond inherited descriptors; `wb-proxyd` outbound network and its
credential store items; `wb-download` outbound HTTPS and its
inherited descriptors; `wb-guestadmin` its `wbadmin/*` store items
and inherited descriptors; `wb-credset` its `binding/*` store items
and its standard streams (S15-least-privilege, "Keychain"); `wb-hostd` its state directories, and on
macOS read access to the installed bundle's `Info.plist` (S04-07-supervision, "Upgrades")
and to each helper bundle's `embedded.provisionprofile`
(S12-platforms, "Development builds on macOS"); `wb-vmd` the
hypervisor and its VM's bundle. This hardens our own code; it is not how
the agent is contained (the VM is). How it is done (X23-sandboxed-daemons,
SEC12-least-privilege):

### Confined first thing in `main`

A daemon confines itself first thing in `main`, from a profile
compiled into it, and exits if that fails. Nothing before that reads
an inherited descriptor, the arguments or the environment. Right
after, it tries one operation its profile denies, and exits if that
succeeds.

### `wb-hostd` before it confines itself

`wb-hostd` is one exception. Before it confines itself, it reads its
own configuration, sets `GOMAXPROCS`, loads the local time zone,
starts `wb-launcher` (on macOS), resolves the git binary from
`<config>` or the fixed platform path, never from `PATH`, checks
its version against the minimum (S08-08-host-git)
and that it honors `GIT_ALLOC_LIMIT` (S08-04-push-checks, "Pack scanner"), and works out the paths for its profile. None of that reads
anything the guest sent.

### `wb-vmd` before it confines itself

`wb-vmd` on macOS is a second, bounded exception. Before it confines
itself, it calls `confstr(_CS_DARWIN_USER_CACHE_DIR)` once, because
the framework needs the per-user cache directory and the lookup
goes to a system service the profile denies. libSystem keeps the
answer. It also reads its slot from the `WB_VMD_SLOT` environment
variable that its launcher entry sets (S04-04-vm-processes), looks up the user's
home directory with `getpwuid(getuid())`, never from `HOME`, and
resolves its slot's bundle path with `realpath`, to get its profile
parameter. It doesn't read anything
from the guest or `wb-hostd` before it confines itself.
The profile's parameters are the bundle path, from the fixed
platform path (S12-platforms, "Paths") resolved to its real path,
and the cache directory from `confstr`. The gRPC contract carries
no path. The profile allows reading and writing that one bundle,
the Virtualization service, the `sysctl` values and system files
the framework checks (the CPU name among them, without which saved
states don't restore across profiles), and the sandbox extensions
the framework hands to its service process, with the cache
directory extension limited to its `com.apple.metal-` and
`com.apple.paravirtualizedgraphics-` entries. It denies the user's
other files, the other VMs' bundles among them, the network,
starting programs, and other services (X25-vmd-sandbox). The
extension classes were measured with the X18-vsock-handoff
configuration, which also had a Mac graphics device with one
display and a USB keyboard and pointer. They are ablated again
against the final device set of S06-vm-lifecycle.

### Daemon logs

`wb-netd`, `wb-vmd`, `wb-guestadmin`, `wb-build-ssh`, `wb-diskfmt`,
`wb-release-check` and `wb-download` write their logs to an inherited pipe or socket to `wb-hostd`, which
frames, attributes and rate-limits each line. None of them holds a
descriptor on any file in `<logs>` or `<data>` for its log.

### `wb-launcher`

On macOS a confined process can't confine itself again, and its
children inherit its profile. So `wb-hostd` starts `wb-vmd`,
`wb-netd`, `wb-proxyd`, `wb-git`, `wb-prover`, `wb-guestadmin`,
`wb-build-ssh`, `wb-diskfmt`, `wb-release-check` and `wb-download` through
`wb-launcher`, which has no profile. On Linux, Landlock and seccomp restrictions stack, so a
child can confine itself further and needs no launcher (assumed from
their documentation, not tested; to be confirmed when Linux
self-sandboxing is built).
`wb-launcher` doesn't widen what a compromised `wb-hostd` can do
only while all of these hold:
- It is a separate, small program that uses only the Go standard
  library, not `wb-hostd` started in another mode.
- Its request channel is a `SOCK_SEQPACKET` or `SOCK_DGRAM`
  socketpair inherited at start, never a path. Each message is one
  fixed-format request. A request with unexpected descriptors has
  them closed and is refused.
- It starts each program with no arguments and a fixed environment.
  It passes only the descriptors `wb-hostd` hands it. The two
  bounded exceptions are `wb-git` and `wb-build-ssh` (S04-06-git-confinement, S04-05-helper-programs).
- Each program's profile and its parameters are compiled into it or
  come from configuration `wb-hostd` read before it confined
  itself, never over the request channel.
- Its program table holds absolute paths in the installed bundle,
  never under `<config>`, `<data>` or `<logs>`, and it checks that
  when it starts. A program that holds a Keychain access group is
  the main executable of its own helper bundle (S10-tech-stack,
  "Packaging and signing"). So the `wb-proxyd` entry names
  `Contents/Helpers/wb-proxyd.app/Contents/MacOS/wb-proxyd`, and
  the `wb-guestadmin-build`, `wb-guestadmin-rotate` and
  `wb-guestadmin-cleanup` entries name
  `Contents/Helpers/wb-guestadmin.app/Contents/MacOS/wb-guestadmin`.
  The entries of programs without a group name `Contents/MacOS`.
  The paths are the same in every build, ad-hoc included.
- Each program has an instance cap (SEC13-bounded-resources),
  and the `wb-download` entries also a file size limit.

**Status:** Draft
