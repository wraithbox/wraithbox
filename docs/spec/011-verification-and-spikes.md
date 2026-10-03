# 011 - Verification and Open Questions

**Purpose:** How the requirements are proven, and which assumptions must
be tested before building on them.

## Test layers

- **Unit tests.** Go `go test -race`, table-driven; Swift Testing.
- **Fuzzing.** Go native fuzz targets for every parser that sees
  guest-controlled bytes: Ethernet/IP/TCP handling at the link endpoint,
  DHCP, DNS, TLS ClientHello parsing, HTTP/1.1 and HTTP/2 request
  handling, policy matching, the git transport framing, every vsock RPC
  handler in `wb-hostd` (Swift: property-based tests where fuzzing is
  impractical).
- **Conformance suite.** A set of adversarial checks run *inside a guest*
  against a real `wb-netd`/`wb-proxyd`. Each must fail safely, and the
  suite is a release gate for every host/guest combination in the
  platform matrix (spec 012); a combination is not supported until it
  passes:
  - connect to a raw IP address, to the host, to the LAN, over IPv6;
  - send UDP other than DNS; resolve a non-allowlisted name; exceed the
    wildcard budget; use a DNS server other than the gateway;
  - present an SNI that differs from the resolved name;
  - push to a repository outside the project set, with and without an
    attacker-supplied token; create a gist or repository; publish a
    package;
  - call the model API with an attacker-supplied key and observe that it
    is replaced;
  - download a package version younger than the minimum age, or with a
    known vulnerability;
  - read or change policy, credentials or the audit log from the guest;
  - write into the host repository through the git transport;
  - forge an approval prompt via terminal output and confirm nothing
    treats it as one.
- **Benchmarks** (N1, N2): time to Claude prompt (warm, suspended);
  `npm ci`, `git status` on a large repository, and an incremental build
  on the data disk compared with the host; throughput of a large
  download through the gateway.
- **Platform contract tests.** Each `internal/platform` interface has
  one test suite that every OS implementation runs (spec 012).
- **CI.** Go unit tests, lint and vet run natively on macOS, Linux and
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

### macOS host, macOS guest (v1)

1. **X1 Model credential via the proxy.** Does Claude Code work with a
   placeholder credential while `wb-proxyd` injects the real one, for
   each supported authentication mode, including token refresh? If not,
   requirement S4 needs a documented exception and the threat model changes.
2. **X2 Warm start.** Measure time to restore a macOS guest from saved
   state with a vsock device and a file-handle network attachment.
   Can a saved state be reused (cloned with its disks) more than once?
3. **X3 Network path.** A macOS guest boots on the file-handle
   attachment with the gVisor stack: gets a DHCP lease, resolves only
   allowlisted names, reaches only the proxy. Confirm no entitlement
   beyond virtualization is needed. Measure throughput.
4. **X4 Inspection compatibility.** Go-based, Swift-based and Xcode
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
    VMM over KVM that supports vsock, save/restore, copy-on-write disks
    and a virtual TPM (for Windows guests), and a way to deliver guest
    Ethernet frames to `wb-netd` as an unprivileged user (no tap device,
    no `CAP_NET_ADMIN`, no unprivileged user namespaces). Measure N1 and
    network throughput.
11. **X11 Windows host through HCS.** On Windows 11 **Home** with only
    the Virtual Machine Platform feature: can a standard user create,
    start, save and restore a VM through HCS? Is a Hyper-V socket
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

## Later, not v1

Git LFS and submodules; image distribution through a registry; Linux
and Windows guests and hosts (spec 012); containers inside the sandbox;
additional agents.

**Status:** Draft
