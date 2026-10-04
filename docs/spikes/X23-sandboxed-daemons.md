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
  daemons so that each can confine itself.
- **You are approving:** each daemon applies its own Seatbelt profile
  in process, first thing in `main`, with no cgo. `wb-hostd` starts
  `wb-vmd`, `wb-netd` and `wb-proxyd` through a small launcher that it
  starts before it confines itself, because a confined process can't
  confine its children any further. S04-architecture and S12-platforms
  say so.
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host): kept, and now backed by measured profiles. No other `SEC*`
  control changes.
- **Assumed:** that Apple keeps `sandbox_init_with_parameters` working.
  The SDK header marks `sandbox_init` deprecated since macOS 10.8 and
  doesn't declare the variant with parameters at all. Both still work
  on macOS 27.0.1. Only arm64 was run. The amd64 build compiles and was
  not run.
- **Open decisions:** none. Who supervises the launched daemons stays
  with the S04-architecture supervision change of I48.
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
| `wb-netd` | Yes | deny everything, allow reading the `hw.ncpu` system value |
| `wb-hostd` | Yes, with conditions | its state directories, its Unix socket, and running git |
| `wb-proxyd` | Network part yes, Keychain and Secure Enclave part open (I61) | outbound TCP, DNS, certificate checks |

The conditions:

1. **In process, without cgo.** The daemon calls
   `sandbox_init_with_parameters` from `libsystem_sandbox` as its first
   act in `main`. A Go assembly trampoline makes the call, the way
   `golang.org/x/sys/unix` calls the C library. The binary builds with
   `CGO_ENABLED=0` and cross-compiles from any OS. The cgo version
   behaves the same. Starting the daemon under `sandbox-exec` also
   works but needs a larger profile (below).
2. **Allow `sysctl-read` of `hw.ncpu`.** Go 1.25 and later re-read the
   CPU count every second to adjust `GOMAXPROCS`. When the profile
   denies the read, the runtime sees one CPU, sets `GOMAXPROCS` to 1, and
   `wb-netd` throughput drops by about 40 percent.
3. **`wb-hostd` starts its children through a launcher.** A confined
   process can't apply a second profile: the kernel refuses it
   (`forbidden-sandbox-reinit`), and children inherit their parent's
   profile. So `wb-hostd` starts a launcher before it confines itself.
   The launcher has no profile, takes requests only from `wb-hostd`
   over a private socket, starts only the programs in its table, and
   each program confines itself.
4. **git runs as a confined child of `wb-hostd`.** It inherits the
   `wb-hostd` profile, which must allow running the git binary and
   reading its installation. Before it confines itself, `wb-hostd`
   resolves the `/usr/bin/git` shim to the real binary. It runs git
   without the user's or the system's git configuration, and creates
   the repository directories before git runs.
5. **Each daemon is fail-closed.** A profile that doesn't compile, or a
   missing parameter, makes the call fail and the daemon exit.

## Measurements

