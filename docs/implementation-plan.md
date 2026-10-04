# Initial implementation plan

**Status:** Draft, 2026-10-04. The specs in `docs/spec/` are
authoritative. This page orders the work toward v1 (macOS host, macOS
guest) and lists what must be answered before milestones and
implementation issues can be cut. Real milestones replace it once
they exist.

## Where things stand

- **Specs** 003 to 013 are drafts. Spec 011 lists 24 open questions
  (spikes `X1` to `X24`). None is answered yet.
- **Go** (`packages/wraithbox-go`): the `wb` command-line parser
  (`internal/cli`), the host and guest platform matrix
  (`internal/platform`), and empty `main` packages for `wb`,
  `wb-hostd`, `wb-netd`, `wb-proxyd` and `wb-guestd`.
- **Swift** (`packages/wraithbox-swift`): a `wb-vmd` executable and a
  `WraithBoxVM` library with a version constant.
- Nothing else yet: `proto/`, VM code, network code and milestones
  are all still to come.

## Approach

1. **Spikes and spec decisions first** (M0 below). Most components
   rest on an assumption that a spike tests, and some specs
   disagree with each other or leave a hole. Building before those are
   settled means rebuilding.
2. **Host floor before features.** Boot a guest, then cut its network
   down to the gateway, then return work through git. Each step ends
   with conformance checks from spec 011 for the controls it brings.
3. **Pure logic in parallel.** Parsers and policy code that face guest
   bytes need no VM. Builders can write them, with fuzz targets, while
   the VM spikes run, once the spike or decision they depend on is
   merged.

## Critical path

```text
X17 image build ─┐
X18 vsock, fds ──┼─▶ VM boots, wb-guestd answers ─▶ wb shell as a project user
X2  warm start ──┘                │
                                  ▼
X3 network path ─▶ wb-netd: DHCP, DNS, synthetic addresses ─▶ wb-proxyd: inspect, policy, audit
X4, X9, X22, X24 ────────────────────────────────────────────┘           │
                                                                         ▼
X7 git round trip ─▶ carry-in, landing repository, wb diff, wb land ─▶ wb claude end to end
X1 model credential, X19 terminal filter ───────────────────────────────┘
```

