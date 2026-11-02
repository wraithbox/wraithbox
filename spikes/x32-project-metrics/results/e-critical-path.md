# Critical path of the remaining V1 work (agent E, X32, 2026-11-02)

Source: `dag.json` (114 nodes, main at 6138336c, GitHub at 2026-11-02 07:30).
Durations here are point estimates: a vm unit is 1.2 active days, a non-vm
unit 1.0 day, and an attended (maintainer-gated) node adds 1 day of wait.
The Monte Carlo in `dag.mjs` resamples these from `calibration.csv` and `gates.csv`.

## 1. The longest dependency path, without lane limits

The path is 22.6 days over 11 nodes, and 3 of them are attended.

IMG.1 (downloads, nonvm 2.6) -> IMG.2 (installer, sealed image, vm) -> IMG.3 (build network, vm)
-> **IMG.4 (wb-guestadmin, vm, host)** -> IMG.5 (provisioning, sealing scan, vm)
-> IMG.7 (Claude Code top layer, logic) -> IMG.7-vm -> **M4.12-vm (C06 end to end, vm, host)**
-> M6.3 (session profiles, vm) -> M6.7 (benchmarks, vm) -> **M6.8 (full conformance, vm, host)**

A close second runs through the network floor and the credential:
#488 -> #491 -> #492 -> #494 -> #511/#512 -> **#513 (egress in a VM, vm, host)**, and
#498 -> #500 -> M4.10 (bindings, model credential, after **#253**, **#504**, **H4**)
-> **M4.12 (first prompt, vm, host)** -> M4.12-vm.

## 2. The binding constraint: the serial vm lane

The vm lane holds 32 nodes and 35.1 vm units, about 42 busy days at 1.2 days per unit.
That's nearly twice the dependency path. With one VM builder, the critical path is the vm
lane plus the stretches where it waits on non-vm or maintainer work.
A list schedule with 3 non-vm builders ends at 48.6 days. The lane is idle for about 6.5
of those days, waiting on dependencies. This is the order the point schedule found, with
the start day and A for attended:

```
#262 X30 spike @0 | #413 @0.8 | #414 @2.0 | #448 X31 spike @2.4 | #424 A @3.2 | #425 A @3.6 | #429 A @4.0
| #436 A @5.2 | #479 @5.5 | #420 @6.7 | IMG.2 @7.9 | M4.8-vm @9.7 | IMG.3 @10.9 | M4.9-vm @12.7
| IMG.4 A @13.9 | IMG.5 @16.7 | IMG.6 @18.5 | IMG.8 @20.3 | M4.2-vm @22.1 | M6.2-vm @23.3
| IMG.7-vm @27.6 | #510 @28.8 | M4.7 @30.2 | #513 A @32.0 | M4.12 A @33.8 | M5.11-vm @36.2
| M4.12-vm A @37.4 | M5.12 A @38.6 | M6.3 @40.4 | M6.4 @42.2 | M6.7 @44.0 | M6.8 A @45.8
```

The vm lane has 10 attended steps: #424, #425, #429, #436, IMG.4, #513, M4.12, M4.12-vm,
M5.12 and M6.8. Four of them are low-priority V1-M6 or V1-M7 follow-ups that could move.
A further 9 attended nodes feed the lane:

- H4 and H5, which have no issue yet;
- #253 (ready-for-human for 18 days, ahead of M4.10);
- #504, #505 and #509 (Keychain and Secure Enclave, needs-host, ahead of #513 and M4.10);
- M5.4 (the notification helper);
- M6.6 with #118 (upgrades, signing);
- #438.

Every vm node also needs the maintainer to approve its merge (`merge_gate`). Since
2026-10-16, gated PRs waited p50 0.5 h, p90 16 h and at most 82 h from the last approving
review to the merge.

## 3. Work that runs beside it

That's 80 non-vm nodes, 107 units in all. Spread over 3 builders, they need about 36
builder-days, which fits inside the vm lane's ~42 to 49 days. With only 2 builders (the
mean in-flight count so far was 1.9), they need about 54 days and become co-critical. The
big parallel blocks:

- the network floor logic: #483 to #512, 21 nodes;
- V1-M5 approvals: M5.1 to M5.10, 10 packages, all `any` or `mac`;
- the git round-trip logic: M4.1, M4.3 to M4.6 and M4.11;
- supervision: #478 to #480 and #370;
- M6.1, M6.5 and M6.6;
- about 10 small V1-M6 follow-ups.

## 4. Plan edges worth checking

- M4.1 (`export.git` writer) depends on M3.11 (`wb-launcher`, #478 to #480 and #370).
  M3.11 moved to V1-M6 on 2026-10-31, but the dependency still pulls it ahead of the
  V1-M4 git work. #479 in it is a vm-lane item.
- IMG.5 and M4.8 or M4.9 depend on "secret scanner". Only its spec (#306) is filed. No
  package in the plan implements the scanner, so it is unaccounted scope.
- The plan gives V1-M6 no dependencies. The edges in `plan.mjs` are guesses: M6.1 after
  M3.11, M6.3 and M6.4 after M4.12, M6.7 after M6.3 and M6.4, and M6.8 after all of them
  and M5.12.
- IMG.2 waits on X30 (#262) and, here, X31 (#448), because X31 decides the admin path
  that IMG.4 builds. H2, a macOS 27 host, is taken as met: #143 is closed and this host
  runs Darwin 27.
