# X25 - Self-sandboxed wb-vmd

**Purpose:** Find out whether `wb-vmd` can confine itself with a
Seatbelt profile at start and still run a macOS VM, and which devices
that profile keeps `wb-vmd` from configuring.

**Requirements:** SEC12-least-privilege, SEC02-no-host-fs-share,
SEC05-default-deny, and the "Each host daemon is self-sandboxed"
decision of S04-architecture

**Brief:** B64-vmd-sandbox

## For review

- **Decides:** how `wb-vmd` confines itself on macOS, its profile, and
  which parts of the device set the profile enforces and which only
  `wb-vmd`'s own code enforces.
- **You are approving:** `wb-vmd` applies a Seatbelt profile in
  process, as the Go daemons do (X23-sandboxed-daemons). The profile
  allows the VM bundles under `<data>/vms`, the Virtualization service,
  and a short list of system reads and sandbox extensions, and denies
  the rest. Under it, a confined `wb-vmd` installs, boots, saves and
  restores a macOS VM and hands out vsock descriptors. S04-architecture,
  S06-vm-lifecycle and S12-platforms say so (changed in this pull
  request).
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host): kept, and `wb-vmd` now has a measured profile.
  SEC02-no-host-fs-share (no host directory in the guest): kept, now
  enforced twice, by `wb-vmd`'s code and by the profile.
  SEC05-default-deny (default-deny egress): kept, but only by
  `wb-vmd`'s code. The profile can't stop `wb-vmd` from giving the
  guest a NAT network, which bypasses `wb-netd` and `wb-proxyd`. No
  control is weakened.
- **Assumed:** that Apple keeps `sandbox_init_with_parameters` working,
  as in X23-sandboxed-daemons. That the profile holds for the signed
  release binary with the hardened runtime: the spike was ad-hoc signed
  without it. That the SPICE clipboard device can't reach the host
  clipboard under the profile: the lookup of the pasteboard service is
  denied, but the guest had no SPICE agent to show the effect.
- **Open decisions:** none.
- **Brief:** B64-vmd-sandbox

## Question

From X00-index: can `wb-vmd` confine itself at start with a Seatbelt
profile that allows the Virtualization framework, its VM bundles under
`<data>/vms` and nothing else, and still create, start, save, and
restore a macOS VM? If not, what does it need instead?

From the maintainer's comment on I64 (the security review of PR87):
which device types can the profile keep `wb-vmd` from configuring, so
that S04-architecture can say whether the device set is enforced by the
sandbox or only by `wb-vmd`'s code?

## Answer

**Yes, with conditions.** A Swift `wb-vmd` stand-in applied a profile
to itself with `sandbox_init_with_parameters`, then installed macOS from
a restore image, booted, saved, restored, and passed a vsock descriptor
out with `SCM_RIGHTS`. Besides the bundles and the Virtualization
service, the profile allows a few system reads and the sandbox
extensions the framework hands to that service. It doesn't allow the
user's other files, the network, or starting programs. App Sandbox and dropping confinement aren't needed. The
conditions:

1. **Resolve the cache directory before confining.** The framework
   asks for the per-user cache directory
   (`confstr(_CS_DARWIN_USER_CACHE_DIR)`), and that lookup goes to the
   `com.apple.bsd.dirhelper` service. `wb-vmd` calls `confstr` once
   before it confines itself, and libSystem keeps the answer. Without
   that call, start fails with "Failed to retrieve cache directory".
   The profile then doesn't need a dirhelper rule.
2. **Allow the CPU name.** A state saved by a process that can't read
   `machdep.cpu.brand_string` restores only in a process that can't
   read it either, and the other way round. The restore fails with
   "invalid argument". A boot doesn't need the rule, so it's easy to
   drop by mistake. With it, states move freely between confined and
   unconfined runs.
3. **Keep the framework's own bundle readable.** Without read access to
   `Virtualization.framework`, a configuration error can't load its
   message text and the process ends in an uncaught exception instead
   of returning the error. A boot doesn't need the rule otherwise.
4. **Installing needs three more rules,** for the restore image: read
   it, issue a read extension for it, and look up the
   `com.apple.Virtualization.Installation` service. They belong in the
   profile only while `wb-vmd` installs an image (X17-image-build).
5. **The profile can't keep a NAT network from the guest.** It enforces
   part of the device set and not the rest (below). Which network a VM
   gets is up to `wb-vmd`'s code, and SEC05-default-deny depends on it.

### What the profile enforces in the device set

Each device was added to the VM configuration, pointing at a file or
directory outside the bundle, once with `wb-vmd` confined and once
unconfined. Unconfined, every device below except the bridged network
starts.

