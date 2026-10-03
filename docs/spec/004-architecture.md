# 004 - Architecture

**Purpose:** Name the processes, the boundaries between them, and which
requirement each boundary serves.

## Overview

```
 Terminal: wb [claude args]
   │
   ▼
 wb (Go, CLI) ───── gRPC over Unix socket ─────▶ wb-hostd (Swift, LaunchAgent)
                                                  │ VM lifecycle · sessions · git gateway
                                                  │ policy store · approvals · audit writer
                     ┌─── NIC socketpair fd ──────┤
                     ▼                            │ vsock (PTY exec, git, control)
 wb-netd (Go)                                     │
   userspace TCP/IP (gVisor) · DHCP · DNS         │
   no secrets · no outbound network               │
     │ accepted streams (Unix socket)             │
     ▼                                            │
 wb-proxyd (Go)                                   │
   TLS/HTTP policy · credential replacement       │
   dependency gate · only upstream connector      │
     │                                            │
     ▼ allowlisted upstreams only                 │
   Internet                                       │
                                                  ▼
 ┌──────── Work VM (macOS guest, Virtualization framework) ─────────┐
 │ virtio-net ⇄ file-handle attachment (frames to wb-netd only)     │
 │ virtio-vsock ⇄ wb-hostd                                          │
 │ wb-guestd (root LaunchDaemon)                                    │
 │   └─ project user A: clone + worktrees · claude · tools          │
 │   └─ project user B: …                                           │
 │ no secrets · no host mounts                                      │
 └──────────────────────────────────────────────────────────────────┘
 ┌──────── Isolated VM (optional, same wiring, own policy) ─────────┐
 │ untrusted repositories, or work needing a GUI login session      │
 └──────────────────────────────────────────────────────────────────┘
```

Exactly two channels cross the VM boundary: **vsock** (terminated by
`wb-hostd`) and **Ethernet frames** (terminated by `wb-netd`). There is no
shared directory, no NAT to the host network, and no other device that
carries data (S1, S2, S5).

## Processes

| Process | Lang | Runs as | Holds | Faces the guest via | Purpose |
|---|---|---|---|---|---|
| `wb` | Go | user, per invocation | nothing | — | Drop-in CLI; relays the TTY (spec 005) |
| `wbctl` | Go | user, per invocation | nothing | — | Management CLI (spec 005) |
| `wb-hostd` | Swift | user LaunchAgent | VM handles, policy, landing repos | vsock | VMs, sessions, git gateway, approvals, audit writer (specs 006, 008, 009) |
| `wb-netd` | Go | user, one per VM, spawned by `wb-hostd` | nothing | Ethernet frames | Network stack, DHCP, DNS, stream hand-off (spec 007) |
| `wb-proxyd` | Go | user, spawned by `wb-hostd` | credentials, CA signing handle | streams from `wb-netd` | HTTP policy, credential replacement, dependency gate, upstream connections (specs 007, 009) |
| `wb-guestd` | Go | root inside the guest | nothing | — (it *is* guest) | Users, PTY exec, git transport, port discovery (spec 006) |

## Design decisions

- **Secrets live in one process, and not the most exposed one.**
  `wb-netd` parses raw guest packets — the largest attack surface — and
  holds no secrets and opens no outbound connections. `wb-proxyd` holds
  credentials but only sees TCP byte streams already reassembled by
  `wb-netd` and validated against the DNS mapping. `wb-hostd` manages VMs
  but never touches a credential (S4, S12).
- **Each host daemon is self-sandboxed.** Host processes apply a macOS
  sandbox profile to themselves at startup that grants only what they
  need: `wb-netd` gets no filesystem and no network beyond inherited
  file descriptors; `wb-proxyd` gets outbound network and its Keychain
  items; `wb-hostd` gets its state directories and the Virtualization
  entitlement. This hardens our own code; it is not how the agent is
  contained (the VM is).
- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted; `wb-proxyd` re-checks
  the hostname, SNI and HTTP request. A bug in one layer does not open
  egress on its own.
- **One work VM, many projects; an isolated VM for the rest.** Apple
  allows two running macOS guests. The work VM hosts every trusted
  project, separated by guest user accounts; the isolated VM takes
  untrusted repositories (S8, N5).
- **No root on the host.** Nothing needs it: the file-handle network
  attachment and the userspace stack replace host networking features
  that would (S12, N4).

## Host state layout

```
~/.config/wraithbox/                 user-editable configuration (spec 009)
  config.toml                        global policy and defaults
  projects/<project-id>.toml         per-project policy, Brewfile reference
~/Library/Application Support/WraithBox/
  images/                            base images (APFS clones are cheap)
  vms/{work,isolated}/               VM bundles: disks, machine identity, saved state
  projects/<project-id>/landing.git  bare repo receiving session branches (spec 008)
  state.db                           SQLite: projects, sessions, approvals, caches
  run/                               0700 directory for Unix sockets
~/Library/Logs/WraithBox/            audit JSONL and daemon logs
```

## Inter-process contracts

All IPC is protobuf over gRPC: Unix sockets on the host (peer credentials
checked, sockets in a `0700` directory), vsock to the guest. The `.proto`
files are the single contract for both languages (spec 010).

**Status:** Draft
