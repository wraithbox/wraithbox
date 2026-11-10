# Implementation plan for V1

**Status:** Draft, updated 2026-11-10. The specs in `docs/spec/` are
authoritative. This page cuts the milestones of V1-initial into work
packages, in the order V1-initial lists them. Each package is one
capability that ends in tests, mostly in one component. A package is
filed as one or more issues, and a package of V1-M7 or V1-M4 has its
issues lettered as parts (IMG.4a, M4.6b). A milestone's packages are
filed as issues when the milestone is next. Until then they are rows
here, and the issues folded into a row are cited by the package issue
once it's filed.

**Sizes.** One issue's source code is about 300 to 1,500 lines. A
package is several issues. R09-v1-progress-and-quality measured the
closed V1-M2 packages: the median package changed about 7,000 lines,
3,900 of them source, and only 8 of 43 package issues were inside the
range. The range fits one issue's source code, not a whole package.
Agents closed about 3 package issues a day.

## Where things stand

- **V1-M1-spikes-closed** closed on 2026-10-16: X01-model-credential to
  X29-dev-signing are answered (X10-linux-hypervisor to
  X16-openshell-linux are after V1), and the specs are split to the
  size limits of S01-spec-based-development.
- **V1-M2-walking-skeleton** closed on 2026-10-31. `wb-vmd` boots a
  fixture bundle in its confined process, `wb-guestd` answers over
  vsock, a data disk made on the host holds the homes, and `wb shell`
  opens a PTY as a project user through the terminal filter.
