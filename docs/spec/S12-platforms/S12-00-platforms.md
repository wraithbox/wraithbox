# S12 - Platforms

**Purpose:** Which host and guest operating systems Wraith Box targets,
how platform-specific code is isolated, what each platform uses, and how
`wb` works inside WSL.

**Requirements:** FR16-wsl, FR17-same-everywhere, SEC01-separate-kernel, SEC02-no-host-fs-share, SEC04-no-guest-secrets, SEC05-default-deny, SEC12-least-privilege, NFR04-host-platforms, NFR05-two-macos-vms, NFR07-maintainability, V1-01-macos-first, V1-03-win-linux-later.

**Tests:** C05-credentials-and-store

Briefs: B29-vsock-handoff

## Design

### Platform matrix

| Host ↓ / Guest → | macOS | Linux (Ubuntu LTS first) | Windows 11 |
|---|---|---|---|
| **macOS 27+, Apple Silicon** | **v1** | later | later, blocked on spike X13-windows-guests |
| **Windows 11 Home / Pro** | not possible | later | later, blocked on spike X11-windows-host |
| **Linux, Ubuntu LTS first** | not possible | later | later |

The code encodes this matrix in `internal/platform` and tests it.

### What the matrix assumes

- macOS guests need Apple hardware, technically and by license.
- A macOS host needs macOS 27 or later (NFR04-host-platforms).
  `wb image build` boots the new guest with the guest provisioning
  options, which are new in macOS 27 (S06-02-image-build). Building
  images on a newer Mac and shipping them to older hosts was rejected:
  it needs image signing and provenance that don't exist, and Apple's
  license may not allow it (I143).
- Guests use the host's CPU architecture, with no instruction-set
  emulation:
  - arm64 on Apple Silicon;
  - x86-64 or arm64 on Windows and Linux.
- Windows guests need a Windows license that the user supplies. Wraith
  Box never ships or downloads a Windows image itself.
- "Windows 11" means the current Home and Pro editions. Wraith Box
  doesn't rely on features that only Pro or Enterprise have, such as the
  Hyper-V management stack or Windows Sandbox.
- Linux distributions other than Ubuntu LTS, as host or guest, are
  considered after Ubuntu works. The Linux code avoids Ubuntu-only
  assumptions where that is free.

### Pages

| Page | What it covers |
|---|---|
| S12-01-platform-interfaces | The interfaces that isolate platform code, what each host OS uses for each, and the rules for platform code |
| S12-02-languages-and-ci | The language used on each platform, and CI per platform |
| S12-03-host-prerequisites | What `wb setup` checks on each host, and signing of development builds on macOS |
| S12-04-guest-operating-systems | What differs per guest OS |
| S12-05-wsl | How `wb` works inside WSL |

**Status:** Draft
