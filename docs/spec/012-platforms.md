# 012 - Platforms

**Purpose:** Which host and guest operating systems Wraith Box targets,
how platform-specific code is isolated, what each platform uses, and how
`wb` works inside WSL.

**Requirements:** F16, F17, S1, S2, S4, S5, S12, N4, N5, N7, C1, C3.

## Platform matrix

| Host ↓ / Guest → | macOS | Linux (Ubuntu LTS first) | Windows 11 |
|---|---|---|---|
| **macOS 15+, Apple Silicon** | **v1** | later | later, blocked on spike X13 |
| **Windows 11 Home / Pro** | not possible | later | later, blocked on spike X11 |
| **Linux, Ubuntu LTS first** | not possible | later | later |

- macOS guests need Apple hardware, technically and by licence.
- Guests use the host's CPU architecture: arm64 on Apple Silicon; x86-64
  or arm64 on Windows and Linux. No instruction-set emulation.
- Windows guests need a Windows licence supplied by the user. Wraith Box
  never ships or downloads a Windows image itself.
- "Windows 11" means current Home and Pro editions. Features available
  only on Pro or Enterprise (the Hyper-V management stack, Windows
  Sandbox) are not relied on.
- Linux distributions other than Ubuntu LTS, as host or guest, are
  considered after Ubuntu works; the Linux code avoids Ubuntu-only
  assumptions where that is free.

The code carries this matrix in `internal/platform` and tests it.

## Isolation of platform code

The cross-platform core never asks which OS it is on. It talks to a
small set of interfaces, each with one implementation per host OS:

| Interface | Responsibility | macOS | Windows | Linux |
|---|---|---|---|---|
| **VM provider** (`wb-vmd`) | Create, start, stop, save, restore VMs; disks; devices | Swift, Virtualization framework | Go, Host Compute Service (HCS) | Go, supervising a VMM over KVM (spike X10) |
| **Host-guest socket** | Byte streams between `wb-hostd` and `wb-guestd` | virtio-vsock | Hyper-V sockets | virtio-vsock |
| **Packet transport** | Guest Ethernet frames to and from `wb-netd`, without host privileges | file-handle network attachment (datagram socketpair) | spike X11 | spike X10 |
| **Disk cloning** | Copy-on-write images | APFS clones | VHDX differencing disks | qcow2 backing files, or reflinks where the filesystem supports them |
| **Local IPC** | Host gRPC endpoints, peer identity | Unix sockets, peer credentials | named pipes, user-SID ACL, client process id | Unix sockets, `SO_PEERCRED` |
| **Secret store** | Credentials, readable by `wb-proxyd` only | Keychain | Credential Manager (DPAPI, per user) | Secret Service (D-Bus); see below |
| **Hardware key** | Non-exportable CA signing key | Secure Enclave | TPM via the CNG platform crypto provider | TPM 2.0 where present |
| **Service manager** | Run `wb-hostd` per user, at login | LaunchAgent | per-user logon task | systemd user unit |
| **Self-sandbox** | Confine our own daemons (spec 004) | sandbox profile applied at start | restricted token, job objects, AppContainer where it fits | Landlock and seccomp (no unprivileged user namespaces needed) |
| **Notifications** | Approval prompts with actions | native helper (Swift) | native helper (see languages) | freedesktop notifications over D-Bus from Go |
| **Downloaded-file marker** | Files leaving the guest other than via git | quarantine attribute | Mark of the Web | none (documented) |
| **Paths** | `<config>`, `<data>`, `<logs>` of spec 004 | `~/.config/wraithbox`, `~/Library/Application Support/WraithBox`, `~/Library/Logs/WraithBox` | `%APPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox\Logs` | `$XDG_CONFIG_HOME/wraithbox`, `$XDG_DATA_HOME/wraithbox`, `$XDG_STATE_HOME/wraithbox/logs` |

Rules:

- **Go interfaces in `internal/platform`**, implemented in files with
  `_darwin.go`, `_windows.go` and `_linux.go` suffixes. Shared code never
  branches on `runtime.GOOS`.
- **A native component is a separate process** with a gRPC contract,
  used only where Go cannot reasonably call the platform API. It makes no
  policy decisions and holds no secrets.
- **Every platform implementation has the same conformance tests** (spec
  011); a platform is not "supported" until its conformance suite passes.
- **Unimplemented platforms fail clearly.** On a host or guest OS that
  is "later" in the matrix, `wb setup` and every command say so; nothing
  half-works.

