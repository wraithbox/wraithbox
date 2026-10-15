# S12-01 - Platform Interfaces

**Purpose:** How platform-specific code is isolated behind interfaces, and
what each host OS uses for each.

## Design

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
| **Hardware key** | Non-exportable CA signing key (S09-05-ca-keys, "Key") | Secure Enclave, a key kept as its wrapped form in a store item | TPM via the CNG platform crypto provider | TPM 2.0 where present, else a software key in the secret store |
| **Service manager** | Run `wb-hostd` per user, at login and again whenever it exits. `wb-hostd` supervises the other daemons (S04-07-supervision) | LaunchAgent `nl.wraithbox.hostd`, which `wb setup` writes (S17-install-and-upgrade) | per-user logon task | systemd user unit |
| **Self-sandbox** | Confine our own daemons (S04-architecture) | Seatbelt profile applied in process at start with `sandbox_init_with_parameters`, called without cgo. A confined process can't confine its children further, so `wb-hostd` starts the other daemons through `wb-launcher`, which it starts before it confines itself (X23-sandboxed-daemons). `wb-vmd` confines itself the same way, one per VM, with the Virtualization service, its VM's bundle and the framework's sandbox extensions in its profile (X25-vmd-sandbox) | restricted token, job objects, AppContainer where it fits | Landlock and seccomp (no unprivileged user namespaces needed) |
| **Image build downloads** | Fetch what an image build needs from the internet: `wb-download` only, never `wb-hostd` (S17-install-and-upgrade, "Downloads") | the restore image from Apple's catalog and Claude Code from `downloads.claude.ai`, with the network part of the `wb-proxyd` Seatbelt profile from X23-sandboxed-daemons (I61), limited to port 443, and a file size limit that `wb-launcher` sets | no Windows image download: the user supplies it (above). The same program and host rules, confined with the Self-sandbox row's mechanism, specified when Windows hosts are built | the same program and host rules, confined with Landlock and seccomp, specified when Linux hosts are built |
| **Notifications** | Approval prompts with actions | native helper (Swift) | native helper (see languages) | freedesktop notifications over D-Bus from Go |
| **Downloaded-file marker** | Files leaving the guest other than via git | quarantine attribute | Mark of the Web | none (documented) |
| **Paths** | `<config>`, `<data>`, `<logs>` of S04-architecture | `~/.config/wraithbox`, `~/Library/Application Support/WraithBox`, `~/Library/Logs/WraithBox` | `%APPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox`, `%LOCALAPPDATA%\WraithBox\Logs` | `$XDG_CONFIG_HOME/wraithbox`, `$XDG_DATA_HOME/wraithbox`, `$XDG_STATE_HOME/wraithbox/logs` |

### Rules

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

**Status:** Draft
