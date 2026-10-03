# 010 - Technology Choices

**Purpose:** Fix languages, libraries, packaging and repository layout,
with the reason for each choice.

**Requirements:** N4, N7, N8, S12.

## Languages: Go and Swift, nothing else

| Component | Language | Reason |
|---|---|---|
| `wb`, `wbctl` | Go | Static binaries with millisecond start-up; mature PTY and terminal handling |
| `wb-hostd` | Swift 6 | First-class access to the Virtualization framework (macOS guests, save/restore, file-handle network attachment), launch-agent registration, notifications, Keychain |
| `wb-netd`, `wb-proxyd` | Go | gVisor's network stack is Go; strong standard TLS/HTTP/HTTP2 libraries; built-in fuzzing for the parsers that face the guest |
| `wb-guestd`, `git-remote-wb` | Go | Static binary in the guest; PTY, vsock and user management |

A third language is not added without a spec change.

## Libraries

Permissive licenses only (Apache-2.0, MIT, BSD, ISC). No copyleft and no
source-available licenses.

| Need | Choice |
|---|---|
| Userspace TCP/IP | gVisor `pkg/tcpip`, from gVisor's `go` branch (Apache-2.0) |
| RPC | protobuf + gRPC (`grpc-go`, `grpc-swift`, `swift-protobuf`); code generated with `buf` |
| DNS server | a mature Go DNS library (for example `miekg/dns`) |
| TLS/HTTP proxying | Go standard library (`crypto/tls`, `net/http`, `httputil`), `golang.org/x/net/http2` |
| PTY | `creack/pty`, `golang.org/x/term` |
| Keychain / Secure Enclave from Go | a thin cgo shim over Security.framework, owned by this repo |
| State | SQLite (system library on macOS) |
| Vulnerability data | OSV API |
| Image distribution (later) | `go-containerregistry` |

## Repository layout

```
packages/
  wraithbox-go/      Go module: cmd/{wb,wbctl,wb-netd,wb-proxyd,wb-guestd}, internal/…
  wraithbox-swift/   SwiftPM package: WraithBoxHost library, wb-hostd executable
  wraithbox-doc/     documentation site
proto/               .proto contracts shared by Go and Swift (added with the first RPC)
docs/spec/           specifications (this directory)
```

## Packaging and signing

- An app bundle, `Wraith Box.app`, contains `wb-hostd`, `wb-netd`,
  `wb-proxyd`, the guest tools disk image, and the CLIs; `wb` and `wbctl`
  are symlinked onto `PATH` by the installer. A bundle is needed for
  notifications, Keychain access groups and launch-agent registration.
- Hardened runtime. Only `wb-hostd` carries the virtualization
  entitlement. Each host daemon applies its own sandbox profile at start
  (spec 004).
- Developer ID signing and notarization come later. Until then builds
  are ad-hoc signed and the Keychain access model uses the weaker
  fallback described in spec 009.
- Distribution via a Homebrew cask once signed builds exist.

## Platforms

- Host: Apple Silicon, macOS 15 or later (N4).
- Guest: macOS 15 or later; the image's macOS version is independent of
  the host's, within what the Virtualization framework supports.

## Quality gates

`gofmt`/`gofumpt`, `golangci-lint`, `go vet`, `govulncheck`;
`swift-format` (strict) and SwiftLint complexity limits; Swift Testing
and `go test -race`; fuzz targets for every guest-facing parser
(spec 011). See spec 002 for the toolchain itself.

**Status:** Draft
