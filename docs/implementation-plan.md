# Implementation plan for V1

**Status:** Draft, 2026-10-16. The specs in `docs/spec/` are
authoritative. This page cuts the milestones of V1-initial into work
packages, in the order V1-initial lists them. Each package is one
capability that ends in tests, mostly in one component, about 300 to
1,500 lines of code. A milestone's packages are filed as issues when
the milestone is next. Until then they are rows here, and the issues
folded into a row are cited by the package issue once it's filed.

## Where things stand

- **V1-M1-spikes-closed** closed on 2026-10-16: X01-model-credential to
  X29-dev-signing are answered (X10-linux-hypervisor to
  X16-openshell-linux are after V1), and the specs are split to the
  size limits of S01-spec-based-development.
- **Code:** about 500 lines. Go has the `wb` command-line parser
  (`internal/cli`), the host and guest platform matrix
  (`internal/platform`), `internal/version` and empty `main` packages.
  Swift has a `wb-vmd` executable and a `WraithBoxVM` library with a
  version constant. Everything below is new code.

## Lanes

Each package has a lane, which says where it can be built and tested
(`docs/agents/host-work.md`):

- **any:** Linux CI is enough.
- **mac:** macOS, no VM.
- **vm:** a running macOS guest (`needs-vm`).
- **host:** a setup on the maintainer's Mac (`needs-host`). H1 is a
  team-signed build (`WB_TEAM_ID`), H2 a macOS 27 host, H3 a fixture
  VM bundle for tests, H4 the `wbtest` account with a model token, and
  H5 the attended notification permission.

A VM package holds only integration and conformance code. Its logic
goes in an `any` or `mac` package, which runs in parallel, so the
`vm` lane stays short.

## How work is cut

A change gets its own issue when it changes a `.proto` file or an
`internal/platform` interface, belongs to another component, moves
where a `SEC*` control is enforced, changes lane, needs a maintainer
decision, could change a spec by measurement, or is more than about
300 lines. A review follow-up in the same spec section with no new
decision is folded into the component's next package. Defaults,
buffer sizes, retry delays and wording are decided in code with a
test (`docs/agents/planning.md`, "Follow-ups").

Contracts land with the first package that uses them, as its first
commit: the VM provider and hello in M2.1, the terminal stream in
M2.12, the hand-off in M3.4, git and sessions in V1-M4.

## V1-M2-walking-skeleton

