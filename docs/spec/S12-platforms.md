# S12 - Platforms

**Purpose:** Which host and guest operating systems Wraith Box targets,
how platform-specific code is isolated, what each platform uses, and how
`wb` works inside WSL.

**Requirements:** FR16-wsl, FR17-same-everywhere, SEC01-separate-kernel, SEC02-no-host-fs-share, SEC04-no-guest-secrets, SEC05-default-deny, SEC12-least-privilege, NFR04-host-platforms, NFR05-two-macos-vms, NFR07-maintainability, V1-01-macos-first, V1-03-win-linux-later.

## Platform matrix

| Host ↓ / Guest → | macOS | Linux (Ubuntu LTS first) | Windows 11 |
|---|---|---|---|
| **macOS 15+, Apple Silicon** | **v1** | later | later, blocked on spike X13-windows-guests |
| **Windows 11 Home / Pro** | not possible | later | later, blocked on spike X11-windows-host |
| **Linux, Ubuntu LTS first** | not possible | later | later |

- macOS guests need Apple hardware, technically and by license.
- Guests use the host's CPU architecture: arm64 on Apple Silicon; x86-64
  or arm64 on Windows and Linux. No instruction-set emulation.
- Windows guests need a Windows license supplied by the user. Wraith Box
  never ships or downloads a Windows image itself.
- "Windows 11" means current Home and Pro editions. Features available
  only on Pro or Enterprise (the Hyper-V management stack, Windows
  Sandbox) are not relied on.
- Linux distributions other than Ubuntu LTS, as host or guest, are
  considered after Ubuntu works; the Linux code avoids Ubuntu-only
  assumptions where that is free.

The code encodes this matrix in `internal/platform` and tests it.

## Isolation of platform code

The cross-platform core never asks which OS it is on. It talks to a
small set of interfaces, each with one implementation per host OS:

| Interface | Responsibility | macOS | Windows | Linux |
|---|---|---|---|---|
| **VM provider** (`wb-vmd`) | Create, start, stop, save, restore VMs; disks; devices | Swift, Virtualization framework | Go, Host Compute Service (HCS) | Go, supervising a VMM over KVM (spike X10-linux-hypervisor; X16-openshell-linux checks OpenShell's VM driver first) |
| **Host-guest socket** | Byte streams between `wb-hostd` and `wb-guestd` | virtio-vsock. In the guest, `AF_VSOCK` through `golang.org/x/sys/unix`. On the host, `wb-vmd` passes each connection to `wb-hostd` as a Unix socket descriptor from Virtualization, with its ports. `wb-hostd` routes only on the destination port. The host opens every connection, and `wb-vmd` doesn't register a host listener (S04-architecture, B29-vsock-handoff) | Hyper-V sockets | virtio-vsock |
| **Packet transport** | Guest Ethernet frames to and from `wb-netd`, without host privileges | file-handle network attachment (datagram socketpair). `wb-vmd` passes the host end to `wb-hostd`, which passes it to that VM's `wb-netd`, a new one for each start or restore (S07-egress-gateway, X18-vsock-handoff) | spike X11-windows-host | spike X10-linux-hypervisor |
| **Disk cloning** | Copy-on-write images | APFS clones | VHDX differencing disks | qcow2 backing files, or reflinks where the filesystem supports them |
| **Local IPC** | Host gRPC endpoints, peer identity | Unix sockets, peer credentials | named pipes, user-SID ACL, client process id | Unix sockets, `SO_PEERCRED` |
| **Secret store** | Holds every credential. Each item is readable only by the processes S15-least-privilege declares for it | Keychain | Credential Manager (DPAPI, per user) | Secret Service (D-Bus); see below |
| **Hardware key** | Non-exportable CA signing key | Secure Enclave | TPM via the CNG platform crypto provider | TPM 2.0 where present |
| **Service manager** | Run `wb-hostd` per user, at login and again whenever it exits. `wb-hostd` supervises the other daemons (S04-architecture, "Supervision and failure") | LaunchAgent | per-user logon task | systemd user unit |
| **Self-sandbox** | Confine our own daemons (S04-architecture) | Seatbelt profile applied in process at start with `sandbox_init_with_parameters`, called without cgo. A confined process can't confine its children further, so `wb-hostd` starts the other daemons through `wb-launcher`, which it starts before it confines itself (X23-sandboxed-daemons). `wb-vmd` confines itself the same way, with the Virtualization service, the VM bundles and the framework's sandbox extensions in its profile (X25-vmd-sandbox) | restricted token, job objects, AppContainer where it fits | Landlock and seccomp (no unprivileged user namespaces needed) |
| **Notifications** | Approval prompts with actions | native helper (Swift) | native helper (see languages) | freedesktop notifications over D-Bus from Go |
| **Downloaded-file marker** | Files leaving the guest other than via git | quarantine attribute | Mark of the Web | none (documented) |
| **Paths** | `<config>`, `<data>`, `<logs>` of S04-architecture | `~/.config/wraithbox`, `~/Library/Application Support/WraithBox`, `~/Library/Logs/WraithBox` | `%APPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox\Logs` | `$XDG_CONFIG_HOME/wraithbox`, `$XDG_DATA_HOME/wraithbox`, `$XDG_STATE_HOME/wraithbox/logs` |

Rules:

- **Go interfaces in `internal/platform`**, implemented in files with
  `_darwin.go`, `_windows.go`, and `_linux.go` suffixes. Shared code never
  branches on `runtime.GOOS`.
- **A native component is a separate process** with a gRPC contract,
  used only where Go cannot reasonably call the platform API. It makes no
  policy decisions and holds no secrets. The one exception is that
  `wb-vmd` gets an image build's provisioning password on a pipe until
  its start call returns (S15-least-privilege).
- **Every platform implementation has the same conformance tests** (S11-verification-and-spikes); a platform is not "supported" until its conformance suite passes.
- **Unimplemented platforms fail clearly.** On a host or guest OS that
  is "later" in the matrix, `wb setup` and every command say so; nothing
  half-works.

### Host-guest socket: keeping `wb-guestd`'s endpoint

On macOS and Linux guests, `wb-guestd` listens on a vsock port below
1024, which a project user can't bind (S06-vm-lifecycle,
X27-vsock-confinement). Open for other guests and hosts:

