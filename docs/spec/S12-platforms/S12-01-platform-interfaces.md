# S12-01 - Platform Interfaces

**Purpose:** How platform-specific code is isolated behind interfaces, and
what each host OS uses for each.

## Design

The cross-platform core never asks which OS it is on. It talks to a
small set of interfaces. Each interface has one implementation per host
OS:

| Interface | Responsibility | macOS | Windows | Linux |
|---|---|---|---|---|
| **VM provider** (`wb-vmd`) | Create, start, stop, save, restore VMs; disks; devices | Swift, Virtualization framework | Go, Host Compute Service (HCS) | Go, supervising a VMM over KVM (spike X10-linux-hypervisor; X16-openshell-linux checks OpenShell's VM driver first) |
| **Host-guest socket** | Byte streams between `wb-hostd` and `wb-guestd` | virtio-vsock ("Host-guest socket on macOS") | Hyper-V sockets | virtio-vsock |
| **Packet transport** | Guest Ethernet frames to and from `wb-netd`, without host privileges | file-handle network attachment ("Packet transport on macOS") | spike X11-windows-host | spike X10-linux-hypervisor |
| **Disk cloning** | Copy-on-write images | APFS clones | VHDX differencing disks | qcow2 backing files, or reflinks where the filesystem supports them |
| **Local IPC** | Host gRPC endpoints, peer identity | Unix sockets, peer credentials | named pipes, user-SID ACL, client process id | Unix sockets, `SO_PEERCRED` |
| **Secret store** | Holds every credential. Each item is readable only by the processes S15-least-privilege declares for it | Keychain | Credential Manager (DPAPI, per user) | Secret Service (D-Bus); see "Secret store on Linux without a desktop session" |
| **Hardware key** | Non-exportable CA signing key (S09-06-ca-keys, "Key") | Secure Enclave, a key kept as its wrapped form in a store item | TPM via the CNG platform crypto provider | TPM 2.0 where present, else a software key in the secret store |
| **Service manager** | Run `wb-hostd` per user, at login and again whenever it exits. `wb-hostd` supervises the other daemons (S04-07-supervision) | LaunchAgent `nl.wraithbox.hostd`, which `wb setup` writes (S17-install-and-upgrade) | per-user logon task | systemd user unit |
| **Self-sandbox** | Confine our own daemons (S04-architecture) | Seatbelt ("Self-sandbox on macOS") | restricted token, job objects, AppContainer where it fits | Landlock and seccomp (no unprivileged user namespaces needed) |
| **Image build downloads** | Fetch what an image build needs from the internet: `wb-download` only, never `wb-hostd` (S17-03-downloads) | "Image build downloads on macOS" | no Windows image download: the user supplies it (S12-platforms, "What the matrix assumes"). The same program and host rules, confined with the Self-sandbox row's mechanism, specified when Windows hosts are built | the same program and host rules, confined with Landlock and seccomp, specified when Linux hosts are built |
| **Notifications** | Approval prompts with actions | native helper (Swift) | native helper (S12-02-languages-and-ci) | freedesktop notifications over D-Bus from Go |
| **Downloaded-file marker** | Files leaving the guest other than via git | quarantine attribute | Mark of the Web | none (documented) |
| **Paths** | `<config>`, `<data>`, `<logs>` of S04-architecture | `~/.config/wraithbox`, `~/Library/Application Support/WraithBox`, `~/Library/Logs/WraithBox` | `%APPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox\Logs` | `$XDG_CONFIG_HOME/wraithbox`, `$XDG_DATA_HOME/wraithbox`, `$XDG_STATE_HOME/wraithbox/logs` |

### Host-guest socket on macOS

- In the guest, `wb-guestd` uses `AF_VSOCK` through
  `golang.org/x/sys/unix`.
- On the host, `wb-vmd` passes each connection to `wb-hostd` as a Unix
  socket descriptor from Virtualization, with its ports.
- `wb-hostd` routes only on the destination port.
- The host opens every connection. `wb-vmd` doesn't register a host
  listener (S04-architecture, B29-vsock-handoff).

### Packet transport on macOS

The network attachment uses a datagram socketpair (X18-vsock-handoff).

1. `wb-vmd` passes the host end to `wb-hostd`.
2. `wb-hostd` passes it to that VM's `wb-netd` (S07-egress-gateway). A
   new `wb-netd` starts for each start or restore.

### Self-sandbox on macOS

Each daemon applies a Seatbelt profile in its own process at start,
with `sandbox_init_with_parameters`, called without cgo.

A confined process can't confine its children further. So `wb-hostd`
starts the other daemons through `wb-launcher`, which it starts before
it confines itself (X23-sandboxed-daemons).

A profile that reaches the Keychain allows two Mach services,
`com.apple.securityd.xpc` and `com.apple.ctkd.token-client`. One that
checks certificates allows `com.apple.trustd.agent` and the metadata of
its own bundle directory (S04-03-self-sandboxing, "`wb-proxyd` and the
secret store on macOS").

`wb-vmd` confines itself the same way, one per VM (X25-vmd-sandbox).
Its profile has:

- the Virtualization service;
- its VM's bundle;
- the framework's sandbox extensions.

### Image build downloads on macOS

`wb-download` fetches:

- the restore image from Apple's catalog;
- Claude Code from `downloads.claude.ai`.

It runs with the network part of the `wb-proxyd` Seatbelt profile from
X23-sandboxed-daemons (I61), limited to port 443. `wb-launcher` sets a
file size limit for it.

### Rules

- **Go interfaces in `internal/platform`**, implemented in files with
  `_darwin.go`, `_windows.go`, and `_linux.go` suffixes. Shared code
  never branches on `runtime.GOOS`.
- **A native component is a separate process** with a gRPC contract. It
  is used only where Go can't reasonably call the platform API. It
  doesn't make policy decisions or hold secrets. The one exception:
  `wb-vmd` gets an image build's provisioning password on a pipe, until
  its start call returns (S15-least-privilege).
- **Every platform implementation has the same conformance tests**
  (S11-verification-and-spikes). A platform is not "supported" until
  its conformance suite passes.
- **Unimplemented platforms fail clearly.** On a host or guest OS that
  is "later" in the matrix, `wb setup` and every command say so. Nothing
  half-works.

### Host-guest socket: keeping `wb-guestd`'s endpoint

On macOS and Linux guests, `wb-guestd` listens on a vsock port below
1024, which a project user can't bind (S06-vm-lifecycle,
X27-vsock-confinement). For other guests and hosts, spikes decide it:

- **A Windows guest on a macOS or Linux host** reaches the host over
  virtio-vsock through a Windows guest driver. X13-windows-guests
  checks whether that driver keeps low ports for privileged processes.
- **Any guest on a Windows host** uses Hyper-V sockets. They name a
  service by GUID instead of a port number. X11-windows-host decides
  how the guest stops a project user from registering `wb-guestd`'s
  service while `wb-guestd` restarts.

### Secret store on Linux without a desktop session

Secret Service needs a running keyring, which headless or minimal
systems may lack. Without one, `wb setup` refuses to store credentials.

A TPM-sealed file is the candidate alternative. It needs its own spec.

## Decisions

### No credentials in a file without a keyring

`wb setup` refuses to store credentials on Linux without a keyring.

- **Rejected:** writing them to a file.

**Status:** Draft
