# X23 - Self-sandboxed Go daemons on macOS

**Purpose:** Find out whether `wb-netd`, `wb-proxyd` and `wb-hostd` can
confine themselves with a sandbox profile at start, as S04-architecture
says, without breaking the Go runtime or the work each one does.

**Requirements:** SEC12-least-privilege, and the "Each host daemon is
self-sandboxed" decision of S04-architecture

**Brief:** B34-sandboxed-daemons

## For review

- **Decides:** how a Go daemon confines itself on macOS, the profiles
  for `wb-netd` and `wb-hostd`, and how `wb-hostd` starts the other
  daemons and git so that each can confine itself.
- **You are approving:** each daemon applies its own Seatbelt profile
  in process with no cgo. `wb-netd` runs under a profile that denies
  everything. `wb-hostd` gets its state directories and its socket.
  With open decision 1, you also choose how the other daemons start.
  S04-architecture and S12-platforms describe the recommended option.
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host): kept under option A with the launcher conditions below.
  Options B and C weaken it. No other `SEC*` control changes.
- **Assumed:** that Apple keeps `sandbox_init_with_parameters` working.
  The SDK header marks `sandbox_init` deprecated since macOS 10.8 and
  doesn't declare the variant with parameters at all. Both still work
  on macOS 27.0.1. Only arm64 was run. The amd64 build compiles and was
  not run. The launcher conditions and the `wb-git` shim are proposals
  the spike didn't build.
- **Open decisions:**
  1. How `wb-hostd` starts `wb-vmd`, `wb-netd` and `wb-proxyd`, given
     that a confined process can't confine its children further:
     **A**, a launcher started before `wb-hostd` confines itself
     (recommended), **B**, children inherit the `wb-hostd` profile,
     **C**, `wb-hostd` stays unconfined, **D**, each daemon as its own
     launchd job. B34-sandboxed-daemons compares them.
  2. How git is confined, under option A: a `wb-git` shim that applies
     a profile for one repository and then runs git (recommended,
     untested), or git inheriting the `wb-hostd` profile (measured,
     weaker, residual risk under condition 4, recorded in T00-index if
     chosen).
- **Brief:** B34-sandboxed-daemons

## Question

Can `wb-netd`, `wb-proxyd` and `wb-hostd` confine themselves at start
with a sandbox profile, as S04-architecture says, while the Go runtime,
inherited descriptors, and Keychain and Secure Enclave access in
`wb-proxyd` keep working?

This result covers approach steps 1 and 2 of I34. Step 3, the
`wb-proxyd` profile with Keychain and Secure Enclave access, is I61.
It adds its own section to this page.

## Answer

**Yes, with conditions.**

| Daemon | Answer | Profile |
|---|---|---|
| `wb-netd` | Yes | `(deny default)` and nothing else |
| `wb-hostd` | Yes, with conditions | its state directories, its Unix socket, and running git |
| `wb-proxyd` | Network part yes, Keychain and Secure Enclave part open (I61) | outbound TCP, DNS, certificate checks |

The conditions:

1. **In process, without cgo.** The daemon calls
   `sandbox_init_with_parameters` from `libsystem_sandbox` as its first
   act in `main`. A Go assembly trampoline makes the call, the way
   `golang.org/x/sys/unix` calls the C library. The binary builds with
   `CGO_ENABLED=0` and cross-compiles from any OS. The cgo version
   behaves the same. Starting the daemon under `sandbox-exec` also
   works but needs a larger profile (below). Nothing before the call
   reads an inherited descriptor, the arguments or the environment.
   Right after the call, the daemon tries one operation its profile
   denies, and exits if that succeeds. `wb-hostd` is the exception to
   "first act in `main`": condition 6 lists what it does first.
2. **Set `GOMAXPROCS` before confining.** Go 1.25 and later re-read the
   CPU count (`hw.ncpu`) every second to adjust `GOMAXPROCS`. When the
   profile denies the read, the runtime sees one CPU, sets
   `GOMAXPROCS` to 1, and `wb-netd` throughput drops by about 40
   percent. An explicit `runtime.GOMAXPROCS(runtime.NumCPU())` before the
   call turns that update off, and `GOMAXPROCS` stays at 8 under a bare
   `(deny default)`. The security review on PR65 found this, and the spike
   repeated it. Allowing `sysctl-read` of `hw.ncpu` works too. The
   explicit call is recommended because it keeps the `wb-netd` profile
   empty and doesn't depend on which value the runtime reads.