| Device | Confined | Enforced by |
|---|---|---|
| Shared directory (virtio-fs) | refused at start. Outside the bundle the process can't read the directory, and inside it the `fuse` sandbox extension is denied | profile, and `wb-vmd`'s code |
| Host microphone (audio input) | refused at start: the `audio-input` sandbox extension is denied | profile, and `wb-vmd`'s code |
| Extra disk image outside `<data>/vms` | refused: the process can't read it (and ends in an uncaught C++ exception) | profile, and `wb-vmd`'s code |
| Serial port that writes to a file outside `<data>/vms` | refused at configuration: "Operation not permitted" | profile, and `wb-vmd`'s code |
| SPICE agent with clipboard sharing | starts. The pasteboard lookup is denied, so the process can't reach the host clipboard (not checked from the guest) | profile (inferred), and `wb-vmd`'s code |
| Bridged network | refused: needs the `com.apple.vm.networking` entitlement, which `wb-vmd` doesn't get | code signing, and `wb-vmd`'s code |
| **NAT network** | **starts, and the guest reached `https://www.apple.com/` (HTTP 200)** | **only `wb-vmd`'s code** |
| Disks and files inside `<data>/vms` | allowed | only `wb-vmd`'s code |
| Devices on a descriptor `wb-vmd` holds: file-handle network, serial port on a file handle | allowed | only `wb-vmd`'s code |

Unconfined, the guest mounted the shared directory and read the
secret file in it, saw the marker of the extra disk, and reached the
internet through NAT (`guest-unconfined-check.txt`). Confined, with
NAT only, it reached the internet, and `mount_virtiofs` didn't find a
share (`guest-confined-nat-check.txt`).

