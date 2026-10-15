# S04-01 - Design Principles

**Purpose:** The design decisions that every host and guest process follows.

## Design

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
- ~~**Secrets live in one process, and not the most exposed one.**~~
  Superseded by S15-least-privilege on 2026-10-07. This was a terse
  early decision, and the detailed design showed that it gives one
  guest-exposed process, `wb-proxyd`, every secret. Now credentials are
  held by the configured secret store, and each process fetches or uses
  only its declared items, just in time. `wb-proxyd` gets only what
  proxying needs. Still true: `wb-netd` parses raw guest packets, the
  largest attack surface, and holds no secrets and opens no outbound
  connections. `wb-proxyd` sees only TCP byte streams already
  reassembled by `wb-netd` and validated against the DNS mapping.
  `wb-hostd` has no store access, and may pass on a descriptor with a
  secret in it without reading it (SEC04-no-guest-secrets,
  SEC12-least-privilege).

- **Everything in the guest is untrusted, including `wb-guestd`.** The
  host validates every message from the guest as adversarial input. The
  guest agent is a convenience for the host, not a security component.
- **Guest confinement narrows, never widens.** Controls inside the
  guest (Seatbelt profiles, a Network Extension that labels flows with
  the program that opened them) can only deny what the host would allow.
  Every `SEC*` control holds without them (S13-guest-confinement, T06-forged-labels).
- **Policy is evaluated on the host, twice.** `wb-netd` decides which
  names resolve and which connections are accepted. `wb-proxyd`
  re-checks the hostname against its own copy of the name mapping and
  the VM's effective policy, then the SNI and the HTTP request. A bug
  in one layer does not open egress on its own.

- **One work VM per guest OS, many projects; an isolated VM for the
  rest.** The work VM hosts every project placed there for its guest
  OS, new projects included, separated by guest user accounts. An
  isolated VM takes projects placed there and `--isolated` sessions
  (S06-vm-lifecycle, "VMs"). For macOS guests this fits Apple's two-VM limit (SEC08-proj-isolation, NFR05-two-macos-vms).
  The accounts keep the projects' files apart. The host can't tell
  their connections apart, so it enforces the policy and the
  credential bindings of a VM's projects for the whole VM
  (S07-egress-gateway, "Enforced per VM", T11-shared-vm-grants).
- **No root or administrator rights at run time.** The packet transport
  and the userspace stack replace host networking features that would
  need them (SEC12-least-privilege, NFR04-host-platforms).

**Status:** Draft