3. **`wb-hostd` starts its children through a launcher (option A).** A
   confined process can't apply a second profile: the kernel refuses it
   (`forbidden-sandbox-reinit`), and children inherit their parent's
   profile. So `wb-hostd` starts a launcher, `wb-launcher`, before it
   confines itself, and each program the launcher starts confines
   itself. The spike's
   launcher showed that this works, but it was looser than this
   (it passed arguments and the environment, and read requests from a
   stream socket). A launcher that doesn't widen what a compromised
   `wb-hostd` can do needs all of these:
   - It is a separate, small program that uses only the Go standard
     library. It isn't `wb-hostd` started again in another mode, which
     would run the `init` code of every `wb-hostd` dependency
     unconfined.
   - Its request channel is a `SOCK_SEQPACKET` or `SOCK_DGRAM`
     socketpair inherited at start, never a path. It takes one
     fixed-format request per message. A request with unexpected
     descriptors has them closed and is refused.
   - It starts each program with no arguments and a fixed environment.
     The one bounded exception is the repository for `wb-git`
     (condition 4).
   - Each program's profile and its parameters are compiled into the
     program, or come from configuration that `wb-hostd` read before it
     confined itself. They never come over the request channel.
   - Its program table holds absolute paths in the installed bundle,
     never under `<config>`, `<data>` or `<logs>`, and it checks that
     when it starts.
   - Each program has an instance cap (SEC13-bounded-resources).
4. **git gets its own profile (proposal), or inherits `wb-hostd`'s
   (measured).** In the spike, git ran as a child of the confined
   `wb-hostd` and inherited its profile. That works, but a bug in
   `git receive-pack`, which parses packs the guest builds, would then
   have every right of `wb-hostd`. It could read every project's policy,
   change approvals in `state.db`, write other projects' landing
   repositories, and alter the audit log. The proposal: a `wb-git` shim
   that `wb-launcher` starts, which applies a profile for one repository
   and then runs git. That profile allows reading the git installation
   and writing that one repository. The spike didn't build it.

   The repository and the git subcommand differ per request, so
   `wb-git` is the one bounded exception to the launcher's "no
   arguments" rule:
   - The launcher table has two fixed entries, `wb-git-receive` and
     `wb-git-upload`, so the subcommand is never a free string.
   - Besides its program, the request holds one field, the repository.
     `wb-launcher` takes the real path of the projects root
     (`<data>/projects`, from configuration `wb-hostd` read before it
     confined itself) once, when it starts. It accepts the repository
     only if it equals, after cleaning,
     `<real path of the projects root>/<id>/<basename>`, with `<id>` in
     the project ID format and `<basename>` either `landing.git` or
     `export.git` (I41). It never resolves the request string itself,
     which would follow a symbolic link an attacker placed.
   - The shim repeats the same check before it calls `sandbox_init`.
   - The repository becomes the shim's profile parameter and git's one
     path argument.
   - Seatbelt matches resolved paths. The security review on PR65
     reproduced that a symbolic link swapped in after the check is
     denied, not followed to the new target. That Seatbelt match is the
     control that matters, and the path check is defense in depth.
   - Instead of a path, `wb-hostd` could hand over an inherited
     directory descriptor that the launcher checks with `F_GETPATH`,
     which keeps the path out of the request.

   If the maintainer chooses the fallback instead, git under the
   `wb-hostd` profile is recorded in T00-index as an accepted residual
   risk. Either way:
   - The profile allows only the git installation: `bin/git`,
     `libexec/git-core/`, `share/git-core/` and the libraries git links.
     Homebrew git links `pcre2` and `gettext` through
     `/opt/homebrew/opt/<lib>`, a symbolic link into
     `/opt/homebrew/Cellar/<lib>/<version>`. Seatbelt matches resolved
     paths. Each library needs two rules: `file-read-metadata` on the
     `/opt/homebrew/opt/<lib>` literal, and `file-read*` on the resolved
     Cellar subpath, with the real path resolved before confining (the
     security review on PR65 reproduced this). `process-exec` can be
     limited to `git`, `git-receive-pack` and `git-upload-pack`. The
     spike allowed all of `/opt/homebrew` or the whole Xcode developer
     directory, which is wider than git needs.
   - `wb-hostd` resolves the `/usr/bin/git` shim to the real binary
     before it confines itself.
   - git runs without the user's or the system's git configuration, in a
     working directory inside `<data>`, with the repository directories
     created before it runs.