The VM spikes (X2, X3, X5 to X8, X17, X18, X20) share one constraint:
a Mac runs at most two macOS guests at a time (N5-two-macos-vms). Only one or two of
them can run at once per machine (#50).

## Components and what they wait on

| Component | Work that needs no VM | Waits on |
|---|---|---|
| `wb` | TTY relay against a local test server, session and project naming | X19 (#30), #37, #42, #46 |
| `wb-hostd` | settings loader, `state.db`, audit writer, risky-path flagger (spec 008) | #52, #46, X7 (#21), X24 (#35) |
| `wb-vmd` (Swift) | none | X2 (#16), X17 (#28), X18 (#29) |
| `wb-netd` | DHCP and DNS logic on gVisor's in-memory link, with fuzz targets | X3 (#17), #36, #51 |
| `wb-proxyd` | ClientHello parsing, leaf issuing with a software key, HTTP rule matching | X4 (#18), X9 (#23), X21 (#32), X22 (#33), #39 |
| `wb-guestd` | user and PTY logic behind interfaces | X18 (#29), X6 (#20), X8 (#22), X20 (#31) |
| network policy | OpenShell schema parser with a bounded YAML decoder | X24 (#35) |
| `internal/platform` | paths, local IPC with peer checks | none |
| Claude Code in the guest | none | X1 (#15), #45 |

## Proposed milestones

For the maintainer to confirm, rename, or cut differently. Each exit
criterion cites the requirement IDs it proves.

- **M0: v1 spikes answered and spec gaps closed.** X1 to X9 and X17
  to X24 written up in `docs/spec/spikes/`, the spec issues below
  decided, and specs updated. Covers no requirement by itself. It makes
  the rest possible to plan.
- **M1: walking skeleton.** `wb image build` makes a sealed image,
  `wb-vmd` boots it, `wb-guestd` answers over vsock, and `wb shell`
  opens a PTY as a project user. The guest has no network device yet.
  Covers S1-separate-kernel, S2-no-host-fs-share, S4-no-guest-secrets (image seal), S8-proj-isolation (users), F2-any-repo.
- **M2: the network floor.** `wb-netd` and `wb-proxyd` in inspect
  mode with a static policy, the audit log, and the conformance cases
  for egress. Covers S5-default-deny, S10-audit, S11-root-gains-nothing, F8-no-proxy-config.
- **M3: `wb claude` end to end.** Git round trip with carry-in,
  landing and flagging, the model credential, the terminal filter,
  `wb diff` and `wb land`. Covers F1-drop-in to F5-parallel-sessions, F13-claude-state, S3-no-host-exec, S4-no-guest-secrets, S6-repo-writes.
- **M4: approvals and policy.** Approvals with notifications and
  `wb approve`, learn mode, credential bindings, the dependency gate,
  `wb trust` with the boundary check, `wb policy explain`. Covers F9-approve-unknown,
  F10-learn-mode, F15-inspect, S7-dep-gate, S9-host-policy, S14-no-fake-approvals, N6-explained-refusals.
- **M5: v1 release gate.** Self-sandboxed daemons, Seatbelt profiles,
  warm start and suspend, the full conformance suite and benchmarks,
  packaging. Covers S12-least-privilege, S13-bounded-resources, N1-startup to N3-footprint.
- **Later:** X10 to X16, spec 013 layers 3 and 4, other host and
  guest platforms.

## Issues filed for M0

Spikes from spec 011. X14 (#10), X15 (#11) and X16 (#12) were filed
before this plan, and don't block v1.

| Spike | Issue | Needs |
|---|---|---|
| X1 Model credential via the proxy | #15 | maintainer's Claude subscription |
| X2 Warm start | #16 | VM |
| X3 Network path | #17 | VM |
| X4 Inspection compatibility | #18 | |
| X5 Filesystem benchmark | #19 | VM |
| X6 Guest users and Xcode | #20 | VM |
| X7 Git round trip | #21 | |
| X8 Data disk for homes | #22 | VM |
| X9 Keychain access without a signing identity | #23 | |
| X10 Linux VMM and packet transport (later) | #24 | Linux machine, after X16 |
| X11 Windows host through HCS (later) | #25 | Windows 11 Home machine |
| X12 WSL client channel (later) | #26 | Windows machine |
| X13 Windows guests on a macOS host (later) | #27 | Windows media |
| X17 Unattended image build | #28 | VM |
| X18 Host-guest socket and descriptor hand-off | #29 | VM |
| X19 Terminal stream filtering | #30 | |
| X20 Homebrew with more than one project user | #31 | VM |
| X21 Dependency gate on real registries | #32 | |
| X22 Clients without guest credentials | #33 | |
| X23 Self-sandboxed Go daemons on macOS | #34 | |
| X24 OpenShell artifacts | #35 | |

Spec gaps, contradictions and missing specs:

| Issue | Topic | Blocked by |
|---|---|---|
| #36 | Network flows in the shared work VM can't be attributed to a project | |
| #37 | The host terminal stream as a boundary | #30 |
| #38 | Port forwarding and clipboard design and threats | |
| #39 | CA name constraints and rotation versus live policy changes | |
| #40 | Learn mode and pass mode as S5-default-deny exceptions | |
| #41 | Which git data reaches the guest, and host git as a parser | |
| #42 | Configuration trust versus VM placement, project identity | |
| #43 | VM slot admission for image builds and GUI work | #20 |
| #44 | Data disk durability, never mounting a guest-written disk | #19, #22 |
| #45 | New spec 014: Claude Code in the guest, agent interface | #15 |
| #46 | Session lifecycle, detach, sleep and failure | |
| #47 | Non-HTTP streams, SSH remotes and ECH | |
| #48 | Process supervision, version skew, host resource limits | |
| #49 | Installation, restore image download, upgrades | #23, #28 |
| #50 | Test infrastructure for work that needs a VM | |
| #51 | Approval flow and approval noise | #17, #36 |
| #52 | First `.proto` contracts and settings schema | #29, #46 |

## Read these first

Ordered by how much of the design they can move:

1. **#36.** In the shared work VM, `wb-netd` sees one address for all
   projects. Per-project policy and credential bindings, as specs 007
   and 009 describe them, are then the union of all projects in the
   VM, even without guest root. Settling this may move X14 (#10) into
   v1.
2. **#31.** Homebrew has one prefix per machine. Project users sharing
   a writable prefix can plant binaries for each other, which breaks
   S8-proj-isolation without any root escalation.
3. **#30 and #37.** The terminal relay passes guest bytes to the host
   terminal emulator, which can write the host clipboard and, in some
   emulators, files.
4. **#33 and #32.** Removing every guest `Authorization` header breaks
   anonymous registry tokens (Homebrew bottles from `ghcr.io`).
   Refusing young package downloads may fail most fresh installs. Both
   decide whether F7-toolchain-manifest and S7-dep-gate are usable.
5. **#39 and #40.** Specs disagree with each other (#39), or weaken
   S5-default-deny without saying so (#40).

## Next steps

1. Triage the issues above: labels, priority, `ready-for-agent` or
   `ready-for-human`.
2. Create milestone M0 and put the triaged v1 spikes and spec issues in
   it. Decide #50 before running VM spikes in parallel.
3. As M0 closes, rewrite this page into milestones M1 to M5 with
   implementation issues per component, each written to the
   `ready-for-agent` standard in `docs/agents/planning.md`.