### Secret store on Linux without a desktop session

Secret Service needs a running keyring, which headless or minimal
systems may lack. Without one, `wb setup` refuses to store credentials
rather than writing them to a file. A TPM-sealed file is the candidate
alternative and needs its own spec.

## Languages per platform (N7)

- **Go everywhere** for the core, the guest agent and as much platform
  code as possible: HCS, Hyper-V sockets and named pipes on Windows;
  KVM-based VMMs, D-Bus, Landlock and seccomp on Linux.
- **macOS: Swift**, built with Xcode, for the Virtualization framework
  (`wb-vmd`) and notifications (native helper). The Keychain and Secure
  Enclave are reached from Go through a thin cgo shim inside `wb-proxyd`,
  because the credential must live in that process (S4, S12).
- **Windows: C# on .NET**, built with the `dotnet` CLI, for a native
  notification and tray helper when that is built. Nothing else on
  Windows is expected to need it.
- **Linux: no native language expected.** If one is ever needed, it gets
  a spec change first.

## Host prerequisites

Checked by `wb setup`, explained to the user, never changed silently:

| Host | Prerequisite | Who can do it |
|---|---|---|
| macOS | Apple Silicon, macOS 15+ | — |
| Windows | Virtual Machine Platform optional feature enabled (it is if WSL 2 is installed) | administrator, once |
| Windows | Permission to create VMs through HCS as a standard user (spike X11) | open |
| Linux | Read-write access to `/dev/kvm` (desktop sessions usually have it; otherwise the `kvm` group) | administrator, once |

If spike X11 shows that HCS needs administrator rights for every VM
operation, the Windows design needs a narrowly scoped system service.
That would change S12 for Windows and requires a spec change, not a
workaround.

## Guest operating systems

What differs per guest OS is confined to `wb-guestd` and to image
building:

| Concern | macOS guest | Linux guest | Windows guest |
|---|---|---|---|
| Image source | Apple restore image | Ubuntu LTS cloud or server image | user-supplied Windows 11 installation media |
| Unattended install | Wraith Box installer flow | automated install with a seed | unattended answer file |
| `wb-guestd` runs as | root LaunchDaemon | root systemd unit | SYSTEM service |
| Per-project users | local accounts | local accounts | local accounts |
| PTY | Unix PTY | Unix PTY | ConPTY |
| Toolchain manifest (F7) | Brewfile | package list (apt) | package list (winget) |
| CA trust (spec 009) | System keychain + toolchain variables | system trust store + toolchain variables | machine certificate store + toolchain variables |

Windows 11 requires a TPM 2.0 and Secure Boot. A host that cannot
provide a virtual TPM to the guest cannot run Windows guests (spikes X11,
X13).

## WSL (F16)

On a Windows host, `wb` inside a WSL 2 distribution is a **client** of
the Windows host's `wb-hostd`:

- **Detection.** Linux `wb` checks for WSL (`internal/platform`). In WSL
  it never starts services or VMs inside the distribution; the WSL
  distribution shares one kernel with every other distribution of the
  user, so it is not a sandbox (S1).
- **Channel.** `wb` starts the Windows `wb.exe relay` through WSL
  interop and speaks gRPC over its standard input and output. `wb.exe`
  connects to `wb-hostd` over its named pipe as the Windows user, so
  authentication is the same as native use. This needs no networking
  configuration in WSL. A Hyper-V socket channel is a possible later
  optimisation (spike X12).
- **Terminal.** The WSL-side `wb` owns the terminal and relays it inside
  the gRPC stream, so Windows console behaviour does not apply.
- **Repository.** The repository stays in the WSL filesystem. The
  WSL-side `wb` serves the read-only `upload-pack` side of the git
  transport (spec 008) locally and tunnels it, so the Windows side never
  reads the repository over the cross-OS file share. `wb land` fetches
  from the Windows-side landing repository over the same channel.
- **Project identity** includes the distribution name (spec 005), so the
  same path in two distributions is two projects.
- **Requirement.** The Windows side must have Wraith Box installed and
  `wb setup` done; WSL-side `wb` explains this if `wb.exe` is missing.

## CI

Go is built, linted, vetted and tested natively on macOS, Ubuntu and
Windows runners from the start (spec 002). Swift runs on macOS only. A
.NET job is added with the first .NET component.

**Status:** Draft
