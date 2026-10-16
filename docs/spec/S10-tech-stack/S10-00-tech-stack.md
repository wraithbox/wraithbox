# S10 - Technology Choices

**Purpose:** Fix languages, libraries, packaging, and repository layout,
with the reason for each choice.

**Requirements:** NFR04-host-platforms, NFR07-maintainability, NFR08-licensing, SEC12-least-privilege, FR17-same-everywhere.

**Tests:** C05-credentials-and-store

## Design

### Pages

| Page | What it covers |
|---|---|
| S10-01-libraries | The libraries and programs Wraith Box uses, and their licenses |
| S10-02-packaging-and-signing | The app bundle, helper bundles, signing, other hosts, and the build version |

### Languages: Go first, native where needed

Most of Wraith Box is Go, and the same on every host OS. A
platform-native language is used only where Go cannot reasonably reach
a platform API. It then runs in a separate process with a gRPC contract
(S12-platforms).

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

### Repository layout

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

`cmd/wb-vmd` in the Go module builds for Windows and Linux. On macOS,
the binary of that name comes from the Swift package.

### Platforms

See S12-platforms for the host/guest matrix. v1 is Apple Silicon,
macOS 15 or later, with macOS guests. The guest image's macOS version
is independent of the host's, within what the Virtualization framework
supports.

### Quality gates

- Go: `gofumpt`, `goimports`, `golangci-lint`, `go vet`, and
  `govulncheck` for each host OS.
- Complexity and size limits in golangci-lint (cognitive and cyclomatic
  complexity, nesting, function, file and signature size) and in
  SwiftLint.
- `swift-format` (strict).
- Swift Testing and `go test -race`.
- Fuzz targets for every guest-facing parser
  (S11-verification-and-spikes).
- Vale and cspell for prose.

Go runs natively in CI on macOS, Linux, and Windows. See S02-toolchain
for the toolchain itself.

**Status:** Draft