- **A Windows guest on a macOS or Linux host** reaches the host over
  virtio-vsock through a Windows guest driver. Whether that driver
  keeps low ports for privileged processes is unknown
  (X13-windows-guests).
- **Any guest on a Windows host** uses Hyper-V sockets, which name a
  service by GUID instead of a port number. How the guest stops a
  project user from registering `wb-guestd`'s service while
  `wb-guestd` restarts is undecided (X11-windows-host).

### Secret store on Linux without a desktop session

Secret Service needs a running keyring, which headless or minimal
systems may lack. Without one, `wb setup` refuses to store credentials
rather than writing them to a file. A TPM-sealed file is the candidate
alternative and needs its own spec.

## Languages per platform (NFR07-maintainability)

- **Go everywhere** for the core, the guest agent, and as much platform
  code as possible: HCS, Hyper-V sockets, and named pipes on Windows;
  KVM-based VMMs, D-Bus, Landlock, and seccomp on Linux.
- **macOS: Swift**, built with Xcode, for the Virtualization framework
  (`wb-vmd`) and notifications (native helper). The Keychain and Secure
  Enclave are reached from Go through a thin cgo shim, linked into each
  process that S15-least-privilege declares store access for, so no
  process gets a credential through another (SEC04-no-guest-secrets, SEC12-least-privilege).
- **Windows: C# on .NET**, built with the `dotnet` CLI, for a native
  notification and tray helper when that is built. Nothing else on
  Windows is expected to need it.
- **Linux: no native language expected.** If one is ever needed, it gets
  a spec change first.

## Host prerequisites

Checked by `wb setup`, explained to the user, never changed silently:

| Host | Prerequisite | Who can do it |
|---|---|---|
| macOS | Apple Silicon, macOS 15+ | n/a (hardware and OS version) |
| Windows | Virtual Machine Platform optional feature enabled (it is if WSL 2 is installed) | administrator, once |
| Windows | Permission to create VMs through HCS as a standard user (spike X11-windows-host) | open |
| Linux | Read-write access to `/dev/kvm` (desktop sessions usually have it; otherwise the `kvm` group) | administrator, once |

If spike X11-windows-host shows that HCS needs administrator rights for every VM
operation, the Windows design needs a narrowly scoped system service.
That would change SEC12-least-privilege for Windows and requires a spec change, not a
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
| Toolchain manifest (FR07-toolchain-manifest) | Brewfile | package list (apt) | package list (winget) |
| CA trust (S09-policy-credentials-audit) | toolchain variables. System keychain: depends on I185 (X04-tls-inspection) | system trust store + toolchain variables | machine certificate store + toolchain variables |
| Guest confinement (S13-guest-confinement) | Seatbelt profiles; Network Extension flow labels (X14-flow-attribution); Endpoint Security (X15-endpoint-security) | Landlock and seccomp, as in OpenShell's sandbox runtime (X16-openshell-linux) | to be decided |

Windows 11 requires a TPM 2.0 and Secure Boot. A host that cannot
provide a virtual TPM to the guest cannot run Windows guests (spikes X11-windows-host,
X13-windows-guests).

## WSL (FR16-wsl)

On a Windows host, `wb` inside a WSL 2 distribution is a **client** of
the Windows host's `wb-hostd`:

- **Detection.** Linux `wb` checks for WSL (`internal/platform`). In WSL
  it never starts services or VMs inside the distribution; the WSL
  distribution shares one kernel with every other distribution of the
  user, so it is not a sandbox (SEC01-separate-kernel).
- **Channel.** `wb` starts the Windows `wb.exe relay` through WSL
  interop and speaks gRPC over its standard input and output. `wb.exe`
  connects to `wb-hostd` over its named pipe as the Windows user, so
  authentication is the same as native use. This needs no networking
  configuration in WSL. A Hyper-V socket channel is a possible later
  optimization (spike X12-wsl-channel).
- **Terminal.** The WSL-side `wb` owns the terminal and relays it inside
  the gRPC stream, so Windows console behavior does not apply.
- **Repository.** The repository stays in the WSL filesystem, and
  `wb-hostd` never reads it. `wb.exe` fetches the selected refs into
  the Windows-side `export.git` as its only writer, from a
  `git upload-pack` that it starts in the distribution through
  `wsl.exe`, so `wb-hostd` is never the fetch client. `wb land` and
  `wb diff` fetch in the distribution from a `git upload-pack` on the
  Windows-side landing repository, which `wb.exe` runs after it checks
  that repository (S08-workspace-and-git, "WSL").
- **Project identity** includes the distribution name (S05-cli), so the
  same path in two distributions is two projects.
- **Requirement.** The Windows side must have Wraith Box installed and
  `wb setup` done; WSL-side `wb` explains this if `wb.exe` is missing.

## CI

Go is built, linted, vetted, and tested natively on macOS, Ubuntu, and
Windows runners from the start (S02-toolchain). Swift runs on macOS only. A
.NET job is added with the first .NET component.

**Status:** Draft
