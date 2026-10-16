# S04-01 - Design Principles

**Purpose:** The design decisions that every host and guest process follows.

## Design

- **One CLI, dispatcher style.** `wb` is the entry point for every
  command. Agent commands (`wb claude …`) pass every following argument
  to the agent untouched. The agent's own flags and subcommands then
  never collide with Wraith Box's (S05-cli).
- **Cross-platform core, native edges.** These are Go, and the same on
  every host OS: session management, policy, approvals, audit, the git
  gateway, the network stack, the proxy, the guest agent, and the CLI.
  Only the pieces that must call a platform API with no good Go binding
  are native: `wb-vmd` on macOS (Virtualization framework) and the
  user-interface helpers. Each is a separate process behind a gRPC
  contract, with no policy decisions of its own (S12-platforms,
  NFR07-maintainability).
- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Guest confinement narrows, never widens.** Controls inside the
  guest can only deny what the host would allow. They are Seatbelt
  profiles and a Network Extension that labels flows with the program
  that opened them. Every `SEC*` control holds without them
  (S13-guest-confinement, T06-forged-labels).
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted. `wb-proxyd`
  re-checks the hostname against its own copy of the name mapping and
  the VM's effective policy, then the SNI and the HTTP request. A bug
  in one layer does not open egress on its own.
- **One work VM per guest OS, many projects; an isolated VM for the
  rest.** The work VM hosts every project placed there for its guest
  OS, new projects included, separated by guest user accounts. An
  isolated VM takes projects placed there and `--isolated` sessions
  (S06-04-vms-and-placement). For macOS guests this fits Apple's two-VM
  limit (SEC08-proj-isolation, NFR05-two-macos-vms).
  - The accounts keep the projects' files apart.
  - The host can't tell their connections apart. So it enforces the
    policy and the credential bindings of a VM's projects for the whole
    VM (S07-01-per-vm-policy, T11-shared-vm-grants).
- **No root or administrator rights at run time.** The packet transport
  and the userspace stack replace host networking features that would
  need them (SEC12-least-privilege, NFR04-host-platforms).

### What stays of the secrets rule

The early rule on secrets is replaced (see "Decisions"). These parts
of it still hold:

- `wb-netd` parses raw guest packets, the largest attack surface. It
  holds no secrets and opens no outbound connections.
- `wb-proxyd` sees only TCP byte streams that `wb-netd` already
  reassembled and validated against the DNS mapping.
- `wb-hostd` has no store access. It may pass on a descriptor with a
  secret in it without reading it (SEC04-no-guest-secrets,
  SEC12-least-privilege).

## Decisions

### Secrets live in one process, and not the most exposed one (replaced)

This was a terse early decision. S15-least-privilege replaced it on
2026-10-07.

- **Why replaced:** the detailed design showed that it gives one
  guest-exposed process, `wb-proxyd`, every secret.
- **Now:** the configured secret store holds the credentials. Each
  process fetches or uses only its declared items, just in time.
  `wb-proxyd` gets only what proxying needs (S15-least-privilege).

**Status:** Draft
