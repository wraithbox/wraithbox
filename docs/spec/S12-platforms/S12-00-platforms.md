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

### Pages

| Page | What it covers |
|---|---|
| S12-01-platform-interfaces | The interfaces that isolate platform code, what each host OS uses for each, and the rules for platform code |
| S12-02-languages-and-ci | The language used on each platform, and CI per platform |
| S12-03-host-prerequisites | What `wb setup` checks on each host, and signing of development builds on macOS |
| S12-04-guest-operating-systems | What differs per guest OS |
| S12-05-wsl | How `wb` works inside WSL |

**Status:** Draft
