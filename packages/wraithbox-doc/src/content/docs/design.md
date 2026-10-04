---
title: Design overview
description: How Wraith Box isolates Claude Code, in one page.
---

The authoritative design is in the repository under
[`docs/spec/`](https://github.com/wraithbox/wraithbox/tree/main/docs/spec).
This page is a summary.

## Threat model in one paragraph

Anything inside the sandbox may be controlled by an attacker: a prompt
injection in a README, issue, dependency, or web page can take over the
agent and get root in the guest. So every control that matters is
enforced on the host, and full control of the guest grants none of them.

## The pieces

| Process | Language | Role |
|---|---|---|
| `wb` | Go | The one command: `wb claude …` runs a session and relays your terminal; `wb diff`, `wb land`, `wb approve`, … manage the rest |
| `wb-hostd` | Go | Sessions, git round trip, policy, approvals, audit |
| `wb-vmd` | platform-native (Swift on macOS) | Runs the VMs; no policy, no secrets |
| `wb-netd` | Go | Userspace network stack (gVisor) behind the VM's virtual NIC; DNS |
| `wb-proxyd` | Go | HTTP policy, credential replacement, dependency gate |
| `wb-guestd` | Go | Agent inside the guest: users, terminal sessions, git transport |

Only two channels cross the VM boundary: a host-guest socket (vsock)
to `wb-hostd`, and Ethernet frames to `wb-netd`.

## Key decisions

- **One command.** `wb [wb flags] <command> [args]`; everything after
  `wb claude` goes to Claude Code unchanged.
- **Separate kernel.** A real VM, never a same-kernel sandbox. First
  target: macOS guests on Macs.
- **Cross-platform core.** Most code is Go and the same on macOS,
  Windows, and Linux hosts; platform-native code is confined to small
  helpers. `wb` inside WSL uses the Windows host's VMs.
- **No host mounts.** The project is cloned into the guest; the agent's
  work returns as a git branch. Changes to files your machine or CI might
  execute are flagged.
- **No secrets in the guest.** Placeholders only; the proxy removes
  whatever credential the guest sends and injects the real one.
- **Default-deny egress.** Only allowlisted names resolve, to synthetic
  addresses that lead only to the proxy. The guest cannot reach raw IP
  addresses, the host, the LAN, or arbitrary DNS servers.
- **One work VM, many projects.** Trusted projects share a work VM as
  separate guest users; untrusted repositories get an isolated VM. (macOS
  allows two running macOS guests, which this fits.)
- **No root or admin on the host, no extra host accounts.** Works on
  managed machines.

## Specifications

| Spec | Topic |
|---|---|
| spec 003-requirements | Requirements and threat model |
| spec 004-architecture | Architecture: processes and boundaries |
| spec 005-cli | The `wb` command |
| spec 006-vm-lifecycle | Images, VMs, guest users, guest agent |
| spec 007-egress-gateway | Network stack, DNS, proxy, dependency gate |
| spec 008-workspace-and-git | Workspace and the git round trip |
| spec 009-policy-credentials-audit | Policy, credentials, certificates, audit |
| spec 010-tech-stack | Languages, libraries, packaging |
| spec 011-verification-and-spikes | Verification and open questions |
| spec 012-platforms | Host and guest platforms, WSL |
| spec 013-guest-confinement | Guest confinement: Seatbelt, Network Extension, Endpoint Security |
