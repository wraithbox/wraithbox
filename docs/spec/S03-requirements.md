# S03 - Requirements

**Purpose:** The problem Wraith Box solves, and where the requirements
that answer it are.

## Problem

Coding agents such as Claude Code are most useful when they can run tools
without asking permission for each step. Running them that way directly on
a developer's machine exposes everything the developer can reach: files,
credentials, other repositories, accounts. Wraith Box runs Claude Code
inside a disposable-feeling but persistent virtual machine, so the agent
keeps a native toolchain for the operating system it works on while the
host, its secrets, and its network stay under host-side control.

The first delivery target is macOS guests on macOS hosts. The design is
for macOS, Windows, and Linux hosts, with Linux and Windows guests on all
of them (S12-platforms); platform-specific code is kept behind narrow
interfaces so later platforms do not reshape the core.

The design assumes the agent is **capable and possibly adversarial**: a
prompt injection in a README, issue, dependency, or web page may take full
control of everything inside the sandbox, including root in the guest.
Agents are increasingly good at finding ways out of their environment, so
every control that matters is enforced *outside* the guest.

## Requirements

Every other spec traces back to these:

- FR00-index: what Wraith Box must do for the user.
- SEC00-index: the security controls, never weakened.
- NFR00-index: how fast, how small, on which platforms.
- T00-index: the adversary, the assets, the trust boundaries, and the
  risks accepted rather than solved.
- V1-initial: what the first version delivers and leaves out.

## Unit of enforcement

Projects whose `placement` is `work`, the default for a new project,
share the work VM, each as its own guest user (S06-vm-lifecycle).
The host keeps their files, settings and returned work apart, but it sees one network address per VM and can't tell
which project opened a connection. So it enforces network grants,
credential bindings and approvals per VM: any process in a VM can use
those of every project with a session there (S07-egress-gateway,
"Enforced per VM"). Per-project rules narrow that only by a label the guest reports.
The maintainer accepted this residual risk on I36, and T00-index
records it as T11-shared-vm-grants. SEC06-repo-writes and
SEC08-proj-isolation say what is enforced per VM and what per project.

Sessions started with `--isolated` and projects marked shared run in
the isolated VM together. The repositories the user trusts least
aren't isolated from each other there (T05-cross-proj-clones). A new project starts in the work VM and gets
the grants of the work-VM projects (T13-new-project-grants). The
maintainer accepted both on I42 (B42-trust-placement).

**Status:** Draft
