# V1 - Initial version

**Purpose:** The first version: what it delivers, what it leaves out,
and the milestones that get there.

## Decisions

- **V1-01-macos-first: macOS first.** The first delivery target is macOS guests on
  macOS hosts. Linux guests (Ubuntu LTS first) and Windows 11 guests
  follow, on every host that can run them (S12-platforms). macOS guests
  run only on macOS hosts.
- **V1-02-claude-only: Claude Code only.** Claude Code is the supported agent. Keep
  agent-specific code behind one interface so another agent can be
  added later.
- **V1-03-win-linux-later: Windows and Linux hosts later.** Windows and Linux hosts are
  designed for but not delivered in V1. Decisions that would make them
  harder need a spec change.
- **V1-04-no-containers: No containers.** Running containers inside the sandbox is out
  of scope for V1.
- **V1-05-cli-only: CLI only.** A full GUI app is out of scope. The UI is the CLI
  plus system notifications for approvals.
- **V1-06-no-lfs-submodules: No Git LFS or submodules.** The git round trip
  (S08-workspace-and-git) carries plain repositories only.
- **V1-07-local-images: Images are built locally.** Each host builds its own
  guest images (S06-vm-lifecycle). Distributing images through a
  registry comes later.
- **V1-08-no-forward-clipboard: No port forwarding or clipboard.** Port
  forwarding (FR11-port-forward) and the clipboard (FR12-clipboard) are
  out of scope for V1, so nothing new crosses the VM boundary for them
  (S06-vm-lifecycle). The terminal filter drops OSC 52
  (X19-terminal-filter), so the clipboard cannot arrive through the
  terminal either. Decided on I38, and
  B38-port-forward-clipboard holds the analysis for when they are
  scheduled.

## Milestones

Each exit criterion cites the requirement IDs it proves. The maintainer
confirmed these on 2026-10-04, and each is a GitHub milestone of the
same name.

- **V1-M1-spikes-closed: Spikes answered and spec gaps closed.** X01-model-credential to
  X09-keychain-unsigned and X17-image-build to X28-git-alloc-limit written up in
  `docs/spikes/`, the spec issues of `docs/implementation-plan.md`
  decided, and specs updated. Covers no requirement by itself. It makes
  the rest possible to plan.
- **V1-M2-walking-skeleton: Walking skeleton.** `wb image build` makes a sealed image,
  `wb-vmd` boots it, `wb-guestd` answers over vsock, and `wb shell`
  opens a PTY as a project user. The guest has no network device yet.
  Covers SEC01-separate-kernel, SEC02-no-host-fs-share,
  SEC04-no-guest-secrets (image seal), SEC08-proj-isolation (users), FR02-any-repo.
- **V1-M3-network-floor: The network floor.** `wb-netd` and `wb-proxyd` in inspect
  mode with a static policy, the audit log, and the conformance cases
  for egress. Covers SEC05-default-deny, SEC10-audit,
  SEC11-root-gains-nothing, FR08-no-proxy-config.
- **V1-M4-claude-end-to-end: `wb claude` end to end.** Git round trip with carry-in,
  landing and flagging, the model credential, the terminal filter,
  `wb diff` and `wb land`. Covers FR01-drop-in to FR05-parallel-sessions,
  FR13-claude-state, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC06-repo-writes.
- **V1-M5-approvals: Approvals and policy.** Approvals with notifications and
  `wb approve`, learn mode, credential bindings, the dependency gate,
  `wb trust` with the boundary check, `wb policy explain`. Covers
  FR09-approve-unknown, FR10-learn-mode, FR15-inspect, SEC07-dep-gate,
  SEC09-host-policy, SEC14-no-fake-approvals, NFR06-explained-refusals.
- **V1-M6-release-gate: Release gate.** Self-sandboxed daemons, Seatbelt profiles,
  warm start and suspend, the full conformance suite and benchmarks,
  packaging. Covers SEC12-least-privilege, SEC13-bounded-resources,
  NFR01-startup, NFR03-footprint. The benchmarks also measure
  NFR02-fs-speed, which is a goal and doesn't pass or fail the gate.

After V1: X10-linux-hypervisor to X16-openshell-linux, layers 3 and 4 of
S13-guest-confinement, FR11-port-forward, FR12-clipboard, and the other
host and guest platforms.

**Status:** Draft
