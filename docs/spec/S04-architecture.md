# S04 - Architecture

**Purpose:** Name the processes, the boundaries between them, and which
requirement each boundary serves.

## Overview

![The processes on the host and in the guest. Two channels cross the VM boundary: the host-guest socket, which wb-hostd terminates, and Ethernet frames, which wb-netd terminates.](S04-architecture.svg)

Exactly two channels cross the VM boundary: the **host-guest socket**
(vsock on macOS and Linux, Hyper-V sockets on Windows; terminated by
`wb-hostd`) and **Ethernet frames** (terminated by `wb-netd`). There is
no shared directory, no NAT to the host network, and no other device
that carries data (SEC01-separate-kernel, SEC02-no-host-fs-share, SEC05-default-deny).

## Processes

| Process | Lang | Runs as | Holds | Faces the guest via | Purpose |
|---|---|---|---|---|---|
| `wb` | Go | user, per invocation | nothing | none | The only CLI: dispatcher for every command, TTY relay for agent sessions (S05-cli) |
| `wb-hostd` | Go | per-user service | policy, landing repos, state | host-guest socket | Sessions, git gateway, approvals, audit writer, admission control (S06-vm-lifecycle, S08-workspace-and-git, S09-policy-credentials-audit) |
| `wb-launcher` | Go, standard library only | user, started by `wb-hostd` before it confines itself (macOS only) | nothing | none | Start the programs in its fixed table when `wb-hostd` asks, so that each can confine itself ("Each host daemon is self-sandboxed") |
| `wb-git` | Go | user, per git gateway transfer, started through `wb-launcher` (recommended, not yet built; X23-sandboxed-daemons open decision 2) | nothing | pack data from the guest, through `wb-hostd` | Confine itself to one repository, then run `git receive-pack` or `git upload-pack` (S08-workspace-and-git) |
| `wb-vmd` | platform-native | user, spawned for `wb-hostd` (through `wb-launcher` on macOS) | VM handles | none (devices only) | Create, start, stop, save, and restore VMs; hand guest socket connections and the NIC endpoint to other processes (S12-platforms) |
| `wb-netd` | Go | user, one per VM, spawned for `wb-hostd` (through `wb-launcher` on macOS) | nothing | Ethernet frames | Network stack, DHCP, DNS, stream hand-off (S07-egress-gateway) |
| `wb-proxyd` | Go | user, spawned for `wb-hostd` (through `wb-launcher` on macOS) | credentials, CA signing handle | streams from `wb-netd` | HTTP policy, credential replacement, dependency gate, upstream connections (S07-egress-gateway, S09-policy-credentials-audit) |
| `wb-guestd` | Go | root / SYSTEM inside the guest | nothing | n/a (runs in the guest) | Users, PTY exec, git transport, port discovery (S06-vm-lifecycle) |

Native user-interface helpers (notifications with actions, later a tray
or menu-bar item) are separate small processes per platform. They render
approval requests and return the user's answer to `wb-hostd`, and decide
nothing themselves (S12-platforms).

## Design decisions

- **One CLI, dispatcher style.** `wb` is the entry point for every
  command. Agent commands (`wb claude …`) pass every following argument
  to the agent untouched, so the agent's own flags and subcommands can
  never collide with Wraith Box's (S05-cli).
- **Cross-platform core, native edges.** Session management, policy,
  approvals, audit, the git gateway, the network stack, the proxy, the
  guest agent, and the CLI are Go and the same on every host OS. Only the
  pieces that must call a platform API with no good Go binding are
  native, and each is a separate process behind a gRPC contract with no
  policy decisions of its own: `wb-vmd` on macOS (Virtualization
  framework) and the user-interface helpers (S12-platforms, NFR07-maintainability).
- **Secrets live in one process, and not the most exposed one.**
  `wb-netd` parses raw guest packets, the largest attack surface, and
  holds no secrets and opens no outbound connections. `wb-proxyd` holds
  credentials but only sees TCP byte streams already reassembled by
  `wb-netd` and validated against the DNS mapping. `wb-hostd` and
  `wb-vmd` never touch a credential (SEC04-no-guest-secrets, SEC12-least-privilege).
- **The VM provider passes descriptors, not bytes.** `wb-vmd` accepts
  guest socket connections and creates the NIC endpoint, then hands the
  descriptors (handles on Windows) to `wb-hostd` and `wb-netd`. It does
  not relay or parse guest traffic, so the native code has the smallest
  possible exposure to guest input.
