# 004 - Architecture

**Purpose:** Name the processes, the boundaries between them, and which
requirement each boundary serves.

## Overview

```
 Terminal: wb [wb flags] claude [claude args]
   │
   ▼
 wb (Go, CLI) ──── gRPC over local IPC ────▶ wb-hostd (Go, per-user service)
                                              │ sessions · git gateway · policy store
                                              │ approvals · audit writer · admission control
                                              │
                    ┌── gRPC over local IPC ──┤
                    ▼                         │
 wb-vmd (platform-native VM provider)         │
   macOS: Swift, Virtualization framework     │
   Windows / Linux: Go (spec 012)             │
   runs VMs · hands out guest socket and      │
   NIC descriptors · no policy, no secrets    │
                    │                         │
                    │ NIC endpoint ──▶ wb-netd (Go)
                    │                    userspace TCP/IP (gVisor) · DHCP · DNS
                    │                    no secrets · no outbound network
                    │                      │ accepted streams
                    │                      ▼
                    │                  wb-proxyd (Go)
                    │                    TLS/HTTP policy · credential replacement
                    │                    dependency gate · only upstream connector
                    │                      │
                    │                      ▼ allowlisted upstreams only → Internet
                    ▼
 ┌──────── Work VM (guest OS per spec 012; v1: macOS) ─────────────┐
 │ virtual NIC ⇄ packet transport (frames to wb-netd only)         │
 │ host-guest socket (vsock / hvsock) ⇄ wb-hostd                   │
 │ wb-guestd (root / SYSTEM service)                               │
 │   └─ project user A: clone + worktrees · claude · tools         │
 │   └─ project user B: …                                          │
 │ no secrets · no host mounts                                     │
 └─────────────────────────────────────────────────────────────────┘
 ┌──────── Isolated VM (optional, same wiring, own policy) ────────┐
 │ untrusted repositories, or work needing a GUI login session     │
 └─────────────────────────────────────────────────────────────────┘
```

Exactly two channels cross the VM boundary: the **host-guest socket**
(vsock on macOS and Linux, Hyper-V sockets on Windows; terminated by
`wb-hostd`) and **Ethernet frames** (terminated by `wb-netd`). There is
no shared directory, no NAT to the host network, and no other device
that carries data (S1, S2, S5).

## Processes

| Process | Lang | Runs as | Holds | Faces the guest via | Purpose |
|---|---|---|---|---|---|
| `wb` | Go | user, per invocation | nothing | — | The only CLI: dispatcher for every command, TTY relay for agent sessions (spec 005) |
| `wb-hostd` | Go | per-user service | policy, landing repos, state | host-guest socket | Sessions, git gateway, approvals, audit writer, admission control (specs 006, 008, 009) |
| `wb-vmd` | platform-native | user, spawned by `wb-hostd` | VM handles | — (devices only) | Create, start, stop, save and restore VMs; hand guest socket connections and the NIC endpoint to other processes (spec 012) |
| `wb-netd` | Go | user, one per VM, spawned by `wb-hostd` | nothing | Ethernet frames | Network stack, DHCP, DNS, stream hand-off (spec 007) |
| `wb-proxyd` | Go | user, spawned by `wb-hostd` | credentials, CA signing handle | streams from `wb-netd` | HTTP policy, credential replacement, dependency gate, upstream connections (specs 007, 009) |
| `wb-guestd` | Go | root / SYSTEM inside the guest | nothing | — (it *is* guest) | Users, PTY exec, git transport, port discovery (spec 006) |

Native user-interface helpers (notifications with actions, later a tray
or menu-bar item) are separate small processes per platform. They render
approval requests and return the user's answer to `wb-hostd`, and decide
nothing themselves (spec 012).

## Design decisions

- **One CLI, dispatcher style.** `wb` is the entry point for every
  command. Agent commands (`wb claude …`) pass every following argument
  to the agent untouched, so the agent's own flags and subcommands can
  never collide with Wraith Box's (spec 005).
- **Cross-platform core, native edges.** Session management, policy,
  approvals, audit, the git gateway, the network stack, the proxy, the
  guest agent and the CLI are Go and the same on every host OS. Only the
  pieces that must call a platform API with no good Go binding are
  native, and each is a separate process behind a gRPC contract with no
  policy decisions of its own: `wb-vmd` on macOS (Virtualization
  framework) and the user-interface helpers (spec 012, N7).
- **Secrets live in one process, and not the most exposed one.**
  `wb-netd` parses raw guest packets — the largest attack surface — and
  holds no secrets and opens no outbound connections. `wb-proxyd` holds
  credentials but only sees TCP byte streams already reassembled by
  `wb-netd` and validated against the DNS mapping. `wb-hostd` and
  `wb-vmd` never touch a credential (S4, S12).
- **The VM provider passes descriptors, not bytes.** `wb-vmd` accepts
  guest socket connections and creates the NIC endpoint, then hands the
  descriptors (handles on Windows) to `wb-hostd` and `wb-netd`. It does
  not relay or parse guest traffic, so the native code has the smallest
  possible exposure to guest input.
- **Each host daemon is self-sandboxed.** Host processes confine
  themselves at startup with the platform's mechanism (spec 012) so that
  each gets only what it needs: `wb-netd` no filesystem and no network
  beyond inherited descriptors; `wb-proxyd` outbound network and its
  credential store items; `wb-hostd` its state directories; `wb-vmd` the
  hypervisor and the VM bundles. This hardens our own code; it is not how
  the agent is contained (the VM is).
- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted; `wb-proxyd` re-checks
  the hostname, SNI and HTTP request. A bug in one layer does not open
  egress on its own.
- **One work VM per guest OS, many projects; an isolated VM for the
  rest.** The work VM hosts every trusted project for its guest OS,
  separated by guest user accounts; an isolated VM takes untrusted
  repositories. For macOS guests this fits Apple's two-VM limit (S8, N5).
- **No root or administrator rights at run time.** The packet transport
  and the userspace stack replace host networking features that would
  need them (S12, N4).

## Host state

Locations follow each OS's conventions (paths in spec 012); the logical
layout is the same everywhere:

```
<config>/                             user-editable configuration (spec 009)
  config.toml                         global policy and defaults
  projects/<project-id>.toml          per-project policy, toolchain manifest reference
<data>/
  images/                             base images (copy-on-write clones)
  vms/<guest-os>-{work,isolated}/     VM bundles: disks, machine identity, saved state
  projects/<project-id>/landing.git   bare repo receiving session branches (spec 008)
  state.db                            SQLite: projects, sessions, approvals, caches
  run/                                user-only directory for local IPC endpoints
<logs>/                               audit JSONL and daemon logs
```

## Inter-process contracts

All IPC is protobuf over gRPC. On the host it runs over a local IPC
endpoint only the user can open: Unix sockets in a `0700` directory on
macOS and Linux, named pipes with an ACL for the user's SID on Windows;
the peer's identity is checked on every connection. To the guest it runs
over the host-guest socket. The `.proto` files are the single contract
for every language (spec 010).

**Status:** Draft
