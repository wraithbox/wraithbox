# NFR00 - Non-functional requirements

**Purpose:** How fast, how small, on which platforms, and how
maintainable Wraith Box must be.

| ID | Description | Status |
|----|-------------|--------|
| NFR01-startup | Startup | No milestone |
| NFR02-fs-speed | Filesystem | No milestone |
| NFR03-footprint | Footprint | V1-M6-release-gate |
| NFR04-host-platforms | Host platforms | No milestone |
| NFR05-two-macos-vms | Platform limit | No milestone |
| NFR06-explained-refusals | Operability | V1-M5-approvals |
| NFR07-maintainability | Maintainability | No milestone |
| NFR08-licensing | Licensing | No milestone |

## Requirements

- **NFR01-startup: Startup.** Goals: a warm VM reaches the Claude prompt
  in about 4 s and a suspended VM in about 7 s, and no start takes more
  than 10 s (p95), excluding first-time project setup and the first boot
  after a host restart. V1-M6-release-gate doesn't pass or fail on these
  goals. Performance work is planned later, and until then it's enough
  to know where a goal isn't met yet (decided on I111). Measured so far,
  on an Apple M2 host with 16 GB and a 4 vCPU guest that sat idle, and
  not yet to the Claude prompt:
  - A restore from saved state, with the state file not in the host's
    page cache, answers on the network in 3.7 s (p50) and 3.9 s (p95)
    with 4 GiB of guest memory, and in 4.1 s and 4.6 s with 8 GiB
    (X02-warm-start). `wb-guestd` answers on vsock 4.1 s (p50) and
    4.2 s (p95) after a 4 GiB restore starts (X18-vsock-handoff).
  - A cold boot, which is how every new VM starts the first time,
    reaches the guest daemon's vsock listener in 10.9 s (p50) and
    13.5 s (p95), over the 10 s goal (X18-vsock-handoff). I86 looks at
    the boot.
  - A warm VM, already running, hasn't been measured.
- **NFR02-fs-speed: Filesystem.** The workspace is on guest-native storage. Goal:
  about 80% of host throughput on representative workloads (`npm ci`,
  `git status` on a large repo, incremental build), measured by the
  benchmark suite (S11-verification-and-spikes). V1-M6-release-gate
  doesn't pass or fail on this goal. Results vary more between hosts
  (FileVault, Endpoint Security agents, load, chip generation) than the
  gap in question, so a fixed bar would pass on one Mac and fail on the
  next without saying anything about the design (decided on I19).
  Expected overhead, as measured: disk-heavy agent work (`npm ci`) runs
  at 71% of the host on the data disk and 77% on a guest RAM disk, on a
  loaded host (X05-fs-benchmark). CPU-bound and memory-bound overhead
  hasn't been measured, so no figure is given for it.
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