No guest network device, and a fixture VM bundle (H3, #277) instead
of a built image. H1 is #276. Until M3.11, `wb-hostd` starts `wb-vmd`
and `wb-diskfmt` itself rather than through `wb-launcher`. `wb-hostd`
has no profile before M6.1, so no control is weaker in the meantime.

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M2.1 | Contracts and buf code generation: `vmd.v1`, `guest.v1` hello | S04-00, S10-01; NFR07 | | any | #159 |
| M2.2 | Stamped build version, `Wraith Box.app`, helper bundles, entitlement check, `wb setup` install | S10-02, S17-01, S17-02, S04-08 "Version skew"; C12 | M2.1 | mac, H1 | #174, #278, #279, #280 |
| M2.3 | Host plumbing: paths, peer-checked local IPC with a version handshake, one-descriptor channels, Seatbelt trampoline and deny probe | S12-01, S04-02, S04-03; SEC12 | M2.1 | mac | #281, #282 |
| M2.4 | `wb-vmd` device-set check, with the guest tools disk | S04-02, S06-06 "Devices"; SEC02, SEC05; C11 | M2.1 | any | #160 |
| M2.5 | `wb-vmd` confined boot and stop, slot checks, ConnectGuest, descriptor hand-off | S04-02, S04-03, S04-04; SEC01, SEC02, SEC08; C11 | M2.3, M2.4, H3 | vm | #161, #164 |
| M2.6 | `wb-guestd` core: vsock below 1024, hello and version form, tools-disk update, clock | S06-12, S06-06 "Time", S04-08; SEC11; C09, C11 | M2.1 | vm | #163 |
| M2.7 | `wb-hostd` core: `state.db`, config, VM supervision, port routing, caps, reconnect, generations, tools-disk clone, log pipes, `wb vm` | S04-02, S04-08, S06-07, S06-11, S06-12; SEC13; C11 | M2.5, M2.6 | any with a fake provider, mac, vm | #283 to #288 |
| M2.8 | Data disk: `wb-diskfmt`, records, guest mount by UUID, the new-data-disk command | S06-05, S06-03, S05-03; SEC15; C11 | M2.7 | mac, vm | #289, #290, #228 (measured on it), folded: #229 |
| M2.9 | Project identity and registration, host git checks, project settings | S05-04, S08-08, S09-03; FR02; C04 | M2.7 | mac | #291 to #294 |
| M2.10 | Project users: host ID allocation, guest create, lock and kill loop, homes, leftover-process results | S06-03, S06-08, S06-12, S13-01; SEC08; C09 | M2.8, M2.9 | vm | #295 to #298, folded: #138 (lock and keep the record) |
| M2.11 | Terminal filter, dropped links, reset on exit, with fuzz properties | S16-01, S16-02, X19-terminal-filter; SEC03, SEC14; C10 | | any | #299, #300, #301 |
| M2.12 | `wb shell` end to end: terminal-stream contract, session helper, PTY, environment, relay, signals, exit | S05-02, S05-03, S06-09, S16-03; C10 | M2.7, M2.9, M2.10, M2.11 | vm | #302, #303, #304 |

Also in V1-M2: CI and tooling (#58, #162, #217, #243, #120) and the
leftovers of V1-M1 in the docs (#169, #236, #237, #247).

## V1-M3-network-floor

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M3.1 | Packet path: datagram link endpoint, link filter, DHCP, caps, TCP rules | S07-02; SEC05; C01 | M2.3 | any | |
| M3.2 | DNS: pool, name form, local and negative answers, hold, wildcard budget, background list | S07-03, S07-04; C01 | M3.1 | any | folded: #124 (no leading or trailing hyphen in a label) |
| M3.3 | Policy loader and effective policy: OpenShell subset, bounded YAML, modes, per-VM union | S09-01, S09-02, S07-01; SEC09; C03 | | any | #259 (refuse rules that allow the OAuth token paths) |
| M3.4 | Hand-off, name mapping, accounting records (a 4 KiB record needs larger socket buffers than the 2 KiB macOS default) | S07-05, S07-06, S04-07, S04-08; SEC08; C02, C11 | M3.1 to M3.3 | mac | folded: #172 (ordering test on macOS and Linux CI), #179 (malformed report rule, cross-VM restart case) |
| M3.5 | Stream checks: first bytes, SNI binding, upstream address rule, pass and raw TCP, tunnels | S07-07, S07-15, S07-16; SEC05; C02 | M3.4 | any | |
| M3.6 | HTTP termination: h1 and h2, request form, caps, transfer coding, guest credential and response header removal | S07-08; SEC04; C02, C05 | M3.5 | any | folded: #246 (rule for a client that rejects the inspected leaf) |
| M3.7 | HTTP policy engine and built-in profiles, parts, GraphQL filter (allowlist starts empty) | S07-11, S07-07; SEC06 | M3.3 | any | folded: #206 (closed list of what audit mode relaxes, `audit-allowed` rule) |
| M3.8 | Secret store shim and `wb-credset` | S15-02, S15-03, S12-03; SEC12; C05 | M2.2, H1 | mac, host | |
| M3.9 | Inspection CA: Secure Enclave key, leaves, rotation, removal list, `wb vm reset-ca`, guest trust | S09-06, S09-07, S09-08; C02 | M3.8 | any, host, vm | |
| M3.10 | Audit writer and event channel: OCSF, budgets, free space, a failed write stops the VM; the records V1-M2 only logs (data disk reset, git checks) | S09-11, S04-08; SEC10; C11 | M2.7 | any | folded: #177 (bound on the event retry queue) |
| M3.11 | Supervision and `wb-launcher` | S04-03, S04-07; SEC13; C11 | M2.3 | mac | |
| M3.12 | Egress integration: the NIC in the device set, a `wb-netd` per VM, flow reset on restore | S04-02; SEC05, SEC10, SEC11, FR08; C01, C02 | M3.1 to M3.11, H1 | vm, host | |

## V1-M7-image-build

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| IMG.0 | Spec changes before the build: `wb-build-ssh` on Go's SSH library, macOS 27 for the build, the base layer, Claude Code's release and top layer, the Xcode variant, the secret scanner | S04-00, S04-05, S15-04, NFR04, S06-01, S06-02, S14-01, S17-04, S06-04 | | any | #165, #143, #150, #220, #194, #224, secret scanner |
| IMG.1 | Downloads: `wb-download`, catalog, resume, hash, records, the Command Line Tools entry | S17-03, S17-04; SEC10; C12 | M3.10, M3.11 | any | |
| IMG.2 | `wb-vmd-install`, build bundle, sealed image, slot lending, `wb image` | S06-02, S04-04, S06-07; SEC15; C11 | IMG.1, H2 | vm | #262 (X30-installer-trust) first |
| IMG.3 | Build network (one inbound port 22, refused for session VMs) and `wb-build-ssh` | S04-05, S15-04 | M3.1, IMG.2 | vm | |
| IMG.4 | `wb-guestadmin`: build, rotation, cleanup, first-contact gate, disk replacement and user recreation | S15-04, S06-03; SEC04, SEC12; C09 | M3.8, IMG.3 | vm, host | folded: #154 (rotation with both passwords on standard input), #155 (no password in the bundle), #142 (optional: can guest root revive the provisioning user's token) |
| IMG.5 | Named provisioning operations, terminfo, sealing scan | S06-02; SEC04 | IMG.4, secret scanner | vm | folded: #203 (`pairs#0x7fff` alias for `xterm-kitty`) |
| IMG.6 | Base layer: Command Line Tools from the host-fetched package, `_wbtools`, Homebrew and its snapshot; derived images from recipes | S06-01, X20-shared-homebrew; FR06; C09 | IMG.5 | vm | |
| IMG.7 | Claude Code in its own top layer: `wb-release-check`, install, `wb image list` | S14-01, S17-04; FR01 | IMG.1, IMG.5 | any, vm | |
| IMG.8 | Xcode variant from a host-built read-only disk image | S06-01, S06-04; FR06 | IMG.6 | vm | |

## V1-M4-claude-end-to-end

The first prompt comes first: M4.10 and the launch part of M4.12 on
the fixture bundle, before the git packages.

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M4.0 | Spec changes: host git hardening, the `--isolated` home, `wb land` of the commit `wb diff` showed | S08-01 to S08-06, S06-04, S06-08, T05 | | any | #72 (folded: #130, #152), #128, #137 |
| M4.1 | `export.git` writer, export lock, `wb-git` shim | S08-02, S08-07, S04-06; SEC12 | M3.11, #72 | mac | |
| M4.2 | Transport: `git-remote-wb`, `wb-guestd` relay with uid attribution, framing fuzz, clone, the withheld-URL records, fsync settings | S08-01, S04-02, S06-05 "Durability"; SEC02, SEC08; C08 | M4.1 | any, vm | #231 (git part) |
| M4.3 | Push gate: ref filter, receive-pack settings, `GIT_ALLOC_LIMIT` probe, size cap, bounds | S08-03, S08-05; SEC06; C08 | M4.2 | any | #71 |
| M4.4 | Pack scanner and pre-receive check, with a differential test against `index-pack` | S08-04, X26, X28; SEC13; C08 | M4.3 | any | |
| M4.5 | `wb land`, `wb diff`, `wb discard`, fetch checks, risky-path flagger | S08-06, S08-09; FR03, SEC03 | M4.1 | mac | #70 |
| M4.6 | Session lifecycle, host: states, admission, deadlines, `wb sessions`, `wb status` and the health table | S06-09, S06-04; FR05 | M2.12 | any | |
| M4.7 | Session lifecycle, guest: reports, link drop, recovery, kill loop, leftovers, the launchd domain removal | S06-10, S06-11, S13-01; C09 | M4.6, M4.3 | vm | #196 (launchd domain check) |
| M4.8 | Carry-in: collect, scan, apply | S08-01; FR04 | secret scanner | mac, vm | |
| M4.9 | `~/.claude` carry-in: filter, caps, write as the user | S14-02, S14-03; FR13 | secret scanner | any, vm | |
| M4.10 | Bindings and model credential: placeholders, injection, streaming scan, `wb cred`; long-lived token and API key | S09-05, S07-08, S07-09; SEC04; C05, C06 | M3.6 to M3.9, #253 | any, H4 | #253 (folded: #226) |
| M4.11 | Terminal records and session summary | S09-12, S16-03, S05-01; SEC10 | M2.11, M3.10 | any | folded: #199 (escaper and report parser fuzz targets) |
| M4.12 | `wb claude`: agent interface, environment, history link, job control; C06 end to end | S14-00, S06-08, S05-01, S05-02; FR01, FR13 | V1-M4, IMG.7, H4 | vm, host | #135, #231 (Claude Code part) |

## V1-M5-approvals

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M5.1 | Approval flow, limits, expiry, name display, re-run during a hold, `wb approve` and `wb deny` | S09-10, S07-04, S07-17; FR09, SEC14 | M3.2 | any | folded: #173 (timing test of the policy order) |
| M5.2 | Approved rule, risk check, joined approvals | S09-09, S07-01 | M3.3 | any | |
| M5.3 | Boundary check: prover vendoring, `wb-prover`, projection, cache | S09-04, S10-01; SEC09 | M3.11 | mac | folded: #207 (extension check asserts the audit projection) |
| M5.4 | Notification helper and its `wb-hostd` contract, spec drafted first | S12-01, S09-09 "Delivery", S04-07 | M5.1, H5 | host | |
| M5.5 | Learn mode, `wb learn report` | S07-17; FR10 | M5.1 | any | |
| M5.6 | `.wraithbox/`, `wb trust` and `wb untrust` (the `wb project` commands are #294) | S09-03, S05-03, S05-04; SEC09 | M5.3 | any | |
| M5.7 | Dependency gate: mappers, metadata filter, OSV, how the gate authenticates its own fetch | S07-12, S07-13; SEC07; C07 | M3.6 | any | #193 (branch and commit `.info` queries) |
| M5.8 | Go clock: checksum database, tiles, heads, reset | S07-13, S07-14; SEC07 | M5.7 | any | |
| M5.9 | General bindings, anonymous credentials, part switches, policy changes while running | S07-10, S07-11, S07-01; SEC06 | M4.10 | any | folded: #153 (a binding names its token endpoint), #208 (a confirmed remote change widens policy) |
| M5.10 | `wb policy`, `wb allow`, `wb audit` | S05-03, S09-02, S09-11; FR15, NFR06 | M5.2, M5.3 | any | folded: #207 (`wb policy explain` shows the audit projection) |
| M5.11 | Toolchain reconcile | S06-01, X20-shared-homebrew; FR07; C09 | IMG.6, M3.12 | any, vm | #180 (folded: #181) |
| M5.12 | Approval and gate conformance | C03, C07 | V1-M5 | vm, host | |

## V1-M6-release-gate

| # | Package | Specs and IDs | Lane | Issues |
|---|---|---|---|---|
| M6.1 | `wb-hostd` profile, rules and probes | S04-06; SEC12 | mac | |
| M6.2 | Confinement self-checks for every program, `wb-vmd` ablation of graphics and input | S04-03, S06-06 | mac, vm | #247 (ablation) |
| M6.3 | Session Seatbelt profiles, the Xcode and simulator profile, safehouse manifest | S13-02 | vm | #90, #196 (simulator profile) |
| M6.4 | Warm start and suspend: clones, permanence, idle; reconcile SEC15-no-guest-disk-mount's rule on clones with S06-06's restore from clones | S06-06, S06-09; NFR03 | vm | |
| M6.5 | Bounds sweep against the S04-08 table, per-VM caps | SEC13 | any | |
| M6.6 | Upgrades, migrations, profile expiry, `wb setup --remove`, release signing check | S17-05, S12-03, S15-01 | mac, host | #118 |
| M6.7 | Benchmarks and measured defaults | S11; NFR01 to NFR03 | vm | #148, #175 |
| M6.8 | Full conformance run and gap sweep | C00-index | vm, host | |

## Critical path

The `vm` lane, about 21 packages in series, with one VM builder at a
time:

H3, M2.5, M2.6, M2.7, M2.8, M2.10, M2.12, M3.12 (H1), IMG.2 (H2),
IMG.3, IMG.4, IMG.5, IMG.6, IMG.7, M4.2, M4.7, M4.12 (H4), M5.12,
M6.3 and M6.4, M6.7, M6.8.

The maintainer's decisions reach the lane before it does: the spec
changes of IMG.0 come first in V1-M7-image-build, and the
notification helper contract is drafted with M5.4. About 35 packages
don't need a VM and run three or four at a time beside it.
