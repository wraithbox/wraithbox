# 011 - Verification and Open Questions

**Purpose:** How the requirements are proven, and which assumptions must
be tested before building on them.

## Test layers

- **Unit tests.** Go `go test -race`, table-driven; Swift Testing.
- **Fuzzing.** Go native fuzz targets for every parser that sees
  guest-controlled bytes: Ethernet/IP/TCP handling at the link endpoint,
  DHCP, DNS, TLS ClientHello parsing, HTTP/1.1 and HTTP/2 request
  handling, policy matching, the git transport framing, every vsock RPC
  handler in `wb-hostd`, network policy YAML, and the guest flow labels
  of spec 013 (Swift: property-based tests where fuzzing is
  impractical).
- **Conformance suite.** A set of adversarial checks run *inside a guest*
  against a real `wb-netd`/`wb-proxyd`. Each must fail safely, and the
  suite is a release gate for every host/guest combination in the
  platform matrix (spec 012); a combination is not supported until it
  passes. OpenShell's adversarial end-to-end tests (`e2e/rust/tests/`:
  bypass detection, L7 proxy bypass, credential gating) are the first
  source of further cases:
  - connect to a raw IP address, to the host, to the LAN, over IPv6;
  - send UDP other than DNS; resolve a non-allowlisted name; exceed the
    wildcard budget; use a DNS server other than the gateway;
  - present an SNI that differs from the resolved name;
  - push to a repository outside the project set, with and without an
    attacker-supplied token; create a gist or repository; publish a
    package;
  - call the model API with an attacker-supplied key and observe that it
    is replaced;
  - send a credential placeholder outside its binding (another host, a
    query string, a body) and get a `403`;
  - once X14 has delivered labels: forge or omit a flow label and
    confirm only rules without program narrowing match, and replace a
    pinned binary and confirm its connections are denied;
  - download a package version younger than the minimum age, or with a
    known vulnerability;
  - read or change policy, credentials, or the audit log from the guest;
  - write into the host repository through the git transport;
  - forge an approval prompt via terminal output and confirm nothing
    treats it as one.
- **Benchmarks** (N1, N2): time to Claude prompt (warm, suspended);
  `npm ci`, `git status` on a large repository, and an incremental build
  on the data disk compared with the host; throughput of a large
  download through the gateway.
- **Platform contract tests.** Each `internal/platform` interface has
  one test suite that every OS implementation runs (spec 012).
- **CI.** Go unit tests, lint, and vet run natively on macOS, Linux, and
  Windows hosted runners; Swift on macOS; fuzz smoke runs. The
  conformance suite and benchmarks need machines that can run the guest
  VMs (Apple Silicon for macOS guests; nested virtualization or bare
  metal elsewhere); hosted runners mostly cannot, so self-hosted runners
  are needed later.

## Spikes, in order

Each spike is a throwaway prototype with a yes/no answer and a written
result in this directory before dependent work starts. Spikes are
numbered `X*` so they cannot be confused with the security requirements
(`S*`) of spec 003.

Each spike is one GitHub issue titled `X<n>: <name>`. Its throwaway code
stays on a `spike/x<n>-<slug>` branch that is kept and never merged; the
written result is `docs/spec/spikes/X<n>-<slug>.md`, linked from the
spike's entry below and merged together with any spec change the answer
forces. A new open question gets the
next free number here before it is filed. The workflow is in
`docs/agents/planning.md`.

### macOS host, macOS guest (v1)