- **V1-M3-network-floor** is in progress. M3.1 to M3.10 are merged
  except the HTTP/1.1 request path (#498), the Keychain and Secure
  Enclave calls on the maintainer's Mac (#505, #509) and `wb-proxyd`'s
  program (#512). Egress in a VM (#513) closes the milestone, with the
  fixes found in review and triage.
- **V1-M7-image-build and V1-M4-claude-end-to-end** were cut into
  issues on 2026-11-10. V1-M5-approvals and V1-M6-release-gate are
  still rows, and are cut when each is next.
- **Code:** about 75,000 lines of Go source, 78,000 lines of Go tests
  and 16,000 lines generated from `.proto` files. Swift has about
  7,200 lines of source and 3,900 of tests. `wb-hostd`, `wb-netd`,
  `wb-guestd`, `wb-diskfmt` and `wb-vmd` run. `wb-proxyd`'s logic is
  in `internal/proxyd`, and its program is #512. `wb-credset` and
  `wb-guestadmin` are still stubs. `wb` runs `setup`, `vm`, `project`,
  `shell` and `version`. Its other commands print "not implemented
  yet".

## Lanes

Each package has a lane, which says where it can be built and tested
(`docs/agents/host-work.md`):

- **any:** Linux CI is enough.
- **mac:** macOS, no VM.
- **vm:** a running macOS guest (`needs-vm`).
- **host:** a setup on the maintainer's Mac (`needs-host`):
  - H1 is a team-signed build (`WB_TEAM_ID`), #276.
  - H2 is a macOS 27 host. The dedicated Mac already runs macOS 27.2
    (X30-installer-trust), so it has no issue.
  - H3 is a fixture VM bundle for the tests of V1-M2 and V1-M3, #277.
  - H4 is the `wbtest` account with a model token, #716.
  - H5 is the attended notification permission, #717.

A VM package holds only integration and conformance code. Its logic
goes in an `any` or `mac` package, which runs in parallel, so the
`vm` lane stays short. Until #413 gives VM tests a bundle root of
their own, a `wb-hostd` VM test needs the real `<data>` directory, so
it runs on the maintainer's Mac.

## How work is cut

A change gets its own issue when it changes a `.proto` file or an
`internal/platform` interface, belongs to another component, moves
where a `SEC*` control is enforced, changes lane, needs a maintainer
decision, could change a spec by measurement, or is more than about
300 lines. A review follow-up in the same spec section with no new
decision is folded into the component's next package. Defaults,
buffer sizes, retry delays and wording are decided in code with a
test (`docs/agents/working.md`, "Follow-ups").

Contracts land with the first package that uses them, as its first
commit: the VM provider and hello in M2.1, the terminal stream in
M2.12, the hand-off in M3.4, the download records in IMG.1a, the
provisioning operations in IMG.5a, the agent interface
(`internal/agent`) in IMG.7a, sessions in M4.6a and the git transport
in M4.2a.

Until M3.11 is merged in V1-M6, `wb-hostd` starts each program of
`wb-launcher`'s table itself, through `internal/hostd/launch`:
`wb-vmd` and its build and layer entries, `wb-diskfmt`,
`wb-download`, `wb-build-ssh`, `wb-guestadmin` and the `wb-git`
entries. Each program is confined by its own Seatbelt profile from its
first package. `wb-hostd` has no profile before M6.1, so no control is
weaker in the meantime.

## V1-M2-walking-skeleton

No guest network device, and a fixture VM bundle (H3, #277) instead
of a built image. H1 is #276.

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M2.1 | Contracts and buf code generation: `vmd.v1`, `guest.v1` hello | S04-00, S10-01; NFR07 | | any | #159 |
| M2.2 | Stamped build version, `Wraith Box.app`, helper bundles, entitlement check, `wb setup` install | S10-02, S17-01, S17-02, S04-08 "Version skew"; C12 | M2.1 | mac, H1 | #174, #278, #279, #280 |
| M2.3 | Host plumbing: paths, peer-checked local IPC with a version handshake, one-descriptor channels, Seatbelt trampoline and deny probe | S12-01, S04-02, S04-03; SEC12 | M2.1 | mac | #281, #282 |
| M2.4 | `wb-vmd` device-set check, with the guest tools disk | S04-02, S06-06 "Devices"; SEC02, SEC05; C11 | M2.1 | mac | #160 |
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
| M3.1 | Packet path: datagram link endpoint, link filter, DHCP, caps, TCP rules | S07-02; SEC05; C01 | M2.3 | any | #482, #483 |
| M3.2 | DNS: pool, name form, local and negative answers, hold, wildcard budget, background list | S07-03, S07-04; C01 | M3.1 | any | #484, #485, #486 (DNS over TCP, #487, in V1-M6), folded: #124 (no leading or trailing hyphen in a label) |
| M3.3 | Policy loader and effective policy: OpenShell subset, bounded YAML, modes, per-VM union | S09-01, S09-02, S07-01; SEC09; C03 | | any | #488, #489 (the per-VM union, #490, in V1-M5), #259 (refuse rules that allow the OAuth token paths, in #489 and #490) |
| M3.4 | Hand-off, name mapping, accounting records (a 4 KiB record needs larger socket buffers than the 2 KiB macOS default) | S07-05, S07-06, S04-07, S04-08; SEC08; C02, C11 | M3.1 to M3.3 | mac | #491 to #494, folded: #172 (ordering test, macOS only), #179 (malformed report rule, cross-VM restart case) |
| M3.5 | Stream checks: first bytes, SNI binding, upstream address rule, pass and raw TCP, tunnels | S07-07, S07-15, S07-16; SEC05; C02 | M3.4 | any | #495, #496 |
| M3.6 | HTTP termination: h1 and h2, request form, caps, transfer coding, guest credential and response header removal | S07-08; SEC04; C02, C05 | M3.5 | any | #497, #498 (HTTP/2, #499, in V1-M6; credential removal, #500, in V1-M4), folded: #246 (rule for a client that rejects the inspected leaf) |
| M3.7 | HTTP policy engine and built-in profiles, parts, GraphQL filter (allowlist starts empty) | S07-11, S07-07; SEC06 | M3.3 | any | #501 (the GraphQL filter, #502, in V1-M4), folded: #206 (closed list of what audit mode relaxes, `audit-allowed` rule) |
| M3.8 | Secret store shim and `wb-credset` | S15-02, S15-03, S12-03; SEC12; C05 | M2.2, H1 | mac, host | #503, #505 (`wb-credset` and `wb cred`, #504 and #514, in V1-M4) |
| M3.9 | Inspection CA: Secure Enclave key, leaves, rotation, removal list, `wb vm reset-ca`, guest trust | S09-06, S09-07, S09-08; C02 | M3.8 | any, host, vm | #506 to #510 |
| M3.10 | Audit writer and event channel: OCSF, budgets, free space, a failed write stops the VM; the records V1-M2 only logs (data disk reset, git checks) | S09-11, S04-08; SEC10; C11 | M2.7 | any | #476, #477, folded: #177 (bound on the event retry queue) |
| M3.11 | Supervision and `wb-launcher` | S04-03, S04-07; SEC13; C11 | M2.3 | mac | moved to V1-M6: #478, #479, #480, #370 (the `wb-diskfmt` kill request, S04-04) |
| M3.12 | Egress integration: the NIC in the device set, a `wb-netd` per VM, flow reset on restore | S04-02; SEC05, SEC10, SEC11, FR08; C01, C02 | M3.1 to M3.10, H1 | vm, host | #481, #511, #512, #513 |

Made smaller on 2026-10-31, with the parts moved to the milestone
whose control or user they serve. Each fails closed until it is merged:

- To V1-M4: `wb-credset` and `wb cred` (#504, #514), next to M4.10,
  credential removal (#500, SEC04-no-guest-secrets), and the GraphQL
  filter (#502, SEC06-repo-writes). Until then GraphQL requests on
  built-in hosts are refused.
- To V1-M5: the per-VM union, modes and limits (#490), next to the
  boundary check. Until then a VM's policy is the built-ins and the
  user's global file, fixed at its start (#489).
- To V1-M6: M3.11, supervision and `wb-launcher` (#478 to #480,
  #370), next to the `wb-hostd` profile (M6.1). Until then `wb-hostd`
  starts its programs itself, as in V1-M2, and a daemon that exits
  stops its VMs instead of restarting. HTTP/2 (#499), with `http/1.1`
  the only ALPN offered until then, and DNS over TCP (#487), refused
  until then.

## V1-M7-image-build

Cut into issues on 2026-11-10. The maintainer's decisions of that day:

- Homebrew comes from a built-in default recipe, not from the base
  image (#150). The base holds macOS, `wb-guestd` and the Command Line
  Tools.
- Only `wb vm reset-system <vm>` replaces a system disk for
  break-glass, recovery and updates (IMG.4e). A disk the admin path
  marked is still replaced at its next start (S15-05-admin-path).
- A system disk with no image record, such as a fixture bundle, is
  refused sessions once IMG.4c is merged. So the VM tests of V1-M4 run
  on a sealed image.
- `wb-download` lowers its own file-size limit before it reads any
  input (IMG.1a). Ctrl-C cancels `wb image build` and keeps the partly
  downloaded restore image (IMG.2d).
- The terminal database is compiled on the host and installed in the
  guest (IMG.5c). The seal skips and logs a measured list of the paths
  macOS's privacy controls protect, and any other unreadable path is a
  finding (IMG.5d).

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| IMG.0 | Spec changes before the build: `wb-build-ssh` on Go's SSH library, macOS 27 for the build, the base layer and recipes, Claude Code's release and top layer, the Xcode variant, the secret scanner | S04-00, S04-05, S15-04, NFR04, S06-01, S06-02, S14-01, S17-04, S06-04 | | any | #165, #143, #150, #220, #194, #224, #306 |
| SCAN | Secret scanner: `internal/secretscan` over gitleaks' vendored rules, with its pin check and Renovate manager | S06-02, S08-01, S14-03, S10; SEC04 | #306 | any | #685 |
| IMG.1 | Downloads: `wb-download`, catalog, resume, hash, records, the Claude Code runs, the Command Line Tools entry | S17-03, S17-04; SEC10; C12 | M3.10, and #150 for IMG.1d | any, mac | #650 to #653 |
| IMG.2 | `wb-vmd-install`, build bundle, sealed image, slot lending, `wb image build`, the restore image's own version | S06-02, S04-04, S06-07; SEC15; C11 | IMG.1 for IMG.2b, and #618 for IMG.2e | vm, host | #654 to #658, after #262 (X30-installer-trust) |
| IMG.3 | Build network (one inbound port 22, refused for session VMs) and `wb-build-ssh` | S04-05, S15-04, S07-18 | M3.1, and IMG.2b and #629 for IMG.3c | any, vm | #659 to #661 |
| IMG.4 | `wb-guestadmin`: build, rotation, cleanup, first-contact gate, disk replacement, `wb vm reset-system`, `wb image use`, user recreation | S15-03, S15-04, S06-03; SEC04, SEC12; C09 | IMG.2, IMG.3b, M3.8 (#505), #539, #614 | any, vm, host | #662 to #666, folded: #154, #155, #142 |
| IMG.5 | Named provisioning operations, `wb-guestroot`'s install, terminfo for Ghostty, kitty, WezTerm, Alacritty and foot, the sealing scan | S06-02; SEC04 | IMG.4d, SCAN, #614 | vm | #667 to #671, folded: #203 and the image half of #474 |
| IMG.6 | Base layer: Command Line Tools from the host-fetched package, the toolchain user and prefix, the npm configuration; Homebrew from the built-in recipe; named images from recipes | S06-01, X20-shared-homebrew; FR06; C09 | #150, #180, #565, IMG.5, IMG.7b, IMG.7d, M3.12 (#513) | any, vm, host | #672 to #675 |
| IMG.7 | Claude Code in its own top layer: `wb-release-check`, `internal/agent`, install with the managed `CLAUDE.md`, `wb-vmd-layer`, `wb image list` | S14-01, S14-04, S17-04; FR01 | IMG.1c, IMG.2b, IMG.4d, IMG.5 | any, vm, host | #676 to #680, folded: #607 |
| IMG.8 | Xcode variant from a host-built read-only disk image | S06-01, S06-14; FR06 | #624, IMG.6c, IMG.7c | any, vm, host | #681 to #684 |

Also in V1-M7: the admin path (#614, B448 option A, no Full Disk
Access), the guest kernel panic found in X31-admin-channel (#615), the
spike on restore image downgrades, X33-restore-downgrade (#618), the data
mount's in-guest checks (#420, after #614), the user docs of the V1
limits (#611) and the measurement of the Xcode export (#624).

## V1-M4-claude-end-to-end

Cut into issues on 2026-11-10. The first prompt comes first: M4.6a,
M4.10, M4.12a and M4.12b, before the git packages. Until M4.7b is merged,
`wb claude` runs in the project user's home and says it returns no
work. The VM parts run on a sealed image from IMG.7e (see V1-M7).

The maintainer's decisions of that day:

- A `wb-git` request holds the session ID and the protocol version,
  and a `wb-git-landing` entry runs the landing maintenance (#72).
- A session's work is the one ref `refs/heads/wb/<id>/work`, landed as
  the host branch `wb/<id>` (#137).
- The model credential has two fixed binding names, one per kind.
  Both stored or neither refuses the session start (M4.10a).
- Placeholders (M4.10a) don't wait for #253. Injection (M4.10b) does.

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M4.0 | Spec changes: host git hardening, the `--isolated` home, `wb land` of the commit `wb diff` showed | S08-01 to S08-06, S06-04, S06-08, T05 | | any | #72 (folded: #130, #152), #128, #137 |
| M4.1 | `wb-git` shim and its entries, export lock, `landing.git`, `export.git` writer, project settings | S08-02, S08-07, S04-06, S09-03; SEC12 | #72, M4.6a. M4.1c also M4.12a | mac | #686 to #688 |
| M4.2 | Transport: guest request contract, `upload-pack`, `git-remote-wb`, `wb-guestd` relay with uid attribution, clone, the withheld-URL records, fsync settings | S08-01, S04-02, S06-05 "Durability"; SEC02, SEC08; C08 | M4.1, M4.6a. M4.2c also IMG.4e, IMG.6a and IMG.7e | any, vm | #689 to #691, folded: the git part of #231 |
| M4.3 | Push gate: ref filter, receive-pack settings, size cap, bounds, the push ledger | S08-03, S08-05; SEC06; C08 | M4.1, M4.2a, #72 | any | #692, #693, folded: #71 |
| M4.4 | Pack scanner and pre-receive check, with a differential test against `index-pack` | S08-04, X26, X28; SEC13; C08 | M4.3b for the wiring only | any | #694, #695 |
| M4.5 | Risky-path flagger, `wb diff`, `wb land`, `wb discard`, fetch checks, returned work | S08-06, S08-09; FR03, SEC03 | M4.3b, M4.6a, M4.6b, M4.7b, #137 | mac, vm | #696 to #698, #427, folded: #70 |
| M4.6 | Session lifecycle, host: contract, states, admission, link drops, deadlines, `wb sessions`, `wb status` and the health table | S06-09, S06-04, S06-11; FR05 | M2.12 | any | #699 to #701 |
| M4.7 | Session lifecycle, guest: reports, worktree, history link, WIP push, leftovers, the launchd domain check | S06-09, S06-10, S06-11, S13-01; C09 | M4.6, M4.12a, M4.2c, #128 | any, vm, host | #702 to #704, #196 (launchd domain check, measured here) |
| M4.8 | Carry-in: collect, scan, apply | S08-01; FR04; C08 | SCAN, M4.12a | mac, vm | #705, #706 |
| M4.9 | `~/.claude` carry-in: filter, caps, write as the user | S14-02, S14-03; FR13; C09 | SCAN, M4.8 | any, vm | #707, #708 |
| M4.10 | Bindings and model credential: placeholders, injection, streaming scan; long-lived token and API key | S09-05, S07-08, S07-09; SEC04; C05, C06 | M4.6a, IMG.7a, #512, and #253, #498, #500 and #567 for M4.10b | any | #709, #710, #253 (folded: #226), #500, #504, #514, #567 |
| M4.11 | Terminal records and session summary | S09-12, S16-03, S05-01; SEC10 | M2.11, M3.10, M4.6, and M4.3b and M4.5b for M4.11b | any | #711, #712, folded: #199 |
| M4.12 | `wb claude`: agent interface, environment, job control, the first prompt, C06 end to end | S14-00, S06-08, S05-01, S05-02; FR01, FR13 | M4.10, M2.12, #504. M4.12b also #513, H4 and IMG.7e. M4.12c the rest of V1-M4 | vm, host | #713 to #715, folded: #135 and the Claude Code part of #231 |

Also in V1-M4: the GraphQL filter (#502), `wb cred` records (#514),
the project resolution checks (#557, #616) and a test-only bundle root
for VM tests (#413). H4 is #716.

## V1-M5-approvals

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M5.1 | Approval flow, limits, expiry, name display, re-run during a hold, `wb approve` and `wb deny` | S09-10, S07-04, S07-17; FR09, SEC14 | M3.2 | any | folded: #173 (timing test of the policy order) |
| M5.2 | Approved rule, risk check, joined approvals | S09-09, S07-01 | M3.3 | any | |
| M5.3 | Boundary check: prover vendoring, `wb-prover`, projection, cache | S09-04, S10-01; SEC09 | M3.3 | mac | folded: #207 (extension check asserts the audit projection) |
| M5.4 | Notification helper and its `wb-hostd` contract, spec drafted first | S12-01, S09-09 "Delivery", S04-07 | M5.1, H5 | host | H5 is #717 |
| M5.5 | Learn mode, `wb learn report` | S07-17; FR10 | M5.1 | any | |
| M5.6 | `.wraithbox/`, `wb trust` and `wb untrust` (the `wb project` commands are #294) | S09-03, S05-03, S05-04; SEC09 | M5.3 | any | |
| M5.7 | Dependency gate: mappers, metadata filter, OSV, how the gate authenticates its own fetch | S07-12, S07-13; SEC07; C07 | M3.6 | any | #193 (branch and commit `.info` queries) |
| M5.8 | Go clock: checksum database, tiles, heads, reset | S07-13, S07-14; SEC07 | M5.7 | any | |
| M5.9 | General bindings, git credential bindings users declare, anonymous credentials, part switches, policy changes while running | S07-10, S07-11, S07-01; SEC06 | M4.10 | any | folded: #153 (a binding names its token endpoint), #208 (a confirmed remote change widens policy) |
| M5.10 | `wb policy`, `wb allow`, `wb audit` | S05-03, S09-02, S09-11; FR15, NFR06 | M5.2, M5.3 | any | folded: #207 (`wb policy explain` shows the audit projection) |
| M5.11 | Toolchain reconcile | S06-01, X20-shared-homebrew; FR07; C09 | IMG.6, M3.12 | any, vm | #180 moved to IMG.6 on 2026-11-10 |
| M5.12 | Approval and gate conformance | C03, C07 | the rest of V1-M5 | vm, host | |

Also in V1-M5: the per-VM union, modes and limits (#490), moved from
V1-M3. Until M5.9, a guest push to a forge fails to authenticate,
because only the bindings a test supplies exist (M4.10b). V1-M4's exit
criterion doesn't need that push.

## V1-M6-release-gate

| # | Package | Specs and IDs | Deps | Lane | Issues |
|---|---|---|---|---|---|
| M3.11 | Supervision and `wb-launcher`, moved from V1-M3 | S04-03, S04-07; SEC13; C11 | M2.3 | mac | #478, #479, #480, #370 (the `wb-diskfmt` kill request, S04-04) |
| M6.1 | `wb-hostd` profile, rules and probes | S04-06; SEC12 | M3.11, and the programs of V1-M7 and V1-M4 it starts | mac | |
| M6.2 | Confinement self-checks for every program, `wb-vmd` ablation of graphics and input | S04-03, S06-06 | M3.11, M6.1 | mac, vm | #318 (ablation) |
| M6.3 | Session Seatbelt profiles, the Xcode and simulator profile, safehouse manifest | S13-02 | M4.7, M4.12, IMG.8 | vm | #90, #196 (simulator profile) |
| M6.4 | Warm start and suspend: clones, permanence, idle; reconcile SEC15-no-guest-disk-mount's rule on clones with S06-06's restore from clones | S06-06, S06-09; NFR03 | M3.12, M4.6, IMG.4e | vm | |
| M6.5 | Bounds sweep against the S04-08 table, per-VM caps | SEC13 | V1-M4, V1-M5 | any | |
| M6.6 | Upgrades, migrations, profile expiry, `wb setup --remove`, release signing check | S17-05, S12-03, S15-01 | M2.2, M3.8, M6.1 | mac, host | #118 |
| M6.7 | Benchmarks and measured defaults | S11; NFR01 to NFR03 | M6.4 | vm | #148, #175 |
| M6.8 | Full conformance run and gap sweep | C00-index | the rest of V1 | vm, host | |

Also in V1-M6: HTTP/2 (#499) and DNS over TCP (#487), moved from
V1-M3, and mutation testing and deeper fuzz corpora for the `wb-netd`
and `wb-proxyd` parsers (#548, after #498 and #499).

## Critical path

The `vm` lane, one VM builder at a time. The parts in order:

- V1-M3: #513 (H1).
- V1-M7: IMG.2a, IMG.2d, IMG.2e, IMG.3c, IMG.4b to IMG.4e, IMG.5a to
  IMG.5e, IMG.7e, IMG.6d and IMG.8d. IMG.2, IMG.3c, IMG.4c to IMG.4e
  and the VM runs of IMG.6 to IMG.8 also need the maintainer's Mac.
- V1-M4: M4.2c, M4.12b (H4), M4.7c, M4.5c, M4.8b, M4.9b and M4.12c.
  They run on a sealed image from IMG.7e, because IMG.4c refuses a
  system disk with no image record, such as the fixture bundles.
- V1-M5 and V1-M6: M5.12, M6.3, M6.4, M6.7 and M6.8.

The VM parts of M3.9, M5.11 and M6.2 run in the same lane, between the
packages they depend on. The admin path (#614), the guest kernel panic
(#615) and the data mount's in-guest checks (#420) are VM work of
V1-M7 too.

These parts have no open blocker and run beside the lane now: IMG.1a,
IMG.2c, IMG.3a, IMG.4a, IMG.7a, IMG.7c, M4.4a, M4.5a and M4.6a. SCAN
follows #306, IMG.8a follows #624, and M4.1a and M4.3a follow #72.
IMG.2a can start in the `vm` lane at once, with a restore image from
the spike cache.

The maintainer's decisions reach the lane before it does: the spec
changes of IMG.0 come first in V1-M7-image-build, and the
notification helper contract is drafted with M5.4.
