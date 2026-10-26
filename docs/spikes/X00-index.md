# X00 - Spikes

**Purpose:** The open questions that must be answered by a throwaway
prototype before work builds on them, in the order they are taken up.

Each spike is a throwaway prototype with a yes/no answer, and its result
is written up before dependent work starts.

Each spike is one GitHub issue titled `X<NN>-<slug>: <name>`. Its
throwaway code stays on a `spike/x<NN>-<slug>` branch that is kept and
never merged. The written result is `docs/spikes/X<NN>-<slug>.md`, next
to this index, and is merged together with any spec change the answer
forces. A new open question gets the next free number here before it is
filed, and `mise run doc:index` adds its row. The workflow is in `docs/agents/spikes.md`.

| ID | Description | Status |
|----|-------------|--------|
| X01-model-credential | Model credential via the proxy | Answered 2026-10-12: yes for a long-lived token. Decided 2026-10-12 (I60): A2, the model only with the event log denied by the host, and the long-lived token as a supported mode for V1. Answered 2026-10-15 (I15): yes, with conditions, for the browser login. Decided 2026-10-15 (I15): not in V1, and no filter for the authorize URL |
| X02-warm-start | Warm start | Answered 2026-10-04: yes, with conditions. Decided 2026-10-04 (I16): an idle VM keeps running while the host is locked and is saved after unlock |
| X03-network-path | Network path | Answered 2026-10-05: yes, with conditions |
| X04-tls-inspection | Inspection compatibility | Answered 2026-10-08: no |
| X05-fs-benchmark | Filesystem benchmark | Answered 2026-10-05: no. NFR02-fs-speed is not met for `npm ci` with any disk setting, and the data disk uses full sync. Decided 2026-10-06 (I19): NFR02-fs-speed is a goal outside the release gate |
| X06-guest-xcode | Guest users and Xcode | Answered 2026-10-08: yes, with conditions |
| X07-git-round-trip | Git round trip | Answered: yes, with conditions |
| X08-data-disk | Data disk for homes | Answered 2026-10-12: yes, with conditions. Decided 2026-10-12 (I22): 1A, the host formats the data disk APFS, and 2A, in the `wb-diskfmt` shim. Follow-up answered 2026-10-26 (I228): the host-made volume is not encrypted, gives freed space back at each clean shutdown or cold boot while mounted `nobrowse`, and formats under the confined shim. Decision 3 (`nobrowse`) awaits the maintainer |
| X09-keychain-unsigned | Keychain access without a signing identity | Answered 2026-10-13: yes between updates, no across an update, and no permanent Secure Enclave key. Decided 2026-10-15 (I23): 1D, with 1A until X29-dev-signing, and 2A |
| X10-linux-hypervisor | Linux VMM and packet transport | Open |
| X11-windows-host | Windows host through HCS | Open |
| X12-wsl-channel | WSL client channel | Open |
| X13-windows-guests | Windows guests on a macOS host | Open |
| X14-flow-attribution | Guest flow attribution | Open |
| X15-endpoint-security | Endpoint Security entitlement | Open |
| X16-openshell-linux | OpenShell for Linux guests | Open |
| X17-image-build | Unattended image build | Answered 2026-10-07: no |
| X18-vsock-handoff | Host-guest socket and descriptor hand-off | Answered 2026-10-05: yes, with conditions |
| X19-terminal-filter | Terminal stream filtering | Answered |
| X20-shared-homebrew | Homebrew with more than one project user | Answered 2026-10-07: yes, with conditions |
| X21-dep-gate-registries | Dependency gate on real registries | Answered 2026-10-04: yes, with conditions |
| X22-no-guest-credentials | Clients without guest credentials | Answered 2026-10-05: yes, with conditions |
| X23-sandboxed-daemons | Self-sandboxed Go daemons on macOS | Answered: yes with conditions. `wb-proxyd` Keychain part answered 2026-10-15 (I61): yes |
| X24-openshell-artifacts | OpenShell artifacts | Answered 2026-10-05: yes, with conditions |
| X25-vmd-sandbox | Self-sandboxed `wb-vmd` | Answered 2026-10-06: yes, with conditions. Only `wb-vmd`'s code keeps a NAT network from the guest. Decided 2026-10-12 (I64): 1A, one `wb-vmd` per VM, and 2A, the `wb-vmd-install` entry |
| X26-pre-receive-check | Push checks before the quarantine lands | Answered 2026-10-05: yes, with conditions |
| X27-vsock-confinement | vsock confinement of project users | Answered 2026-10-06: yes, with conditions |
| X28-git-alloc-limit | git's allocation cap against delta bombs | Answered 2026-10-06: yes, with conditions |
| X29-dev-signing | Keychain access group for development builds | Answered 2026-10-15: yes, with conditions. Decided 2026-10-15 (I248): no Keychain backend for a build without a team signature |
| X30-installer-trust | Installer refusal of a changed restore image | Open |