The NAT attachment doesn't need any right in `wb-vmd`'s process: vmnet
runs in Apple's Virtualization service. A NAT network would give the
guest a path to the internet that skips `wb-netd` and `wb-proxyd`, and
only `wb-vmd`'s code keeps it out. `wb-vmd` doesn't read guest bytes
(S04-architecture), so getting a NAT network attached takes a bug in
`wb-hostd`'s requests or in `wb-vmd` itself.
S04-architecture now has `wb-vmd` check each configuration against its
fixed device set before every start and restore.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0.
Guest: macOS 27.0.1, 4 vCPU, 4 GiB, the X18-vsock-handoff configuration,
with the X18 guest daemon answering gRPC on vsock port 1024. The spike
code and its recorded output are on the spike branch:
[spikes/x25-vmd-sandbox at cc58d25](https://github.com/wraithbox/wraithbox/tree/cc58d254fb218a9d219579a565ee47f8b591caa6/spikes/x25-vmd-sandbox)
(`results/` holds the output of each run, and the Sandbox log lines it
caused).

### How the profile was found

1. A run under `(allow default (with report))` logged every operation
   the process did during a boot, a save, and a restore
   (`discover-*.sandbox.txt`).
2. Starting from `(deny default)` with the bundle and the
   Virtualization service, each run added what the last one was
   denied, until boot, save and restore worked (`vmd-v1` to `vmd-v5`).
3. Three ablation rounds booted once per rule without that rule
   (`ablate-*.txt`). Dropped: the disk image service, `vfs.disk-space`,
   the ICU data file, read and write on the cache directory, the
   read-write extension on the bundle, `kern.maxfilesperproc`, and the
   CPU name, which step 4 put back.
4. Restores across confined and unconfined saves found the CPU name
   rule (`matrix-*`, `addon-round1.txt`, `addon-round2.txt`).

### The profile

`BUNDLE` is `<data>/vms`: a run with the parent of all bundles as the
parameter booted the same (`final-vms-root`). `CACHE` is the cache
directory from `confstr`, resolved (`/private/var/folders/...`),
without the trailing slash.

```scheme
(version 1)
(deny default)
(allow file-read* file-write* (subpath (param "BUNDLE")))
(allow mach-lookup (xpc-service-name "com.apple.Virtualization.VirtualMachine"))
(allow sysctl-read (sysctl-name "kern.hv_support" "hw.memsize" "machdep.cpu.brand_string"))
(allow file-read* (literal "/System/Library/CoreServices/SystemVersion.plist"))
(allow file-read* (subpath "/System/Library/Frameworks/Virtualization.framework"))
(allow file-read-metadata (literal "/var") (literal "/private/var/folders"))
(allow generic-issue-extension
  (extension-class
    "com.apple.virtualization.extension.avp.rtc"
    "com.apple.virtualization.extension.fp"
    "com.apple.virtualization.extension.io-surface"
    "com.apple.virtualization.extension.paravirtualized-graphics"
    "com.apple.virtualization.extension.strong-identity"
    "com.apple.virtualization.extension.usb-hci"
    "com.apple.virtualization.extension.videotoolbox"))
(allow file-issue-extension
  (require-all (subpath (param "BUNDLE")) (extension-class "com.apple.app-sandbox.read")))
(allow file-issue-extension
  (require-all (subpath (param "CACHE")) (extension-class "com.apple.app-sandbox.read-write")))
```

What each part is for, and what happened without it:

| Rule | Without it |
|---|---|
| read and write `BUNDLE` | the bundle's files can't be read |
| the Virtualization service | "The virtual machine failed to start" |
| `kern.hv_support` | "Virtualization is not available on this hardware" |
| `hw.memsize` | `memorySize` is greater than `maximumAllowedMemorySize` |
| `machdep.cpu.brand_string` | boots, but saved states don't move between this profile and an unconfined process (condition 2) |
| `SystemVersion.plist` | "Unsupported `hardwareModel`" |
| `Virtualization.framework` | boots, but a configuration error ends in an uncaught exception (condition 3) |
| metadata of `/var` and `/private/var/folders` | "Failed to retrieve cache directory" |
| each of the 7 extension classes | "Failed to issue ... sandbox extension", one per class |
| read extension in `BUNDLE` | "Failed to issue sandbox extension for auxiliary storage device" |
| read-write extension in `CACHE` | "Failed to issue sandbox extension for ParavirtualizedGraphics cache directory" |

The extensions are how the framework passes access to its service
process: the auxiliary storage file, the graphics caches, and the
device classes. The disks are opened in `wb-vmd`'s own process.
Writing the saved state doesn't need more than the bundle rule.

Denied in the confined runs, with no effect on the VM: the disk
image service lookup, `mach-task-name` on the service process,
`kern.maxfilesperproc`, `vfs.disk-space`, the `SystemVersion.bundle`
strings, and the syslog socket.

Right after confining, the stand-in tried to read `/etc/passwd` and
`~/.ssh/known_hosts`, write in `/tmp`, connect over TCP to loopback,
and start `/usr/bin/true`. Each was denied, in every confined run.

### Boot, save, restore, install, hand-off

| Run | Result |
|---|---|
| `sandbox_init_with_parameters` | 5.9 ms (p50 of the 73 calls across all runs, 5.1 to 15.2 ms) |
| cold boot to the guest daemon answering on vsock | 9 to 16 s, confined and unconfined alike |
| save, confined | 3.0 to 3.7 s |
| restore from a fresh clone, confined | 3.40 s p50, 3.57 s max. vsock answers at 3.60 s p50 (10 runs) |
| restore from a fresh clone, unconfined | 3.40 s p50, 3.53 s max. vsock answers at 3.60 s p50 (10 runs) |
| restore, save, restore again, all confined | works |
| state saved confined, restored unconfined, and the other way round | works with condition 2. Without it, "invalid argument" in all 9 tries |
| install from the restore image, confined, with the 3 install rules | 240 s, completes |
| pass a vsock descriptor with `SCM_RIGHTS` over a socketpair made before confining, and speak HTTP/2 to the guest on the received copy | the guest answers with a SETTINGS frame |

Confinement doesn't make a restore measurably slower. The installed VM
wasn't booted: it had no guest daemon to answer.

## What it means for the specs

- **S04-architecture, "Each host daemon is self-sandboxed":** a
  `wb-vmd` item with the profile's shape, condition 1 as what `wb-vmd`
  does before confining, and the install rules (changed in this pull
  request).
- **S04-architecture, "The VM provider passes descriptors, not
  bytes":** which devices the profile enforces, that NAT and the other
  devices in the table are only enforced by `wb-vmd`'s code, and that
  `wb-vmd` checks every configuration against its fixed device set and
  refuses and logs anything else (changed in this pull request).
- **S06-vm-lifecycle, "Warm start":** "invalid argument" also comes
  from a state saved under a `wb-vmd` profile that read different host
  facts, which the existing rules already treat as permanent (changed
  in this pull request).
- **S12-platforms, "Self-sandbox" row:** names X25-vmd-sandbox next to
  X23-sandboxed-daemons for macOS (changed in this pull request).
- Cloning a base image from `<data>/images` into a bundle isn't in this
  profile. If `wb-vmd` does it, the profile also reads
  `<data>/images`. Who clones is for the implementation.

## Not covered

- The release signing: hardened runtime and a Developer ID signature.
  The spike was ad-hoc signed with `com.apple.security.virtualization`
  and no hardened runtime.
- The SPICE clipboard seen from inside the guest. The guest has no
  SPICE agent.
- Two VMs at once under one `wb-vmd`. Each run had one VM. The profile
  has no rule that names one bundle.
- App Sandbox through entitlements. The Seatbelt profile works, so it
  wasn't needed.
- Other devices: USB mass storage (the same file rules as a disk
  apply), Rosetta directory shares (a directory share, so the `fuse`
  extension applies, untested), and network block devices.

**Status:** Answered 2026-10-06: yes, with conditions. Only `wb-vmd`'s code keeps a NAT network from the guest
