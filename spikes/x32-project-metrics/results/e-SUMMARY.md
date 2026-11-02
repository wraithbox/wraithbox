# X32 agent E: remaining V1 work (saved by coordinator from the agent's hand-back, condensed)

## Remaining work

- 78 open issues in M3 to M7: M3 24, M7 9, M4 18, M5 4, M6 23.
- 39 plan packages are not filed yet: IMG.1 to IMG.8, M4.1 to M4.12, all of M5 and all of M6. 13 open issues fold into them.
- The DAG has 114 nodes, or 142.5 units, where 1 unit is one closed M2 or M3 package issue. An unfiled package counts as 2.6 units.

| ms | non-vm nodes/units | vm nodes/units | attended |
|---|---|---|---|
| M3 | 22/21.3 | 2/2.0 | 3 |
| M7 | 7/6.8 | 11/12.7 | 2 |
| M4 | 21/32.1 | 8/8.3 | 5 |
| M5 | 13/29.3 | 2/2.5 | 3 |
| M6 | 17/17.9 | 9/9.6 | 6 |

- Untriaged issues that may add V1 scope: #536, #541, #535 and #539, plus #437, which is ready-for-human.

## Critical path

- The path through the dependencies alone takes 22.6 days over 11 nodes, 3 of them attended: IMG.1 → IMG.2 → IMG.3 → IMG.4* → IMG.5 → IMG.7 → IMG.7-vm → M4.12-vm (C06)* → M6.3 → M6.7 → M6.8*.
- What binds is the serial vm lane: 32 nodes, 35 units, about 42 busy days. A list schedule with 1 VM builder and 3 other builders takes about 49 days, with the vm lane busy 87% of the time.
- The vm lane has 10 attended steps, and 9 more attended steps feed it. Among the feeders, #253 has been ready-for-human for 18 days and sits in front of M4.10.
- Beside the lane are 80 non-vm nodes, or 107 units: about 36 builder-days on 3 builders, or about 54 days on 2, when that work becomes co-critical.
- Stale or missing plan edges:
  - M4.1 depends on M3.11, which moved to M6.
  - No package builds the secret scanner (only spec issue #306 exists), yet IMG.5, M4.8 and M4.9 need it.
  - M6 has no dependencies in the plan.

## Calibration

The sample is 80 closed issues in M2, M3 and M7, each closed by a PR.

- Lines changed per package issue (code and tests, excluding generated code): p50 2,714, p10 882, p90 4,304. Of the p50, 1,364 lines are source.
  - Only 8 of 43 fall within 300 to 1,500 lines.
  - A whole M2 package ranges from 477 to 15,761 lines, median about 7,000, which is 3 to 5 times the plan's upper figure.
- Claim to merge:
  - non-vm: p50 2.3 h, p90 18 h;
  - vm: p50 13 h, p90 38 h, maximum 72 h.
- Review rounds: p50 2, maximum 3, which is the cap.
- Throughput over 14 active days (10-16 to 11-01): 43 package issues and 37 other issues, which is 3.1 package issues a day. The vm lane closed 0.93 items a day.
- Concurrency: in flight p50 1, p90 4, maximum 8. More than one vm item was in flight in only 2 of 209 hours.
- Runs: 13 runs of about 35 waves merged 49 PRs. Five runs (#394, #411, #415, #439, #470) merged none, because everything waited on the maintainer. Run #519 merged 14 PRs in 5 waves over 11.5 h.
- Maintainer waits:
  - needs-host: p50 23 h, p90 164 h;
  - ready-for-human: p50 47 h, p75 149 h, p90 269 h;
  - last approval to merge: p50 0.5 h, p90 16 h, maximum 82 h.

## Scope growth

- No issue moved from V1 to post-V1. 29 issues moved between V1 milestones, after M2 and M3 were re-cut.
- 93 new issues entered V1 after 10-16:
  - 46 were filed in bulk;
  - 47 came in as work went on, about 3 a day.
- 45 were bugs or follow-ups, and 26 of those are still open. Follow-ups arrive at about half the closing rate.

## Agent E's estimate (active working days from 2026-11-02)

- P50 about 55. Its Monte Carlo gave p50 53 with 3 builders and 61 with 2.
- Pessimistic about 85: follow-ups ×1.3 on 2 builders gives p80 84 and p90 89.
- 24 of the 31 days so far had merges. Calendar time is about 70 days (mid-January 2027) at P50, and about 110 days (late February 2027) in the pessimistic case.

## Files

remaining.csv, dag.json, critical-path.md, calibration.csv, scope.csv, gates.csv, merge-waits.csv, raw/

## Doubts

- Size factors assume future packages split into issues the way M2 and M3 did. Image building has no precedent.
- Durations are wall-clock, converted to active days with throughput means.
- When one PR closed several issues, its lines are split evenly across them.
- H2 is assumed met. H4 and H5 have no issue, so their waits are sampled.
- The Monte Carlo is greedy: it starts whatever is ready first, not in milestone order.