5. **Profiles take paths as parameters.** Generated rules, such as the
   ones for each parent directory of `<data>` (below), name parameters
   (`(param "PARENT1")`), and the paths go in as parameter values. The
   spike pasted the paths into the profile text instead. The security
   review on PR65 showed that a crafted path then turns the profile
   off, so the daemon runs unconfined.
6. **What `wb-hostd` does before it confines itself.** It reads its own
   configuration, sets `GOMAXPROCS`, loads the local time zone, starts
   the launcher, resolves the git binary, and works out the paths and
   parameters for its profile. None of that reads anything the guest
   sent.
7. **Each daemon fails closed.** A profile that doesn't compile, or a
   missing parameter, makes the call fail and the daemon exit.

## Measurements

Host: macOS 27.0.1 (26A434), Apple Silicon, Go 1.27.1. The spike code
and its recorded output are on the spike branch:
[spikes/x23-sandboxed-daemons at 6602ecb](https://github.com/wraithbox/wraithbox/tree/6602ecbe63dc1acf10fe3415d7c74b8ad7c8721c/spikes/x23-sandboxed-daemons),
and the `GOMAXPROCS` check after review at
[00c2119](https://github.com/wraithbox/wraithbox/tree/00c2119b3dbccef600474f715855c55809643aef/spikes/x23-sandboxed-daemons)
(`results/` holds the output of each run).

### What a confined Go program can still do

A probe program applied each profile to itself and then tried each
operation. Under `deny-all.sb` (`(deny default)` and nothing else),
applied in process:

| Works | Denied |
|---|---|
| goroutines, garbage collection, returning memory to the OS, async preemption, timers | opening any file, `/etc/passwd`, the home directory, `/tmp` |
| `crypto/rand`, ECDSA signing | `stat` of any path, `os.Getwd`, `os.Hostname` |
| inherited descriptors: stderr, sockets | TCP and UDP connect, also to loopback |
| `socketpair` and passing descriptors with `SCM_RIGHTS` | connecting to a Unix socket by path, `listen` |
| | DNS lookups, running a program, signals to other processes |
| | Mach service lookups the system libraries try: `com.apple.logd`, `com.apple.system.notification_center`, `com.apple.system.opendirectoryd.libinfo` |
| | `sysctl` reads such as `kern.hostname` and `kern.ipc.somaxconn` |

The profile applies to every thread of the process, including the ones
the Go runtime started before the call. A file open from 32 goroutines,
each locked to its own OS thread, was denied on all 32.

`time.Local` becomes UTC, because the time zone file can't be read. A
daemon that needs local time loads it before it confines itself.

### wb-netd: gVisor over inherited descriptors

A stand-in for `wb-netd` confined itself, then ran a gVisor stack on an
inherited datagram socket with one Ethernet frame per datagram (as the
file-handle network attachment uses). For each TCP connection from a
guest stack on the other end, it made a socketpair, passed one end to a
proxy over an inherited Unix socket with `SCM_RIGHTS`, and copied the
stream to the other end. The proxy echoed. Each run sent 64 MiB each
way on each of 4 streams:

| Mechanism | Profile | Echo throughput |
|---|---|---|
| not confined | none | 420 MiB/s, median of 5 |
| in process, no cgo | `netd.sb` (`hw.ncpu` allowed) | 438 MiB/s, median of 5 |
| in process, cgo | `netd.sb` | 418 MiB/s, median of 5 |
| `sandbox-exec` | `netd-exec.sb` | 439 MiB/s, median of 5 |
| in process, no cgo | `(allow default)` | 404 MiB/s, median of 5 |
| in process, no cgo | `deny-all.sb` | 258 MiB/s, median of 3 |
| in process, no cgo, explicit `GOMAXPROCS` | `deny-all.sb` | 437 MiB/s, median of 3 (runs: 437, 314, 458) |
| in process, no cgo, same session | `netd.sb` | 472 MiB/s, median of 3 |

All streams arrived complete. The differences between the rows other
than plain `deny-all.sb` are within the noise of a shared machine. The
call took 5.36 to 9.26 ms in the recorded runs.

The recommended `wb-netd` profile is the bare one, with `GOMAXPROCS`
set before the call:

```scheme
(version 1)
(deny default)
```

Under it, the stand-in couldn't open, write or `stat` a file, connect
over TCP, UDP or a Unix socket path (also to a listener in the same
test), listen, resolve a name, or run a program. Its log goes to an
inherited descriptor: a pipe or socket to `wb-hostd`, never a
descriptor on a file in `<logs>` or `<data>`. A profile is checked when a
file is opened, not on each write, so an inherited file descriptor
would keep working under any profile.

### In process or sandbox-exec

| | In process | `sandbox-exec` |
|---|---|---|
| Profile | the rules the daemon needs | those, plus loading the binary: running it, reading it, `/usr/lib`, the dyld cache, and the `sysctl` values the Go runtime reads at start |
| cgo | not needed | not needed |
| Who chooses the profile | the daemon, from a profile compiled into it | its parent, on the command line |
| Apple's status | `sandbox.h` marks `sandbox_init` "No longer supported" since 10.8, and doesn't declare `sandbox_init_with_parameters` | the man page says "DEPRECATED" |

`sandbox-exec` with `(deny default)` alone can't start the program at
all (`execvp() ... Operation not permitted`). The extra rules let any
code in the process read `/usr/lib` and the system libraries until it
exits. In process, the profile only has to allow what the daemon
does after start.

The variant with parameters takes paths such as `<data>` as values that
the profile reads with `(param "DATA")`, so paths stay out of the
profile text (condition 5).

Both interfaces are deprecated and both still work. Apple points
developers at App Sandbox, which is set by entitlements at signing
time. Its entitlements switch capabilities on or off (network client,
for example) and give the program a container directory. This spike
didn't try it.

### wb-hostd: state, IPC and git

A stand-in for `wb-hostd` confined itself to its directories
(parameters `CONFIG`, `DATA`, `LOGS`), then:

| Check | Result |
|---|---|
| read `<config>/config.toml` | allowed |
| write in `<config>` | denied |
| create, lock (`flock`) and write `<data>/state.db` | allowed |
| append to `<logs>/audit.jsonl` | allowed |
| listen on a Unix socket in `<data>/run`, serve a client outside the sandbox | allowed |
| read or write outside its directories, TCP or UDP out, DNS, listen on TCP | denied |
| git round trip with Homebrew git: `init --bare`, commit, push (`receive-pack`), clone (`upload-pack`) | works |
| the same with the Xcode git binary, found with `xcrun --find git` before confining | works |
| the same through the `/usr/bin/git` shim | fails: the shim runs `xcode-select`, which reads `/var/select/developer_dir` |
| start a `wb-netd` child that confines itself | fails: `forbidden-sandbox-reinit` |
| the same through a launcher started before confining | works, and the child's profile holds |
| ask the launcher for a program not in its table (`sh`) | refused |

git needed these from the profile and from `wb-hostd`:

- Running and reading the git installation, and `/dev/null`.
- No user or system git configuration: `GIT_CONFIG_NOSYSTEM=1`,
  `GIT_CONFIG_GLOBAL=/dev/null` and `HOME` set to a state directory.
  git treats a denied `~/.config/git/config` as an error and stops. The
  git gateway must not use the user's git configuration in any case.
- Metadata reads (`stat`) of each parent directory of `<data>`, because
  git resolves real paths from the root. A profile has no rule for "the
  parents of a path", so the profile needs one rule for each parent,
  with the path as a parameter (condition 5).
- A working directory inside `<data>`, and the repository directories
  created by `wb-hostd` before git runs.

The local push and clone in the test start `git-receive-pack` and
`git-upload-pack` through `/bin/sh`, which the test profile allowed.
The git gateway runs them directly (S08-workspace-and-git), so the real
profile doesn't need a shell.

The profile that ran, without the generated parent rules and without the
`NETD` rules that let the spike start `wb-netd` directly:

```scheme
(version 1)
(deny default)
(allow sysctl-read (sysctl-name "hw.ncpu"))
(allow file-read* (subpath (param "CONFIG")))
(allow file-read* file-write* (subpath (param "DATA")) (subpath (param "LOGS")))
(allow network-bind network-inbound
  (local unix-socket (subpath (string-append (param "DATA") "/run"))))
(allow process-fork)
(allow process-exec (literal (param "GIT")))
(allow process-exec file-read* (subpath (param "GITROOT")))
(allow file-read* file-write-data (literal "/dev/null"))
; git starts under this profile, so dyld must load it.
(allow file-read* (literal (param "GIT")))
(allow file-read* (literal "/") (subpath "/usr/lib")
  (subpath "/System/Library/dyld") (subpath "/System/Cryptexes/OS"))
(allow file-read-metadata (subpath "/System/Volumes/Preboot/Cryptexes"))
(allow sysctl-read (sysctl-name "hw.pagesize" "hw.pagesize_compat"
  "hw.logicalcpu_max" "hw.physicalcpu_max" "kern.osrelease" "kern.osversion"))
```

`GITROOT` was `/opt/homebrew` or `/Applications/Xcode.app/Contents/Developer`.
Condition 4 narrows it to the git installation. That narrower rule set
was not run.

SQLite itself was not run. `state.db` stands for it with a file lock
and a write.

### wb-proxyd: the network part

This is the part of `wb-proxyd` that doesn't use the Keychain. The full
answer waits on I61.

- Outbound TCP and DNS work under a short profile: `network-outbound`
  to TCP, UDP to port 53, reading `/private/etc/resolv.conf` and
  `/private/etc/hosts`, and connecting to
  `/private/var/run/mDNSResponder`, which Go's resolver uses through
  libSystem on macOS (`profiles/proxyd-net.sb`).
- Checking a server certificate against the system roots fails under
  that profile (`SecPolicyCreateSSL error: 0`). It works once the
  profile also allows the `trustd`, `SecurityServer`, `cfprefsd` and
  `ocspd` services, reading the Security framework and the system
  keychains, and every `stat` and `sysctl` read
  (`profiles/proxyd-net-tls.sb`). That profile is too wide, and I61
  must not start from it as it is.

## What it means for the specs

- S04-architecture, "Processes" and "Each host daemon is
  self-sandboxed": the launcher under option A with its conditions, the
  self-check, `wb-hostd` as the exception, and the `wb-netd` log
  descriptor.
- S12-platforms, "Self-sandbox" row for macOS: a Seatbelt profile
  applied in process with `sandbox_init_with_parameters`, without cgo,
  and the launcher.
- A proposal for I48's supervision section in S04-architecture: the
  launcher is the parent of `wb-vmd`, `wb-netd` and `wb-proxyd`. It sees
  their exits and must pass them on to `wb-hostd`. I48 must also decide
  what happens to the launcher and its children when `wb-hostd` restarts,
  so no orphaned daemons keep running.
- I61 starts from `proxyd-net.sb`.
- `wb-vmd` confines itself too (S04-architecture). With the launcher it
  can, but its profile for the Virtualization framework was not part of
  this spike (I64).

## Not covered

- The `wb-proxyd` Keychain and Secure Enclave rules (I61).
- A profile for `wb-vmd` (I64).
- **Reading the user's repository.** S08-workspace-and-git has
  `wb-hostd` run `git upload-pack` against the user's repository, or
  fetch from it into an export repository (I41). The `wb-hostd` profile
  can't read it, and a profile can't be widened after start. I67 has
  the options: `wb` runs `upload-pack` and tunnels it, or a spec change.
- How `wb-proxyd` ties a stream from `wb-netd` to a VM and project
  (I68).
- The launcher as specified in condition 3, and the `wb-git` shim. The
  spike's launcher was looser.
- App Sandbox through entitlements, as an alternative mechanism.
- Running the amd64 build. It compiles, and this host has no Rosetta.

**Status:** Answered: yes with conditions (`wb-proxyd` Keychain part in I61)