1. **X1 Model credential via the proxy.** Does Claude Code work with a
   placeholder credential while `wb-proxyd` injects the real one, for
   each supported authentication mode, including token refresh? If not,
   requirement S4 needs a documented exception and the threat model changes.
   OpenShell's Claude Code provider shows that an API key works. A Claude
   subscription login is the open part: OpenShell holds back from it
   until Anthropic approves an OAuth client identity for third-party
   tools (OpenShell issue #3331), and a login shared with the host's
   Claude Code would race it on token refresh. The answer must say
   which login Wraith Box owns and how it is refreshed.
2. **X2 Warm start.** Measure time to restore a macOS guest from saved
   state with a vsock device and a file-handle network attachment.
   Can a saved state be reused (cloned with its disks) more than once?
3. **X3 Network path.** A macOS guest boots on the file-handle
   attachment with the gVisor stack: gets a DHCP lease, resolves only
   allowlisted names, reaches only the proxy. Confirm no entitlement
   beyond virtualization is needed. Measure throughput.
4. **X4 Inspection compatibility.** Go-based, Swift-based, and Xcode
   clients accept the name-constrained CA when it is trusted only in the
   guest. List clients that pin certificates.
5. **X5 Filesystem benchmark.** Data disk with default versus relaxed
   write-through settings, against the host, on the N2 workloads.
6. **X6 Guest users and Xcode.** Can builds and simulators run for a
   guest user without a GUI login? If not, Xcode sessions go to the
   isolated VM's console user.
7. **X7 Git round trip.** Remote helper over vsock, read-only
   `upload-pack`, restricted `receive-pack` into the landing repository,
   flagging of risky paths.
8. **X8 Data disk for homes.** Guest user homes on a second disk,
   surviving a system-disk replacement.
9. **X9 Keychain access without a signing identity.** Confirm the
   fallback access-control model from spec 009 and the Secure Enclave
   key creation for an ad-hoc signed binary.

### Other platforms (spec 012)

These do not block v1, but each must be answered before work on its
platform starts.

10. **X10 Linux VMM and packet transport.** Pick a permissively licensed
    VMM over KVM that supports vsock, save/restore, copy-on-write disks,
    and a virtual TPM (for Windows guests), and a way to deliver guest
    Ethernet frames to `wb-netd` as an unprivileged user (no tap device,
    no `CAP_NET_ADMIN`, no unprivileged user namespaces). Measure N1 and
    network throughput.
11. **X11 Windows host through HCS.** On Windows 11 **Home** with only
    the Virtual Machine Platform feature: can a standard user create,
    start, save, and restore a VM through HCS? Is a Hyper-V socket
    available to it? How do guest frames reach `wb-netd` without a
    virtual switch that bridges to the host network? Can a Windows 11
    guest get a virtual TPM and Secure Boot? If any answer needs
    administrator rights, write up the smallest privileged component
    that would do (spec 012, S12).
12. **X12 WSL client channel.** `wb` in WSL 2 relaying through
    `wb.exe relay` over interop standard streams: terminal fidelity,
    window resizing, throughput of the git transport, latency to the
    first prompt. Compare with a Hyper-V socket from the WSL VM.
13. **X13 Windows guests on a macOS host.** Can the Virtualization
    framework boot Windows 11 for Arm in a supported way, including TPM
    and Secure Boot requirements and usable display and network drivers?
    If not, Windows guests on macOS stay unsupported; a second VMM on
    macOS needs a spec change.

### Guest confinement (spec 013)

These do not block v1: every `S*` control holds without spec 013's
layers 3 and 4.

14. **X14 Guest flow attribution.** A Network Extension transparent
    proxy in a macOS guest (spec 013): can it be approved during the
    image build without MDM? Does it see the flows of Claude Code, git,
    Homebrew, SwiftPM, and `xcodebuild`? How do labels reach `wb-netd`
    (a header on each flow, or a side channel over vsock)? What happens
    to flows when guest root kills or unloads it, with System Integrity
    Protection on? Which Developer ID entitlement does it need?
15. **X15 Endpoint Security entitlement.** Request the Endpoint
    Security entitlement from Apple. Until it is granted, prototype the
    client in a development guest with System Integrity Protection off:
    exec authorization by cdhash and process ancestry for spec 013's
    layer 4.

### Linux guests through OpenShell

Answered before X10, because a yes may make X10 unnecessary.

16. **X16 OpenShell for Linux guests.** Could OpenShell's VM driver
    (libkrun, guest without a network device, egress over vsock) run
    Linux guests for Wraith Box on macOS and Linux hosts instead of a
    VMM chosen in X10? The check covers S1 to S14, a policy shared with
    macOS guests (spec 009), and N1. It has no git round trip and no
    saved-state restore, so those are Wraith Box's to add. Its other drivers share a kernel between
    sandboxes (S1) and are out of the question.

### Found during initial planning (v1)

These came out of the first read-through of the specs for an
implementation plan (`docs/implementation-plan.md`). Each tests an
assumption that v1 work would otherwise build on. They block v1.

17. **X17 Unattended image build.** Can a macOS guest installed from
    an Apple restore image get past Setup Assistant, with `wb-guestd`
    running as a root LaunchDaemon, without SSH, scripted clicks, or
    MDM? The candidate is writing files to the new guest's data
    volume from the host at build time, before any untrusted code has
    run. Measure the build time from the restore image.
18. **X18 Host-guest socket and descriptor hand-off.** Can `wb-guestd`,
    written in Go, open `AF_VSOCK` sockets in a macOS guest? Do the
    file descriptors of a vsock connection and of the file-handle
    network attachment keep working after `wb-vmd` passes them to
    another process (`SCM_RIGHTS`), and across save and restore? Spec
    004 rests on the answer ("descriptors, not bytes").
19. **X19 Terminal stream filtering.** Which terminal escape sequences
    does Claude Code emit, and can `wb` drop the ones that act on the
    host (clipboard writes, file transfer, terminal multiplexer control
    sequences) without visible damage in the common macOS terminals?
    The relay passes guest bytes to the host terminal emulator.
20. **X20 Homebrew with more than one project user.** Can each project
    user in one guest get its declared Brewfile without a Homebrew
    prefix that another project user can write to (S8)? Compare a
    prefix owned by `wb-guestd` that installs every declared Brewfile
    with per-user prefixes. Measure bottle availability and install
    time for both.
21. **X21 Dependency gate on real registries.** Can `wb-proxyd` map a
    download to package, version and publish time for npm, PyPI, the
    Go module proxy, crates.io and Homebrew? Does removing too-young
    versions from metadata let resolvers pick an older version, where
    refusing the download fails the whole install? Measure failure
    rates with a 7-day minimum age on real projects, and the time OSV
    lookups add to an `npm ci` with a large lockfile.
22. **X22 Clients without guest credentials.** Which common clients
    (Homebrew against `ghcr.io`, git, `gh`, npm, pip, uv, SwiftPM, Go,
    cargo, Claude Code) break when `wb-proxyd` removes every
    guest-supplied `Authorization` header and cookie on inspected
    hosts? Registries such as `ghcr.io` hand out anonymous tokens that the
    client must send back. Find a rule for those that keeps S4 and S6.
23. **X23 Self-sandboxed Go daemons on macOS.** Can `wb-netd`,
    `wb-proxyd` and `wb-hostd` confine themselves at start with a
    sandbox profile, as spec 004 says, while the Go runtime, inherited
    descriptors, and Keychain and Secure Enclave access in `wb-proxyd`
    keep working?
24. **X24 OpenShell artifacts.** Do the OpenShell parts that specs 007,
    009 and 010 build on exist in the assumed form, under a permissive
    license, at a version that can be pinned: the `network_policies`
    schema version 1 (still valid with Wraith Box's extra top-level
    keys), a standalone `openshell-prover` binary and its reading of
    `binaries: /**`, the proposal risk check for one rule, and
    `supervisor_middleware.proto` (RFC 0009) used inside a Go process?
    The same check covers the OCSF 1.8 classes and the agent-safehouse
    Seatbelt profiles.

## Deferred beyond v1

Git LFS and submodules; image distribution through a registry; Linux
and Windows guests and hosts (spec 012); containers inside the sandbox;
additional agents.

**Status:** Draft
