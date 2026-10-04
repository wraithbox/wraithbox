# NFR00 - Non-functional requirements

**Purpose:** How fast, how small, on which platforms, and how
maintainable Wraith Box must be.

| ID | Description | Status |
|----|-------------|--------|
| NFR01-startup | Startup | V1-M6-release-gate |
| NFR02-fs-speed | Filesystem | V1-M6-release-gate |
| NFR03-footprint | Footprint | V1-M6-release-gate |
| NFR04-host-platforms | Host platforms | No milestone |
| NFR05-two-macos-vms | Platform limit | No milestone |
| NFR06-explained-refusals | Operability | V1-M5-approvals |
| NFR07-maintainability | Maintainability | No milestone |
| NFR08-licensing | Licensing | No milestone |

## Requirements

- **NFR01-startup: Startup.** Warm VM: ≤ 4 s to the Claude prompt. Suspended VM:
  ≤ 7 s. Hard ceiling 10 s (p95), excluding first-time project setup and
  the first boot after a host restart.
- **NFR02-fs-speed: Filesystem.** The workspace is on guest-native storage. Target
  ≥ 80% of host throughput on representative workloads (`npm ci`,
  `git status` on a large repo, incremental build), measured by the
  benchmark suite (S11-verification-and-spikes).
- **NFR03-footprint: Footprint.** Idle VMs suspend after a configurable period. Disk use
  grows only with divergence from the base image (copy-on-write clones).
- **NFR04-host-platforms: Host platforms.** First target: Apple Silicon, macOS 15 or later.
  Later: Windows 11 (Home and Pro) and Ubuntu LTS, with other Linux
  distributions considered after that. Works on managed (MDM or
  domain-joined) machines on every host OS.
- **NFR05-two-macos-vms: Platform limit.** macOS permits at most two concurrently running
  macOS guests per host. The design works within that limit (S06-vm-lifecycle).
  Other guest operating systems are limited only by resources.
- **NFR06-explained-refusals: Operability.** Every refusal names the rule that caused it and how
  to change it.
- **NFR07-maintainability: Maintainability.** Most code is cross-platform Go. Platform-native
  code (Swift on macOS, and the native toolchain of Windows or Linux only
  where Go cannot do the job) is confined to small components behind
  defined contracts (S10-tech-stack). Complexity gates, fuzzing for every parser
  that faces the guest, Go CI on all three host OSes from the start.
- **NFR08-licensing: Licensing.** Apache-2.0; dependencies under permissive licenses
  only.
