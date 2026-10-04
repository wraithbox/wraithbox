# 010 - Technology Choices

**Purpose:** Fix languages, libraries, packaging, and repository layout,
with the reason for each choice.

**Requirements:** N4-host-platforms, N7-maintainability, N8-licensing, S12-least-privilege, F17-same-everywhere.

## Languages: Go first, native where needed

Most of Wraith Box is Go and the same on every host OS. A platform-native
language is used only where Go cannot reasonably reach a platform API,
in a separate process with a gRPC contract (spec 012-platforms).

| Component | Language | Reason |
|---|---|---|
| `wb` | Go | One static binary per OS with millisecond start-up; mature PTY and terminal handling; the same CLI everywhere |
| `wb-hostd` | Go | Sessions, policy, approvals, audit, and the git gateway are platform-independent logic |
| `wb-netd`, `wb-proxyd` | Go | gVisor's network stack is Go; strong standard TLS/HTTP/HTTP2 libraries; built-in fuzzing for the parsers that face the guest |
| `wb-guestd`, `git-remote-wb` | Go | One code base for macOS, Linux, and Windows guests; PTY, vsock, and user management |
| `wb-vmd` on Windows and Linux | Go | HCS and Hyper-V sockets have Go bindings; Linux VMMs are driven over sockets and processes |
| `wb-vmd` on macOS, macOS notification helper, macOS guest Network Extension (spec 013-guest-confinement) | Swift 6 | First-class access to the Virtualization framework (macOS guests, save/restore, file-handle network attachment), to notifications, and to the Network Extension provider classes |
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
| Network policy schema | OpenShell's `network_policies` schema, version 1 (Apache-2.0), implemented in Go by this repo; YAML read with a size- and depth-bounded decoder |
| Policy prover | OpenShell's standalone `openshell-prover` release binary (Apache-2.0, bundles Z3 under MIT), run as an external program, version- and checksum-pinned |
| Dependency-gate API | OpenShell's `supervisor_middleware.proto` (Apache-2.0), Go code generated with `buf` |
| Audit event schema | OCSF 1.8 |
| Guest confinement (macOS guest) | Seatbelt profiles applied by `wb-guestd`; a Network Extension system extension in Swift (spec 013-guest-confinement) |
| Image distribution (later) | `go-containerregistry` |

## Repository layout

```
packages/
  wraithbox-go/      Go module: cmd/{wb,wb-hostd,wb-netd,wb-proxyd,wb-guestd,wb-vmd}, internal/…
                     internal/platform: host/guest matrix and per-OS interfaces (spec 012-platforms)
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
  model uses the weaker fallback in spec 009-policy-credentials-audit. Distribution via a
  Homebrew cask once signed builds exist.
- **Windows (later):** a per-user installer (no administrator rights)
  with the same set of binaries as `.exe`; code signing when available.
- **Linux (later):** a tarball and Ubuntu package with the same binaries
  and a systemd user unit.
- **WSL:** the Linux `wb` binary; it requires the Windows installation
  (spec 012-platforms).
- Each host daemon applies its own confinement at start (spec 004-architecture).

## Platforms

See spec 012-platforms for the host/guest matrix. v1: Apple Silicon, macOS 15 or
later, with macOS guests; the guest image's macOS version is independent
of the host's, within what the Virtualization framework supports.

## Quality gates

`gofumpt`, `goimports`, `golangci-lint`, `go vet`, `govulncheck` (for each
host OS); `swift-format` (strict) and SwiftLint complexity limits; Swift
Testing and `go test -race`; fuzz targets for every guest-facing parser
(spec 011-verification-and-spikes); Vale and cspell for prose. Go runs natively in CI on macOS,
Linux, and Windows. See spec 002-toolchain for the toolchain itself.

**Status:** Draft
