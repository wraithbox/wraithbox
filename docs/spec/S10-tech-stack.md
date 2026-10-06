# S10 - Technology Choices

**Purpose:** Fix languages, libraries, packaging, and repository layout,
with the reason for each choice.

**Requirements:** NFR04-host-platforms, NFR07-maintainability, NFR08-licensing, SEC12-least-privilege, FR17-same-everywhere.

## Languages: Go first, native where needed

Most of Wraith Box is Go and the same on every host OS. A platform-native
language is used only where Go cannot reasonably reach a platform API,
in a separate process with a gRPC contract (S12-platforms).

| Component | Language | Reason |
|---|---|---|
| `wb` | Go | One static binary per OS with millisecond start-up; mature PTY and terminal handling; the same CLI everywhere |
| `wb-hostd` | Go | Sessions, policy, approvals, audit, and the git gateway are platform-independent logic |
| `wb-netd`, `wb-proxyd` | Go | gVisor's network stack is Go; strong standard TLS/HTTP/HTTP2 libraries; built-in fuzzing for the parsers that face the guest |
| `wb-guestd`, `git-remote-wb` | Go | One code base for macOS, Linux, and Windows guests; PTY, vsock, and user management |
| `wb-vmd` on Windows and Linux | Go | HCS and Hyper-V sockets have Go bindings; Linux VMMs are driven over sockets and processes |
| `wb-vmd` on macOS, macOS notification helper, macOS guest Network Extension (S13-guest-confinement) | Swift 6 | First-class access to the Virtualization framework (macOS guests, save/restore, file-handle network attachment), to notifications, and to the Network Extension provider classes |
| Windows notification and tray helper (later) | C# (.NET) | Native Windows notifications with actions |

No other language is added without a spec change.

## Libraries

Permissive licenses only (Apache-2.0, MIT, BSD, ISC). No copyleft and no
source-available licenses. This includes programs Wraith Box ships or
downloads and runs, such as a VMM on Linux.

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
| Keychain / Secure Enclave from Go | a thin cgo shim over Security.framework, owned by this repo, linked into `wb-proxyd` on macOS only |
| State | SQLite, through a cgo-free Go driver so every OS builds the same way |
| Vulnerability data | OSV API |
| Network policy schema | OpenShell's `network_policies` schema, version 1, with the keys of OpenShell v0.1.2 (commit `6648bd0c`, Apache-2.0), implemented in Go by this repo; YAML read with a size- and depth-bounded decoder (S09-policy-credentials-audit) |
| Policy prover | OpenShell's standalone `openshell-prover` 0.1.2 release binary (Apache-2.0, Z3 linked in statically under MIT), run by `wb-prover` through `wb-launcher` (S04-architecture, S09-policy-credentials-audit). At vendoring or release time: pin the archive's SHA-256, check the release's build attestation, extract only the one regular file `openshell-prover` (no links, no other paths), re-sign it with Wraith Box's signing identity, install it in the bundle, and pin the SHA-256 of that binary, which `wb-prover` checks before each run. X24-openshell-artifacts recorded the archive hash for macOS arm64 only. v0.1.2 has builds for macOS arm64 and Linux (x86-64 and arm64), none for Windows, so the boundary check fails closed on a Windows host until one exists, which refuses every session start there, not only `wb trust` (S09-policy-credentials-audit, I91) |
| Approval risk check | Go code in `wb-hostd`, following the four categories and names of OpenShell's proposal risk check at v0.1.2, with Wraith Box's own checks added and OpenShell's cases as tests (S09-policy-credentials-audit). The maintainer decided this on I35 (B35-openshell-artifacts) |
| Dependency-gate API | OpenShell's `supervisor_middleware.proto` and `extension.proto` (package `openshell.middleware.v1`, OpenShell v0.1.2, Apache-2.0), copied with their license headers; Go code generated with `buf`, with import paths set in the generation config because the files set no `go_package`. They follow OpenShell's `buf` lint exceptions, not this repo's rules |
| Audit event schema | OCSF 1.8.0 (`ocsf-schema` commit `6fa6499a`, Apache-2.0), the version OpenShell v0.1.2 emits |
| Guest confinement (macOS guest) | Seatbelt profiles applied by `wb-guestd`; a Network Extension system extension in Swift (S13-guest-confinement) |
| Image distribution (later) | `go-containerregistry` |

## Repository layout

```
packages/
  wraithbox-go/      Go module: cmd/{wb,wb-hostd,wb-netd,wb-proxyd,wb-guestd,wb-vmd}, internal/…
                     internal/platform: host/guest matrix and per-OS interfaces (S12-platforms)
  wraithbox-swift/   SwiftPM package: WraithBoxVM library, wb-vmd executable (macOS)
  wraithbox-dotnet/  .NET solution for Windows-native helpers (when the first one is built)
  wraithbox-doc/     documentation site
proto/               .proto contracts shared by every language (added with the first RPC)
docs/spec/           specifications (this directory)
```

`cmd/wb-vmd` in the Go module builds for Windows and Linux; on macOS the
binary of that name comes from the Swift package.

## Packaging and signing

- **macOS:** an app bundle, `Wraith Box.app`, contains `wb-hostd`,
  `wb-vmd`, `wb-netd`, `wb-proxyd`, the notification helper, the guest
  tools disk image, and `wb`, which the installer links onto `PATH`. A
  bundle is needed for notifications, Keychain access groups, and
  launch-agent registration. Hardened runtime; only `wb-vmd` has the
  virtualization entitlement. Developer ID signing and notarization come
  later; until then builds are ad-hoc signed and the Keychain access
  model uses the weaker fallback in S09-policy-credentials-audit. Distribution via a
  Homebrew cask once signed builds exist.
- **Windows (later):** a per-user installer (no administrator rights)
  with the same set of binaries as `.exe`; code signing when available.
- **Linux (later):** a tarball and Ubuntu package with the same binaries
  and a systemd user unit.
- **WSL:** the Linux `wb` binary; it requires the Windows installation
  (S12-platforms).
- Each host daemon applies its own confinement at start (S04-architecture).

## Platforms

See S12-platforms for the host/guest matrix. v1: Apple Silicon, macOS 15 or
later, with macOS guests; the guest image's macOS version is independent
of the host's, within what the Virtualization framework supports.

## Quality gates

`gofumpt`, `goimports`, `golangci-lint`, `go vet`, `govulncheck` (for each
host OS); `swift-format` (strict) and SwiftLint complexity limits; Swift
Testing and `go test -race`; fuzz targets for every guest-facing parser
(S11-verification-and-spikes); Vale and cspell for prose. Go runs natively in CI on macOS,
Linux, and Windows. See S02-toolchain for the toolchain itself.

**Status:** Draft