- **Each host daemon is self-sandboxed.** Host processes confine
  themselves at startup with the platform's mechanism (S12-platforms) so that
  each gets only what it needs: `wb-netd` no filesystem and no network
  beyond inherited descriptors; `wb-proxyd` outbound network and its
  credential store items; `wb-hostd` its state directories; `wb-vmd` the
  hypervisor and the VM bundles. This hardens our own code; it is not how
  the agent is contained (the VM is). How it is done (X23-sandboxed-daemons,
  SEC12-least-privilege):
  - A daemon confines itself first thing in `main`, from a profile
    compiled into it, and exits if that fails. Nothing before that reads
    an inherited descriptor, the arguments or the environment. Right
    after, it tries one operation its profile denies, and exits if that
    succeeds.
  - `wb-hostd` is the exception. Before it confines itself, it reads its
    own configuration, sets `GOMAXPROCS`, loads the local time zone,
    starts `wb-launcher` (on macOS), resolves the git binary, and works
    out the paths for its profile. None of that reads
    anything the guest sent.
  - `wb-netd` writes its log to an inherited pipe or socket to
    `wb-hostd`, which frames, attributes and rate-limits each line.
    `wb-netd` holds no descriptor on any file in `<logs>` or `<data>`.
  - On macOS a confined process can't confine itself again, and its
    children inherit its profile. So `wb-hostd` starts `wb-vmd`,
    `wb-netd` and `wb-proxyd` through `wb-launcher`, which has no
    profile. On Linux, Landlock and seccomp restrictions stack, so a
    child can confine itself further and needs no launcher (assumed from
    their documentation, not tested; X10-linux-hypervisor work confirms it).
    `wb-launcher` doesn't widen what a compromised `wb-hostd` can do
    only while all of these hold:
    - It is a separate, small program that uses only the Go standard
      library, not `wb-hostd` started in another mode.
    - Its request channel is a `SOCK_SEQPACKET` or `SOCK_DGRAM`
      socketpair inherited at start, never a path. Each message is one
      fixed-format request. A request with unexpected descriptors has
      them closed and is refused.
    - It starts each program with no arguments and a fixed environment.
      It passes only the descriptors `wb-hostd` hands it. The one
      bounded exception is `wb-git`, below.
    - Each program's profile and its parameters are compiled into it or
      come from configuration `wb-hostd` read before it confined
      itself, never over the request channel.
    - Its program table holds absolute paths in the installed bundle,
      never under `<config>`, `<data>` or `<logs>`, and it checks that
      when it starts.
    - Each program has an instance cap (SEC13-bounded-resources).
  - git, which the git gateway runs on data the guest sends, is
    recommended to start through `wb-launcher` as `wb-git`, a shim that
    applies a profile for one repository and then runs git (not yet
    built, X23-sandboxed-daemons open decision 2). The launcher table
    has two fixed entries, `wb-git-receive` (`git receive-pack`) and
    `wb-git-upload` (`git upload-pack`), so the subcommand is never a
    free string. A `wb-git` request carries exactly one extra field,
    the repository. `wb-launcher` accepts it only if it resolves to
    `<data>/projects/<id>/landing.git`, or the export repository of
    I41, under a projects root fixed from configuration before
    `wb-hostd` confined itself, with `<id>` matching the project ID
    format, and with no `..` component or symbolic link on the way. It
    passes the repository to the shim as the profile parameter and as
    git's one path argument. This is the one bounded exception to "no
    arguments". The alternative shape, an inherited directory
    descriptor that the launcher checks with `F_GETPATH`, keeps the path
    out of the request. It is the fallback if the path check turns out
    to be fragile.
  - The measured, weaker fallback is git as a child of `wb-hostd`,
    under the `wb-hostd` profile. A git bug would then reach every
    project's policy, the approvals in `state.db`, other projects'
    landing repositories, and the audit log. If the maintainer chooses
    the fallback, it is recorded in T00-index as an accepted residual
    risk.
  - Open: `wb-hostd` can't read the user's repository under its profile,
    which S08-workspace-and-git's `upload-pack` and export repository
    need (I67).
- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Guest confinement narrows, never widens.** Controls inside the
  guest (Seatbelt profiles, a Network Extension that labels flows with
  the program that opened them) can only deny what the host would allow.
  Every `SEC*` control holds without them (S13-guest-confinement, T06-forged-labels).
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted; `wb-proxyd` re-checks
  the hostname, SNI, and HTTP request. A bug in one layer does not open
  egress on its own.
- **One work VM per guest OS, many projects; an isolated VM for the
  rest.** The work VM hosts every trusted project for its guest OS,
  separated by guest user accounts; an isolated VM takes untrusted
  repositories. For macOS guests this fits Apple's two-VM limit (SEC08-proj-isolation, NFR05-two-macos-vms).
- **No root or administrator rights at run time.** The packet transport
  and the userspace stack replace host networking features that would
  need them (SEC12-least-privilege, NFR04-host-platforms).

## Host state

Locations follow each OS's conventions (paths in S12-platforms); the logical
layout is the same everywhere:

```
<config>/                             user-editable configuration (S09-policy-credentials-audit)
  config.toml                         global settings and defaults
  policy.yaml                         global network policy (S09-policy-credentials-audit)
  projects/<project-id>.toml          per-project settings, toolchain manifest reference
  projects/<project-id>.policy.yaml   per-project network policy
<data>/
  images/                             base images (copy-on-write clones)
  vms/<guest-os>-{work,isolated}/     VM bundles: disks, machine identity, saved state
  projects/<project-id>/landing.git   bare repo receiving session branches (S08-workspace-and-git)
  state.db                            SQLite: projects, sessions, approvals, caches
  run/                                user-only directory for local IPC endpoints
<logs>/                               audit JSONL and daemon logs
```

## Inter-process contracts

All IPC is protobuf over gRPC. On the host it runs over a local IPC
endpoint only the user can open: Unix sockets in a `0700` directory on
macOS and Linux, named pipes with an ACL for the user's SID on Windows.
The peer's identity is checked on every connection. To the guest it runs
over the host-guest socket. The `.proto` files are the single contract
for every language (S10-tech-stack).

**Status:** Draft