## Spikes

### macOS host, macOS guest (V1)

- **X01-model-credential: Model credential via the proxy.** Does Claude Code work with a
  placeholder credential while `wb-proxyd` injects the real one, for
  each supported authentication mode, including token refresh? If not,
  requirement SEC04-no-guest-secrets needs a documented exception and the threat model changes.
  OpenShell's Claude Code provider shows that an API key works. A Claude
  subscription login is the open part: OpenShell holds back from it
  until Anthropic approves an OAuth client identity for third-party
  tools ([OpenShell issue 3331](https://github.com/NVIDIA/OpenShell/issues/3331)), and a login shared with the host's
  Claude Code would race it on token refresh. The answer must say
  which login Wraith Box owns and how it is refreshed.
  Answered 2026-10-12 in the result page X01-model-credential: yes
  for a long-lived token from `claude setup-token`, given to Claude
  Code as a placeholder in `CLAUDE_CODE_OAUTH_TOKEN`, and awaiting
  decision on four points. API key mode wasn't run, and moved to
  I253. Answered 2026-10-15 for the browser login (I15): yes, with
  conditions. Claude Code works with placeholder access and refresh
  tokens when the host holds the real pair and makes every refresh,
  which rotates the refresh token each time. A login lasts about 30 days.
  Decided 2026-10-15 (I15): not in V1, and no terminal filter for the
  authorize URL (B15-browser-login).
- **X02-warm-start: Warm start.** Measure time to restore a macOS guest from saved
  state with a vsock device and a file-handle network attachment.
  Can a saved state be reused (cloned with its disks) more than once?
  Answered yes, with conditions, in the result page X02-warm-start.
- **X03-network-path: Network path.** A macOS guest boots on the file-handle
  attachment with the gVisor stack: gets a DHCP lease, resolves only
  allowlisted names, reaches only the proxy. Confirm no entitlement
  beyond virtualization is needed. Measure throughput. Answered yes,
  with conditions, in the result page X03-network-path.
- **X04-tls-inspection: Inspection compatibility.** Go-based, Swift-based, and Xcode
  clients accept a CA that only the guest trusts. List
  clients that pin certificates, and which clients reread trust
  without a restart, which sets the CA overlap
  (S09-07-ca-certificates, "Lifetimes"). The CA has no name
  constraints since I39. Answered no, in the result page
  X04-tls-inspection: every client that takes a CA file or variable
  accepts the CA and none pins, but `wb-guestd` can't add it to a macOS
  guest's system trust store, so URLSession clients reject inspected
  hosts and reach only pass hosts (decided on I185). Of the clients tested, only Python's `requests` rereads trust while it runs.
- **X05-fs-benchmark: Filesystem benchmark.** Data disk with default versus relaxed
  write-through settings, against the host, on the NFR02-fs-speed workloads.
  Answered no in the result page X05-fs-benchmark: `npm ci` stays under
  80% of the host with every disk setting, and the data disk uses full
  sync. Decided on I19: NFR02-fs-speed is a goal outside the release gate.
- **X06-guest-xcode: Guest users and Xcode.** Can builds and simulators run for a
  guest user without a GUI login? If not, Xcode sessions go to the
  isolated VM's console user. Answered yes, with conditions, in the
  result page X06-guest-xcode: everything runs without one except
  `xcodebuild test` on macOS, SwiftPM packages included, and launching
  a macOS app.
- **X07-git-round-trip: Git round trip.** Remote helper over vsock, read-only
  `upload-pack`, restricted `receive-pack` into the landing repository,
  flagging of risky paths. Answered yes, with conditions, in the result
  page X07-git-round-trip.
- **X08-data-disk: Data disk for homes.** Guest user homes on a second disk,
  surviving a system-disk replacement. Answered yes, with conditions,
  in the result page X08-data-disk: a volume the guest formats APFS is
  lost with its system disk, and one the host formats APFS before the
  first boot survives, with the users recreated from host state. Its
  follow-up (I228), in the same page, measured the host-made volume:
  it is not encrypted, `wb-diskfmt` formats it confined, and it gives
  freed space back at each clean shutdown or cold boot while mounted
  `nobrowse`.
- **X09-keychain-unsigned: Keychain access without a signing identity.** Confirm the
  fallback access-control model from S09-policy-credentials-audit and the Secure Enclave
  key creation for an ad-hoc signed binary. Answered in the result page
  X09-keychain-unsigned: the access-control list pins each binary by
  its cdhash, and after an update every item asks for the keychain
  file's password. A permanent Secure Enclave key needs an entitlement,
  and an ephemeral key's token object ID restores it in a later process.
  Decided on I23: development builds signed with the developer's own
  Apple Development certificate, after X29-dev-signing, failing closed
  after an update until then, and the CA key wrapped in a store item.
  X29-dev-signing then answered yes. I248 dropped the ad-hoc keychain
  file, and an ad-hoc signed build has no Keychain backend.

### Other platforms (S12-platforms)

These do not block v1, but each must be answered before work on its
platform starts.

- **X10-linux-hypervisor: Linux VMM and packet transport.** Pick a permissively licensed
  VMM over KVM that supports vsock, save/restore, copy-on-write disks,
  and a virtual TPM (for Windows guests), and a way to deliver guest
  Ethernet frames to `wb-netd` as an unprivileged user (no tap device,
  no `CAP_NET_ADMIN`, no unprivileged user namespaces). Measure NFR01-startup and
  network throughput.
- **X11-windows-host: Windows host through HCS.** On Windows 11 **Home** with only
  the Virtual Machine Platform feature: can a standard user create,
  start, save, and restore a VM through HCS? Is a Hyper-V socket
  available to it? How do guest frames reach `wb-netd` without a
  virtual switch that bridges to the host network? Can a Windows 11
  guest get a virtual TPM and Secure Boot? If any answer needs
  administrator rights, write up the smallest privileged component
  that would do (S12-platforms, SEC12-least-privilege).
- **X12-wsl-channel: WSL client channel.** `wb` in WSL 2 relaying through
  `wb.exe relay` over interop standard streams: terminal fidelity,
  window resizing, throughput of the git transport, latency to the
  first prompt. Compare with a Hyper-V socket from the WSL VM. For the
  git fetches of S08-10-wsl, also measure how
  `wsl.exe --exec` passes arguments that hold spaces and quotes, and
  whether `GIT_PROTOCOL=version=2` reaches the relayed `upload-pack`
  in both directions. Measure the MSYS path conversion of the Git for
  Windows `sh` on those arguments, with and without
  `MSYS_NO_PATHCONV=1`. Try a placeholder path in a place where user
  processes can't create files, under `/proc/self/` on WSL and under the Windows system
  directory on Windows. Once it works, that is the stricter choice
  over a path in an empty directory that `wb` creates.
- **X13-windows-guests: Windows guests on a macOS host.** Can the Virtualization
  framework boot Windows 11 for Arm in a supported way, including TPM
  and Secure Boot requirements and usable display and network drivers?
  If not, Windows guests on macOS stay unsupported; a second VMM on
  macOS needs a spec change.

### Guest confinement (S13-guest-confinement)

These do not block v1: every `SEC*` control holds without S13-guest-confinement's
layers 3 and 4.

- **X14-flow-attribution: Guest flow attribution.** A Network Extension transparent
  proxy in a macOS guest (S13-guest-confinement): can it be approved during the
  image build without MDM? Does it see the flows of Claude Code, git,
  Homebrew, SwiftPM, and `xcodebuild`? Does each label carry the user
  ID of the process, so the host can narrow per project
  (T11-shared-vm-grants)? How do labels reach `wb-netd`
  (a header on each flow, or a side channel over vsock)? What happens
  to flows when guest root kills or unloads it, with System Integrity
  Protection on? Which Developer ID entitlement does it need?
- **X15-endpoint-security: Endpoint Security entitlement.** Request the Endpoint
  Security entitlement from Apple. Until it is granted, prototype the
  client in a development guest with System Integrity Protection off:
  exec authorization by cdhash and process ancestry for S13-guest-confinement's
  layer 4.

### Linux guests through OpenShell

Answered before X10-linux-hypervisor, because a yes may make X10-linux-hypervisor unnecessary.

- **X16-openshell-linux: OpenShell for Linux guests.** Could OpenShell's VM driver
  (libkrun, guest without a network device, egress over vsock) run
  Linux guests for Wraith Box on macOS and Linux hosts instead of a
  VMM chosen in X10-linux-hypervisor? The check covers SEC01-separate-kernel to SEC15-no-guest-disk-mount, a policy shared with
  macOS guests (S09-policy-credentials-audit), and NFR01-startup. It has no git round trip and no
  saved-state restore, so those are Wraith Box's to add. Its other drivers share a kernel between
  sandboxes (SEC01-separate-kernel) and are out of the question.

### Found during initial planning (V1)

These came out of the first read-through of the specs for an
implementation plan (`docs/implementation-plan.md`). Each tests an
assumption that v1 work would otherwise build on. They block v1.

- **X17-image-build: Unattended image build.** Can a macOS guest installed from
  an Apple restore image get past Setup Assistant, with `wb-guestd`
  running as a root LaunchDaemon, without SSH, scripted clicks, or
  MDM? The candidate is writing files to the new guest's data
  volume from the host at build time, before any untrusted code has
  run. Measure the build time from the restore image.
  Answered no, in the result page X17-image-build: a host without root
  can't write files launchd accepts, so the build installs `wb-guestd`
  with one SSH session on its first boot.
- **X18-vsock-handoff: Host-guest socket and descriptor hand-off.** Can `wb-guestd`,
  written in Go, open `AF_VSOCK` sockets in a macOS guest? Do the
  file descriptors of a vsock connection and of the file-handle
  network attachment keep working after `wb-vmd` passes them to
  another process (`SCM_RIGHTS`), and across save and restore? S04-architecture rests on the answer ("descriptors, not bytes").
  Answered yes, with conditions, in the result page X18-vsock-handoff.
  Decided on I111: NFR01-startup is a goal outside the release gate.
- **X19-terminal-filter: Terminal stream filtering.** Which terminal escape sequences
  does Claude Code emit, and can `wb` drop the ones that act on the
  host (clipboard writes, file transfer, terminal multiplexer control
  sequences) without visible damage in the common macOS terminals?
  The relay passes guest bytes to the host terminal emulator.
  Answered: yes, with conditions (X19-terminal-filter).
- **X20-shared-homebrew: Homebrew with more than one project user.** Can each project
  user in one guest get its declared Brewfile without a Homebrew
  prefix that another project user can write to (SEC08-proj-isolation)? Compare a
  prefix owned by `wb-guestd` that installs every declared Brewfile
  with per-user prefixes. Measure bottle availability and install
  time for both. Answered: yes, with conditions. A prefix owned by the
  toolchain user works when the manifest is read as formula names and
  only `wb-guestd` installs, and a prefix in each home builds too much
  from source (X20-shared-homebrew).
- **X21-dep-gate-registries: Dependency gate on real registries.** Can `wb-proxyd` map a
  download to package, version and publish time for npm, PyPI, the
  Go module proxy, crates.io and Homebrew? Does removing too-young
  versions from metadata let resolvers pick an older version, where
  refusing the download fails the whole install? Measure failure
  rates with a 7-day minimum age on real projects, and the time OSV
  lookups add to an `npm ci` with a large lockfile. Answered: yes, with
  conditions. Filter the metadata and keep refusing the download, use
  the checksum database as Go's clock, and leave Homebrew ungated
  (X21-dep-gate-registries). I76 measured the Go clock end to end on
  2026-10-08, and it held.
- **X22-no-guest-credentials: Clients without guest credentials.** Which common clients
  (Homebrew against `ghcr.io`, git, `gh`, npm, pip, uv, SwiftPM, Go,
  cargo, Claude Code) break when `wb-proxyd` removes every
  guest-supplied `Authorization` header and cookie on inspected
  hosts? Registries such as `ghcr.io` hand out anonymous tokens that the
  client must send back. Find a rule for those that keeps SEC04-no-guest-secrets and SEC06-repo-writes.
  Answered: yes, with conditions. Only Homebrew breaks, and a host-side
  anonymous binding for `ghcr.io` fixes it, so no guest credential is
  ever forwarded (X22-no-guest-credentials).
- **X23-sandboxed-daemons: Self-sandboxed Go daemons on macOS.** Can `wb-netd`,
  `wb-proxyd` and `wb-hostd` confine themselves at start with a
  sandbox profile, as S04-architecture says, while the Go runtime, inherited
  descriptors, and Keychain and Secure Enclave access in `wb-proxyd`
  keep working? Answered: yes with conditions, in
  `docs/spikes/X23-sandboxed-daemons.md`. The `wb-proxyd` Keychain and
  Secure Enclave part is I61.
- **X24-openshell-artifacts: OpenShell artifacts.** Do the OpenShell parts that S07-egress-gateway,
  S09-policy-credentials-audit and S10-tech-stack build on exist in the assumed form, under a permissive
  license, at a version that can be pinned: the `network_policies`
  schema version 1 (still valid with Wraith Box's extra top-level
  keys), a standalone `openshell-prover` binary and its reading of
  `binaries: /**`, the proposal risk check for one rule, and
  `supervisor_middleware.proto` (RFC 0009) used inside a Go process?
  The same check covers the OCSF 1.8 classes and the agent-safehouse
  Seatbelt profiles.
  Answered yes, with conditions, in the result page
  X24-openshell-artifacts: the extension keys are stripped before
  OpenShell's tooling sees a policy, and the risk check isn't in the
  prover binary.
- **X25-vmd-sandbox: Self-sandboxed `wb-vmd`.** Can `wb-vmd` confine
  itself at start with a Seatbelt profile that allows the
  Virtualization framework, its VM bundles under `<data>/vms` and
  nothing else, and still create, start, save and restore a macOS VM?
  If not, what does it need instead: App Sandbox with the
  virtualization entitlement, or no confinement and a spec change
  (S04-architecture, SEC12-least-privilege). Found by
  X23-sandboxed-daemons. Needs a VM. Answered yes, with conditions, in
  the result page X25-vmd-sandbox: a Seatbelt profile with the VM
  bundles, the Virtualization service and the framework's sandbox
  extensions works for install, boot, save and restore. It keeps
  shared directories out of the guest, but not a NAT network.
- **X26-pre-receive-check: Push checks before the quarantine lands.**
  Can a `pre-receive` check that Wraith Box owns, never the repository,
  refuse a push whose objects inflate past a cap, and repeat the ref
  checks git only runs in `update()`, so a refused push leaves nothing
  in `landing.git` (S08-workspace-and-git, SEC13-bounded-resources)?
  Found in the review of the X07-git-round-trip result. Answered yes,
  with conditions, in the result page X26-pre-receive-check: the check
  works, and a Go pack scanner in front of `receive-pack` bounds the
  memory git uses while it unpacks.
- **X27-vsock-confinement: vsock confinement of project users.** Can a
  non-root process in a macOS guest open `AF_VSOCK`, and bind or
  listen on `wb-guestd`'s port while `wb-guestd` is running or being
  restarted? Can the Seatbelt profile of S13-guest-confinement deny
  `AF_VSOCK` to project users? If a project user can bind the port,
  it can pose as `wb-guestd` and claim another project's identity
  (S04-architecture, SEC08-proj-isolation). Found while deciding
  B29-vsock-handoff. Needs a VM. Answered yes, with conditions, in the
  result page X27-vsock-confinement: a project user took port 1024
  while launchd restarted the daemon, ports below 1024 need guest
  root, and a Seatbelt rule on the socket domain denies `AF_VSOCK`.
- **X28-git-alloc-limit: git's allocation cap against delta bombs.**
  Does `GIT_ALLOC_LIMIT` in `receive-pack`'s environment make both host
  gits refuse each delta bomb of X26-pre-receive-check before
  allocating its result? If so, git bounds per-object memory and the
  Go pack scanner of S08-workspace-and-git shrinks to the per-push
  caps (SEC13-bounded-resources). Found after deciding B74-pre-receive-check.
  Answered yes, with conditions, in the result page
  X28-git-alloc-limit: git refuses each oversized allocation itself,
  and the scanner stays for the per-push total and the object count,
  because deltas under the cap still cost git CPU time.
- **X29-dev-signing: Keychain access group for development builds.**
  Can a developer's free personal team, an Apple ID signed in to Xcode
  without a paid membership, sign the helpers in `Wraith Box.app`
  (`wb-proxyd`, `wb-hostd`, `wb-vmd`) with a `keychain-access-groups`
  entitlement and a provisioning profile, so that each helper reads
  and writes items in the data protection keychain behind one access
  group, and keeps reading them after a rebuild signed by the same
  team, with no dialog? Found while deciding B23-keychain-unsigned
  (I23). Tracked in I248.
  Answered yes, with conditions, in the result page X29-dev-signing:
  each helper that holds an access group is a bundle of its own, and
  the developer builds once in the Xcode app. Decided on I248: an
  ad-hoc signed build has no Keychain backend, and the ad-hoc keychain
  file left the specs (I252).
- **X30-installer-trust: Installer refusal of a changed restore image.**
  Does the Virtualization framework's macOS installer, run by
  `wb-vmd-install`, refuse a restore image that Apple didn't sign, or
  one with a changed or replaced component, and does the install
  contact Apple while `wb-vmd-install`'s profile denies the network?
  S17-04-restore-and-release checks a restore image only against the catalog's
  SHA-1, which guards against a damaged file, not against a
  compromised `wb-download`. The trust base of T00-index and
  S17-04-restore-and-release rests on the installer's own check
  (SEC12-least-privilege, SEC08-proj-isolation). Found while deciding
  B49-install-upgrade. Tracked in I262.
