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
  suite is a release gate:
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
- **CI.** Unit tests, lint and fuzz smoke runs in hosted CI. The
  conformance suite and benchmarks need an Apple Silicon machine that
  can run macOS guests; hosted CI runners cannot, so a self-hosted runner
  is needed later.

## Spikes, in order

Each spike is a throwaway prototype with a yes/no answer and a written
result in this directory before dependent work starts.

1. **S1 Model credential via the proxy.** Does Claude Code work with a
   placeholder credential while `wb-proxyd` injects the real one, for
   each supported authentication mode, including token refresh? If not,
   S4 needs a documented exception and the threat model changes.
2. **S2 Warm start.** Measure time to restore a macOS guest from saved
   state with a vsock device and a file-handle network attachment.
   Can a saved state be reused (cloned with its disks) more than once?
3. **S3 Network path.** A macOS guest boots on the file-handle
   attachment with the gVisor stack: gets a DHCP lease, resolves only
   allowlisted names, reaches only the proxy. Confirm no entitlement
   beyond virtualization is needed. Measure throughput.
4. **S4 Inspection compatibility.** Go-based, Swift-based and Xcode
   clients accept the name-constrained CA when it is trusted only in the
   guest. List clients that pin certificates.
5. **S5 Filesystem benchmark.** Data disk with default versus relaxed
   write-through settings, against the host, on the N2 workloads.
6. **S6 Guest users and Xcode.** Can builds and simulators run for a
   guest user without a GUI login? If not, Xcode sessions go to the
   isolated VM's console user.
7. **S7 Git round trip.** Remote helper over vsock, read-only
   `upload-pack`, restricted `receive-pack` into the landing repository,
   flagging of risky paths.
8. **S8 Data disk for homes.** Guest user homes on a second disk,
   surviving a system-disk replacement.
9. **S9 Keychain access without a signing identity.** Confirm the
   fallback access-control model from spec 009 and the Secure Enclave
   key creation for an ad-hoc signed binary.

## Later, not v1

Git LFS and submodules; image distribution through a registry; Linux
guests; containers inside the sandbox; additional agents.

**Status:** Draft
