# F: NFR targets users will feel, against what was measured

Sources: NFR00-index, V1-initial, S06-06, the spikes and C11. Every
measurement below was taken on the maintainer's Apple M2 with 16 GB, macOS 27.0.1.

## Which NFRs gate V1

| NFR | Target | Gate? | How it got there |
|---|---|---|---|
| NFR01-startup | warm VM about 4 s, suspended about 7 s, p95 at most 10 s, all to the Claude prompt | **No**. A goal. M6.7 measures it, and it doesn't pass or fail the gate | Decided on I111: "performance work is planned later" |
| NFR02-fs-speed | about 80% of host on `npm ci`, `git status`, incremental build | **No**. A goal | Demoted after X05 answered "no", decided on I19: host-to-host variance is larger than the gap |
| NFR03-footprint | idle suspend, disk grows only with divergence (CoW) | **Yes**, M6 | qualitative, no number |
| NFR04-host-platforms | Apple Silicon, macOS 27+ | design constraint | macOS 27 forced by X17 (#143) |
| NFR05-two-macos-vms | at most two macOS guests | design constraint | |
| NFR06-explained-refusals | every refusal names its rule and fix | **Yes**, M5 | |
| NFR07, NFR08 | maintainability, licensing | continuous (CI) | |

**What gates V1 is security and function, not speed or friction.** Of the
NFRs a user feels day to day, only NFR03 (in qualitative form) and NFR06 gate.
No NFR covers memory, latency added by the proxy, approval prompt rate,
image build time, first-run time or total disk.

## Targets against measurements

| What the user feels | Target | Measured | Source | Gap |
|---|---|---|---|---|
| Start, suspended VM | about 7 s to prompt | 3.7 s p50, 3.9 s p95 to guest network (4 GiB). 4.1 / 4.6 s at 8 GiB. 4.1 / 4.2 s to vsock | X02, X18 | to the guest only. The Claude prompt adds session start, worktree, `~/.claude` copy and Claude Code's own start, none measured. Warm start isn't built (M6.4) |
| Start, warm VM | about 4 s to prompt | not measured | NFR01 says so | unknown |
| Start, cold boot | p95 at most 10 s (cold boot after host restart is excluded, but a cold boot happens on every start until warm start exists) | 10.9 s p50, 13.5 s p95 to vsock (X18). 8.5 to 11.0 s (X17). **14.1 s and 19.4 s through `wb vm start`** (C11, 2026-10-25, #288), the extra likely `wb-guestd` self-update | X18, X17, C11 | about 2x the 10 s goal on the product path. #86 (post-V1) |
| Time to first prompt, first session of a project | excluded from NFR01 | cold boot 14 to 19 s, plus export 16.2 s and clone 16.6 s for a 426 MiB repo (X07), plus reconcile (unmeasured), carry-in scan (unmeasured) | C11, X07 | probably 45 s to a few minutes. Never measured end to end |
| `npm ci` | about 80% | 71% on the data disk, 77% on a RAM disk | X05 | under the goal. #97 post-V1 |
| `git status`, 43k files | about 80% | 137% | X05 | fine |
| Go incremental build | about 80% | 81% | X05 | fine |
| `fsync`-heavy writes | none | 500 appends with fsync: 1.50 to 2.05 s guest (full sync) against 1.28 s host | X05 | full sync costs up to about 60% on flush-heavy work (databases, some test suites) |
| Stop | none | **30.0 s every time**, forced stop after timeout | C11, #414 | the guest ignores the requested stop |
| Reconnect after link drop | none | 103 ms | C11 | fine |
| Git round trip | fits NFR01 budget | incremental fetch 0.57 s, push of 3 commits 0.10 s, `wb land` 1.1 s | X07 | fine |
| Model call latency through proxy | none | `claude -p` 1.8 s through the spike proxy | X01 | no host baseline. TLS inspection overhead never measured |
| Unknown destination | none | DNS held 4 s, then NXDOMAIN with TTL 0 unless approved within the hold | S09-10 | design. The first try of every new name fails unless the user answers within 4 s |
| Image build | none | 6.6 min from a downloaded restore image (install 271.6 s, provisioning 127.1 s) | X17 | restore image download (26.6 GB) unmeasured (I49). The base layer, Claude Code layer and Xcode variant aren't timed |
| Xcode variant | none | copy Xcode 3.9 GB 125 s, first launch 62 s, iOS platform 8.05 GB download 1202 s, `simctl boot` 44 to 70 s | X06 | about 25 min more on a build that includes it |
| Memory | none (SEC13 caps) | defaults 4 vCPU and 8 GB per VM, two VMs possible. One booted simulator took 6% of 8 GiB | S06-06, X06 | host pressure on a 16 GB Mac with both VMs never measured. No balloon device in the spec |
| Disk | NFR03 qualitative | 64 GiB sparse system disk per build. ASIF grew to 11.7 GB on the X05 benchmark, so raw was chosen. Data disk gives space back at clean shutdown or cold boot (X08 follow-up). Restore image 26.6 GB, 40 GiB download cap | X05, X08, X17, S17-03 | total host disk for images, two VMs, data disks and per-session worktrees never measured. Worktrees stay until `wb discard`. `wb land` doesn't clean up (S08-07) |
| Audit volume | budgets | unmeasured (#175) | | |

## What M6.7 will and won't measure

M6.7 "Benchmarks and measured defaults" covers NFR01 to NFR03 and #148, #175.
S11 "Benchmarks" lists time to the Claude prompt (warm, suspended after a
session and a build, cold), `npm ci` with the dependency gate on, `git status`,
an incremental build, and download throughput through the gateway with the
CPU time of `wb-netd` and `wb-proxyd`. They don't gate the release.

S11 doesn't plan to measure: the first session of a new project (registration,
export, clone, reconcile), approval prompt rate, first-run time (download plus
build), host memory with both VMs, total disk after a week of use, or how
often the dependency gate refuses a lockfile install. Until V1 is close, VM
tests run only on the maintainer's Mac, outside `ci` (S11 "Test layers").