Host: macOS 27.0.1 (26A434), Apple Silicon, Go 1.27.1. The spike code
and its recorded output are on the spike branch:
[spikes/x23-sandboxed-daemons at 6602ecb](https://github.com/wraithbox/wraithbox/tree/6602ecbe63dc1acf10fe3415d7c74b8ad7c8721c/spikes/x23-sandboxed-daemons)
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

The profile applies to every thread of the process, including the ones
the Go runtime started before the call. A file open from 32 goroutines,
each locked to its own OS thread, was denied on all 32.

`time.Local` becomes UTC, because the time zone file can't be read. A
daemon that needs local time loads it before it confines itself. The
`hw.ncpu` read of condition 2 is the other change the program sees.

### wb-netd: gVisor over inherited descriptors

A stand-in for `wb-netd` confined itself, then ran a gVisor stack on an
inherited datagram socket with one Ethernet frame per datagram (as the
file-handle network attachment uses). For each TCP connection from a
guest stack on the other end, it made a socketpair, passed one end to a
proxy over an inherited Unix socket with `SCM_RIGHTS`, and copied the
stream to the other end. The proxy echoed. Each run sent 64 MiB each
way on each of 4 streams:

| Mechanism | Profile | Echo throughput, median of 5 |
|---|---|---|
| not confined | none | 420 MiB/s |
| in process, no cgo | `netd.sb` | 438 MiB/s |
| in process, cgo | `netd.sb` | 418 MiB/s |
| `sandbox-exec` | `netd-exec.sb` | 439 MiB/s |
| in process, no cgo | `(allow default)` | 404 MiB/s |
| in process, no cgo | `deny-all.sb`, no `hw.ncpu` | 258 MiB/s (median of 3) |

All streams arrived complete. The differences between the first five
rows are noise on a shared machine. The call takes 6 to 11 ms.

The `wb-netd` profile:

```scheme
(version 1)
(deny default)
(allow sysctl-read (sysctl-name "hw.ncpu"))
```

Under it, the stand-in couldn't open, write or `stat` a file, connect
over TCP, UDP or a Unix socket path (also to a listener in the same
test), listen, resolve a name, or run a program. `wb-netd` writes its
log to a descriptor it inherits.

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
the profile reads with `(param "DATA")`. No path is pasted into the
profile text, so a path with a quote in it can't change a rule.

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

git needed four things from the profile and from `wb-hostd`:

- Running and reading the git installation (`/opt/homebrew` or
  `/Applications/Xcode.app/Contents/Developer`), and `/dev/null`.
- No user or system git configuration: `GIT_CONFIG_NOSYSTEM=1`,
  `GIT_CONFIG_GLOBAL=/dev/null` and `HOME` set to a state directory.
  git treats a denied `~/.config/git/config` as an error and stops. The
  git gateway must not use the user's git configuration in any case.
- Metadata reads (`stat`) of each parent directory of `<data>`, because
  git resolves real paths from the root. Since a profile has no rule
  for "the parents of a path", `wb-hostd` adds one literal rule for each
  parent when it builds its profile.
- A working directory inside `<data>`, and the repository directories
  created by `wb-hostd` before git runs.

The local push and clone in the test start `git-receive-pack` and
`git-upload-pack` through `/bin/sh`, which the test profile allowed.
The git gateway runs them directly (S08-workspace-and-git), so the real
profile doesn't need a shell.

The profile, without the generated parent rules:

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

SQLite itself was not run. `state.db` stands for it with a file lock
and a write.

### wb-proxyd: the network part

This is the part of `wb-proxyd` that doesn't use the Keychain. The full
answer waits on I61.

- Outbound TCP and DNS work under a short profile: `network-outbound`
  to TCP, UDP to port 53, reading `/private/etc/resolv.conf` and
  `/private/etc/hosts`, and connecting to
  `/private/var/run/mDNSResponder`, which Go's resolver uses through
  libSystem on macOS.
- Checking a server certificate against the system roots fails under
  that profile (`SecPolicyCreateSSL error: 0`). It works once the
  profile also allows the `trustd`, `SecurityServer`, `cfprefsd` and
  `ocspd` services, reading the Security framework and the system
  keychains, and every `stat` and `sysctl` read. That profile is too
  wide, and I61 narrows it together with the Keychain rules
  (`profiles/proxyd-net-tls.sb` on the spike branch).

## What it means for the specs

- S04-architecture, "Each host daemon is self-sandboxed": names the
  mechanism, the launcher, and that `wb-netd` logs through an inherited
  descriptor.
- S12-platforms, "Self-sandbox" row for macOS: a Seatbelt profile
  applied in process with `sandbox_init_with_parameters`, without cgo.
- I48's supervision section in S04-architecture: the launcher is the
  parent of `wb-vmd`, `wb-netd` and `wb-proxyd`, so it sees their exits
  and passes them on to `wb-hostd`.
- I61 starts from `proxyd-net.sb` and `proxyd-net-tls.sb`.
- `wb-vmd` confines itself too (S04-architecture). With the launcher it
  can, but its profile for the Virtualization framework was not part of
  this spike (I64).

## Not covered

- The `wb-proxyd` Keychain and Secure Enclave rules (I61).
- A profile for `wb-vmd` (I64).
- App Sandbox through entitlements, as an alternative mechanism.
- Running the amd64 build. It compiles, and this host has no Rosetta.

**Status:** Answered: yes with conditions (`wb-proxyd` Keychain part in I61)
