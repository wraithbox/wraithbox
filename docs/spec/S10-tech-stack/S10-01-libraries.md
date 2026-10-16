# S10-01 - Libraries

**Purpose:** The libraries and programs Wraith Box uses, and their
licenses.

## Design


Permissive licenses only (Apache-2.0, MIT, BSD, ISC). No copyleft and
no source-available licenses. This includes programs Wraith Box ships
or downloads and runs, such as a VMM on Linux.

| Need | Choice |
|---|---|
| Userspace TCP/IP | gVisor `pkg/tcpip`, from gVisor's `go` branch (Apache-2.0) |
| RPC | protobuf + gRPC (`grpc-go`, `grpc-swift`, `swift-protobuf`; `Grpc.Net` when .NET arrives); code generated with `buf` |
| DNS server | a mature Go DNS library (for example `miekg/dns`) |
| TLS/HTTP proxying | Go standard library (`crypto/tls`, `net/http`, `httputil`), `golang.org/x/net/http2` |
| PTY | `creack/pty` (Unix), ConPTY through `golang.org/x/sys/windows`; `golang.org/x/term` |
| Windows host APIs | `golang.org/x/sys/windows`; Microsoft's Go libraries for HCS and named pipes / Hyper-V sockets (MIT) |
| Linux host APIs | `golang.org/x/sys/unix` (KVM device checks, Landlock, seccomp, vsock); a pure-Go D-Bus client (Secret Service, notifications) |
| Linux VMM | decided by spike X10-linux-hypervisor; must be permissively licensed |
| TPM | a Go TPM 2.0 library (Linux); CNG through `golang.org/x/sys/windows` (Windows) |
| Keychain / Secure Enclave from Go | a thin cgo shim over Security.framework, owned by this repo, linked on macOS only into the processes that S15-least-privilege declares store access for |
| State | SQLite, through a cgo-free Go driver so every OS builds the same way |
| Vulnerability data | OSV API |
| Network policy schema | OpenShell's `network_policies` schema, version 1, with the keys of OpenShell v0.1.2 (commit `6648bd0c`, Apache-2.0), implemented in Go by this repo; YAML read with a size- and depth-bounded decoder (S09-policy-credentials-audit) |
| Policy prover | OpenShell's standalone `openshell-prover` 0.1.2 release binary ("Policy prover" below) |
| Approval risk check | Go code in `wb-hostd` ("Approval risk check" below) |
| Dependency-gate API | OpenShell's `supervisor_middleware.proto` and `extension.proto` ("Dependency-gate API" below) |
| Audit event schema | OCSF 1.8.0 (`ocsf-schema` commit `6fa6499a`, Apache-2.0), the version OpenShell v0.1.2 emits |
| Guest confinement (macOS guest) | Seatbelt profiles applied by `wb-guestd`; a Network Extension system extension in Swift (S13-guest-confinement) |
| Image distribution (later) | `go-containerregistry` |

### Policy prover

The policy prover is OpenShell's standalone `openshell-prover` 0.1.2
release binary. It is Apache-2.0, with Z3 linked in statically under
MIT. `wb-prover` runs it through `wb-launcher` (S04-architecture,
S09-policy-credentials-audit).

At vendoring or release time, the build:

1. pins the archive's SHA-256;
2. checks the release's build attestation;
3. extracts only the one regular file `openshell-prover`, with no links
   and no other paths;
4. re-signs it with Wraith Box's signing identity;
5. installs it in the bundle;
6. pins the SHA-256 of that binary, which `wb-prover` checks before
   each run.

X24-openshell-artifacts recorded the archive hash for macOS arm64 only.
v0.1.2 has builds for macOS arm64 and Linux (x86-64 and arm64), and
none for Windows. So the boundary check fails closed on a Windows host
until one exists. That refuses every session start there, not only
`wb trust` (S09-policy-credentials-audit, I91).

### Approval risk check

The approval risk check is Go code in `wb-hostd`
(S09-policy-credentials-audit).

It follows the four categories and names of OpenShell's proposal risk
check at v0.1.2, and adds Wraith Box's own checks. OpenShell's cases
are its tests.

### Dependency-gate API

The dependency-gate API is OpenShell's `supervisor_middleware.proto`
and `extension.proto`: package `openshell.middleware.v1`, OpenShell
v0.1.2, Apache-2.0.

They are copied with their license headers. Go code is generated with
`buf`. The import paths are set in the generation config, because the
files set no `go_package`. The files follow OpenShell's `buf` lint
exceptions, not this repo's rules.

## Decisions

### Approval risk check after OpenShell's

- **Decided:** the maintainer decided this on I35
  (B35-openshell-artifacts).

**Status:** Draft
