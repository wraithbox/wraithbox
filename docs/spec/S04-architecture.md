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
| `wb-vmd` | platform-native | user, spawned by `wb-hostd` | VM handles | none (devices only) | Create, start, stop, save, and restore VMs; hand guest socket connections and the NIC endpoint to other processes (S12-platforms) |
| `wb-netd` | Go | user, one per VM, spawned by `wb-hostd` | nothing | Ethernet frames | Network stack, DHCP, DNS, stream hand-off (S07-egress-gateway) |
| `wb-proxyd` | Go | user, spawned by `wb-hostd` | credentials, CA signing handle | streams from `wb-netd` | HTTP policy, credential replacement, dependency gate, upstream connections (S07-egress-gateway, S09-policy-credentials-audit) |
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
  the agent is contained (the VM is).
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
