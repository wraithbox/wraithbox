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
filed, and `mise run doc:index` adds its row. The workflow is in `docs/agents/planning.md`.

| ID | Description | Status |
|----|-------------|--------|
| X01-model-credential | Model credential via the proxy | Open |
| X02-warm-start | Warm start | Answered 2026-10-04: yes, with conditions. Awaiting decision on idle-while-locked |
| X03-network-path | Network path | Open |
| X04-tls-inspection | Inspection compatibility | Open |
| X05-fs-benchmark | Filesystem benchmark | Open |
| X06-guest-xcode | Guest users and Xcode | Open |
| X07-git-round-trip | Git round trip | Answered: yes, with conditions |
| X08-data-disk | Data disk for homes | Open |
| X09-keychain-unsigned | Keychain access without a signing identity | Open |
| X10-linux-hypervisor | Linux VMM and packet transport | Open |
| X11-windows-host | Windows host through HCS | Open |
| X12-wsl-channel | WSL client channel | Open |
| X13-windows-guests | Windows guests on a macOS host | Open |
| X14-flow-attribution | Guest flow attribution | Open |
| X15-endpoint-security | Endpoint Security entitlement | Open |
| X16-openshell-linux | OpenShell for Linux guests | Open |
| X17-image-build | Unattended image build | Open |
| X18-vsock-handoff | Host-guest socket and descriptor hand-off | Open |
| X19-terminal-filter | Terminal stream filtering | Answered |
| X20-shared-homebrew | Homebrew with more than one project user | Open |
| X21-dep-gate-registries | Dependency gate on real registries | Answered 2026-10-04: yes, with conditions |
| X22-no-guest-credentials | Clients without guest credentials | Open |
| X23-sandboxed-daemons | Self-sandboxed Go daemons on macOS | Answered: yes with conditions (`wb-proxyd` Keychain part in I61) |
| X24-openshell-artifacts | OpenShell artifacts | Open |

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
- **X02-warm-start: Warm start.** Measure time to restore a macOS guest from saved
  state with a vsock device and a file-handle network attachment.
  Can a saved state be reused (cloned with its disks) more than once?
  Answered yes, with conditions, in the result page X02-warm-start.
- **X03-network-path: Network path.** A macOS guest boots on the file-handle
  attachment with the gVisor stack: gets a DHCP lease, resolves only
  allowlisted names, reaches only the proxy. Confirm no entitlement
  beyond virtualization is needed. Measure throughput.
- **X04-tls-inspection: Inspection compatibility.** Go-based, Swift-based, and Xcode
  clients accept the name-constrained CA when it is trusted only in the
  guest. List clients that pin certificates.
- **X05-fs-benchmark: Filesystem benchmark.** Data disk with default versus relaxed
  write-through settings, against the host, on the NFR02-fs-speed workloads.
- **X06-guest-xcode: Guest users and Xcode.** Can builds and simulators run for a
  guest user without a GUI login? If not, Xcode sessions go to the
  isolated VM's console user.
- **X07-git-round-trip: Git round trip.** Remote helper over vsock, read-only
  `upload-pack`, restricted `receive-pack` into the landing repository,
  flagging of risky paths. Answered yes, with conditions, in the result
  page X07-git-round-trip.
- **X08-data-disk: Data disk for homes.** Guest user homes on a second disk,
  surviving a system-disk replacement.
- **X09-keychain-unsigned: Keychain access without a signing identity.** Confirm the
  fallback access-control model from S09-policy-credentials-audit and the Secure Enclave
  key creation for an ad-hoc signed binary.

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
  first prompt. Compare with a Hyper-V socket from the WSL VM.
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
  Homebrew, SwiftPM, and `xcodebuild`? How do labels reach `wb-netd`
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
  VMM chosen in X10-linux-hypervisor? The check covers SEC01-separate-kernel to SEC14-no-fake-approvals, a policy shared with
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
- **X18-vsock-handoff: Host-guest socket and descriptor hand-off.** Can `wb-guestd`,
  written in Go, open `AF_VSOCK` sockets in a macOS guest? Do the
  file descriptors of a vsock connection and of the file-handle
  network attachment keep working after `wb-vmd` passes them to
  another process (`SCM_RIGHTS`), and across save and restore? S04-architecture rests on the answer ("descriptors, not bytes").
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
  time for both.
- **X21-dep-gate-registries: Dependency gate on real registries.** Can `wb-proxyd` map a
  download to package, version and publish time for npm, PyPI, the
  Go module proxy, crates.io and Homebrew? Does removing too-young
  versions from metadata let resolvers pick an older version, where
  refusing the download fails the whole install? Measure failure
  rates with a 7-day minimum age on real projects, and the time OSV
  lookups add to an `npm ci` with a large lockfile. Answered: yes, with
  conditions. Filter the metadata and keep refusing the download, use
  the checksum database as Go's clock, and leave Homebrew ungated
  (X21-dep-gate-registries).
- **X22-no-guest-credentials: Clients without guest credentials.** Which common clients
  (Homebrew against `ghcr.io`, git, `gh`, npm, pip, uv, SwiftPM, Go,
  cargo, Claude Code) break when `wb-proxyd` removes every
  guest-supplied `Authorization` header and cookie on inspected
  hosts? Registries such as `ghcr.io` hand out anonymous tokens that the
  client must send back. Find a rule for those that keeps SEC04-no-guest-secrets and SEC06-repo-writes.
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
