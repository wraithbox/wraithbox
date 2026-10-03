---
title: Design overview
description: How Wraith Box isolates Claude Code, in one page.
---

The authoritative design lives in the repository under
[`docs/spec/`](https://github.com/lsimons/wraithbox/tree/main/docs/spec).
This page is a summary.

## Threat model in one paragraph

Anything inside the sandbox may be controlled by an attacker: a prompt
injection in a README, issue, dependency or web page can take over the
agent and get root in the guest. So every control that matters is
enforced on the host, and full control of the guest grants none of them.

## The pieces

| Process | Language | Role |
|---|---|---|
| `wb` | Go | Drop-in for `claude`; relays your terminal |
| `wbctl` | Go | Management: review and land work, policy, approvals, audit |
| `wb-hostd` | Swift | Runs the VMs, sessions, git round trip, approvals |
| `wb-netd` | Go | Userspace network stack (gVisor) behind the VM's virtual NIC; DNS |
| `wb-proxyd` | Go | HTTP policy, credential replacement, dependency gate |
| `wb-guestd` | Go | Agent inside the guest: users, terminal sessions, git transport |

Two channels cross the VM boundary: vsock to `wb-hostd`, and Ethernet
frames to `wb-netd`. Nothing else.

## Key decisions

- **macOS guest, separate kernel.** No same-kernel sandbox mode.
- **No host mounts.** The project is cloned into the guest; the agent's
  work returns as a git branch. Changes to files your machine or CI might
  execute are flagged.
- **No secrets in the guest.** Placeholders only; the proxy removes
  whatever credential the guest sends and injects the real one.
- **Default-deny egress.** Only allowlisted names resolve, to synthetic
  addresses that lead only to the proxy. No raw IPs, no host, no LAN, no
  arbitrary DNS.
- **One work VM, many projects.** macOS allows two running macOS guests,
  so trusted projects share a work VM as separate guest users; untrusted
  repositories get the second, isolated VM.
- **No root on the host, no extra host accounts.** Works on managed Macs.

## Specifications

| Spec | Topic |
|---|---|
| 003 | Requirements and threat model |
| 004 | Architecture: processes and boundaries |
| 005 | `wb` and `wbctl` |
| 006 | Images, VMs, guest users, guest agent |
| 007 | Network stack, DNS, proxy, dependency gate |
| 008 | Workspace and the git round trip |
| 009 | Policy, credentials, certificates, audit |
| 010 | Languages, libraries, packaging |
| 011 | Verification and open questions |
