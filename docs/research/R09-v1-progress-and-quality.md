# R09 - V1 progress, effort and quality

**Purpose:** Measures how far along V1-initial is and what it has cost
in human and AI effort. Forecasts when V1 is done, and predicts its
quality to spec and in use. Measured at `6138336c` on 2026-11-02, over the
project's first month (2026-10-03 to 2026-11-01). This note is the
report of X32-project-metrics (I543). The scripts and the aggregate
outputs are on the kept branch `spike/x32-project-metrics`.

**Requirements:** none. The note measures the project and the way it's
built, not the product. It points at NFR01-startup, NFR02-fs-speed and
SEC06-repo-writes where its findings touch them.

## Summary

- **Where V1 stands.** The design is close to done: V1-M1-spikes-closed
  and V1-M2-walking-skeleton are closed, 22 of 31 spikes are answered,
  and 118 decisions are recorded. The implementation is about a quarter
  to a third done. 55 work units are merged, and 143 units remain in
  114 items. `wb` runs 6 of its 22 commands, `wb claude` isn't one of
  them, and `wb-netd` and `wb-proxyd` are stubs.
- **Effort so far.** The maintainer spent an estimated 59 to 63 hours,
  with a range of 27 to 117 hours, on 25 of 30 days. That is about 2.5
  hours per active day, mostly in evenings and at weekends. The agents
  ran 182 agent-hours in 705 subagents. They used $3,294 of tokens at
  API list prices: $16 per merged pull request, or $132 per active day.
- **When it will be done.** At the pace of the last three weeks, the
  forecast needs 53 to 60 active days. The serial VM lane binds. On
  the calendar, P50 is early to mid January 2027 if attention stays as
  it was, and April 2027 at half the attention. Between those
  scenarios the date moves by three months, with the same agents.
- **Will it be good.** To spec: probably yes. The leading indicators
  are strong, with weak spots in the tests that only the maintainer can
  run and in the security code that isn't written yet. In use: unknown,
  with real risk. No exit criterion measures use, and some of
  Wraith Box's own design choices change the daily work loop a lot.
  These choices are deliberate. One of them, the empty GraphQL
  allowlist, breaks `gh pr create` and is cheap to revisit, as a
  change to the built-in profile.

## Method

The coordinator ran six agents in parallel, then combined their
outputs. Each agent wrote scripts (bun, on the spike branch) and
returned aggregates only.

| Agent | Measured | Sources |
|---|---|---|
| A | The current state of the art in measuring AI-assisted work and in forecasting | 60 or so papers, reports and docs, each with a trust note (`results/a-sources.md`) |
| B | Commits, lines, structure over time, issues, pull requests, CI, contributions | `git log`, `git blame`, GitHub REST and GraphQL |
| C | Sessions, subagents, tokens, cost, agent time, prompts, gaps | Claude Code transcripts on the maintainer's Mac, about 1 GB |
| D | Specs, traceability, coverage, fuzz targets, imports, complexity, doc comments, dependencies, interfaces | The tree at `6138336c`, `go test -cover`, `swift test` with coverage, golangci-lint with a temporary config |
| E | Remaining work, calibration, scope growth, critical path | The open issues of V1-M3 to V1-M7, `docs/implementation-plan.md`, run issues |
| F | Fitness for use | Specs, spike results, R01 and R04 to R07, the built `wb` binary (read-only commands) |

Choices behind the numbers:

- **Days and weeks** are in Amsterdam time, and weeks start on Monday.
  The week of 2026-09-28 holds only 2026-10-03 and 2026-10-04.
- **Tokens** are deduplicated by message ID. Claude Code writes one
  message as several lines that repeat its usage, and a naive sum
  counts it about twice. The transcripts still miss about 30% of the
  cost that Claude Code's own `cost-state` records show, probably the
  side calls it doesn't log (classifier, web tools, compaction, titles).
  Sessions without subagents or web tools match `cost-state` within
  3%. So totals use `cost-state` ($3,294), and breakdowns use the
  transcripts scaled by 1.43.
- **Cost** is API list price (the pricing page, fetched 2026-11-02),
  not what the maintainer paid on a subscription.
- **Human effort** isn't recorded anywhere, and agents commit and post
  as the maintainer's GitHub login. 13 commits lack the `Assisted-by`
  trailer: 5 from Dependabot and 8 others, 4 of them hand edits. So do
  12 comments. The note infers human effort from the
  transcripts ("Human effort").
- **Privacy.** The transcripts and cached GitHub responses stay on the
  maintainer's disk. This note publishes weekly totals and summaries by
  weekday and hour, not session timelines. It doesn't name any other
  project the maintainer works on.
- **Function points** were skipped, as the maintainer agreed. The note
  counts the external interfaces instead, as specified, implemented and
  tested.

## The state of the art, in short

Agent A's survey shaped the method more than it supplied comparisons:

- **Count finished work, not output.** In the largest study, 718 firms
  adopting coding agents wrote 30% more code and 23% more pull requests,
  but resolved no more issues (Chen and Stratton, Harvard, 2026, seen
  through press coverage). Review time rose 49%. So the forecast below
  uses closed work items, sized against the plan, and treats review and
  decision capacity as the constraint.
- **Measure human time from transcripts, in 10-minute windows.** METR
  counts the 10-minute windows that hold at least one human message,
  once across parallel sessions
  ([METR, 2026-02-17](https://metr.org/notes/2026-02-17-exploratory-transcript-analysis-for-estimating-time-savings-from-coding-agents/)).
  Session wall time doesn't work: about 80% of interactive session time
  is spent waiting for the human
  ([Anthropic, 2026-10-16](https://www.anthropic.com/research/employment-at-anthropic)).
  Self-reports are unreliable both ways. In METR's 2025 trial,
  developers believed they were 20% faster while they were 19% slower
  ([METR, 2025](https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/)).
- **Forecast with a Monte Carlo over throughput, and report P85.** At
  ASOS, a P85 forecast held 90% of the time (Vacanti's method). No
  source covers part-time capacity or projects built by agents, so the
  two-layer model below (active days, then throughput per active day)
  is this note's own.
- **Treat coverage as a floor and green CI as weak evidence when agents
  write the tests.** Tests written by the agent that wrote the code
  mirror its reading of the spec. Mutation testing and acceptance tests
  written apart are stronger
  ([Google, TSE 2022](https://arxiv.org/abs/2102.11378)).
- **Assess fitness for use apart from conformance.** ISO/IEC 25010
  calls it interaction capability. The practical methods are a
  walkthrough of the first-run journey, and real use measured against written
  goals.

No published reference class fits a solo, part-time, agent-heavy
project. Per-engineer figures from teams look absurd next to one
person with 13 agents in parallel. The note compares against outside
numbers only where the setting is close.

## Where V1 stands

| Milestone | Closed / total issues | State |
|---|---|---|
| V1-M1-spikes-closed | 67 / 67 | Closed 2026-10-16 |
| V1-M2-walking-skeleton | 62 / 62 | Closed |
| V1-M3-network-floor | 18 / 42 | In progress |
| V1-M7-image-build | 4 / 13 | Started |
| V1-M4-claude-end-to-end | 0 / 18 | Not started, 12 packages unfiled |
| V1-M5-approvals | 0 / 4 | Not started, all packages unfiled |
| V1-M6-release-gate | 0 / 23 | Not started, all packages unfiled |

Issue counts mislead here: M1 was 67 small spike and decision issues.
Agent E sized the remaining work in units of one closed M2 or M3
package issue (about 2,700 changed lines at the median, about 1,400 of
them source). An unfiled plan package counts as 1 to 2.6 units by
kind: 1 for the VM part of a package, 1.5 for a VM package, 2 for a
package of logic, and 2.6, the M2 and M3 average, for the rest.

- **Built in the build phase** (2026-10-16 to 11-01): 55 units, from
  43 package issues, 1 spike and 36 small issues.
- **Remaining:** 143 units in 114 items: 76 with an issue, 36
  unfiled packages, and the H4 and H5 host setups. The 78 open issues
  sit on the 76 items, two of which carry two issues each. 35 units are on the VM lane, which runs one item
  at a time, and 107 units run in parallel beside it.

So the implementation is about 28% done, by work units. The parts a
user touches are further behind:

| Interface | Specified | Implemented | Tested |
|---|---|---|---|
| `wb` top-level commands (S05-cli) | 22 | 6 | 6 |
| `wb` leaf commands | 41 | 15 | 15 |
| gRPC methods between processes | 26 | 26 | 25 |
| Policy rule kinds (S07-11-http-policy, S09-policy-credentials-audit) | 11 | 0 | 0 |

`wb-hostd`, `wb-guestd` and `wb-vmd` exist and boot a VM in tests on
the maintainer's Mac. `wb-netd` and `wb-proxyd`, which do the
egress controls, and `wb-credset` are 11-line stubs. `wb claude` prints
`not implemented yet`. V1-M3-network-floor builds the network path, and
V1-M4-claude-end-to-end gives the first real prompt.

## Effort

### AI effort

| Measure | Value |
|---|---|
| Sessions | 76 top-level sessions, 705 subagents |
| Subagents by role | builder 170, code reviewer 162, forked code-review skill 139, security reviewer 99, general-purpose 59, wave lead 35, other 41 |
| API calls | 38,890 logged |
| Tokens | output 13.0 M, cache write 168 M, cache read 4,531 M, uncached input 0.1 M |
| Cost at API list price | $3,294: Opus 5.5 $2,663, Fable 5.1 $618 (security reviewers), other $13 |
| Agent time | 182 agent-hours, 92 hours of wall time with at least one agent active, up to 13 agents at once |
| Cost by role (transcripts) | builders 45%, security reviewers 15%, code reviewers and the review skill 10%, wave leads 3% |
| Per merged pull request | $16 and 0.9 agent-hours, over 201 agent pull requests |
| Per active day | $132 |
| Most expensive issues | I67 $57, I289 $52, I161 $48, I476 $48, I345 $46 (transcripts, unscaled) |

Cache reads are 96% of the tokens. The work runs on long contexts
that it reads again on each turn, which matches R03-fix-rounds-compaction.
The vendor's figure for one developer is $13 per active day
([Claude Code docs](https://code.claude.com/docs/en/costs)). This
project spends ten times that, because it runs waves of parallel
agents, not one session.

### Human effort

The maintainer's time is inferred, not measured. The assumptions came
from the maintainer:

- A day without AI activity doesn't hold any effort.
- On an active day, effort rises with the prompts typed, the agent
  output read, the time spent thinking and the decisions made.
- Weekdays from 9:00 to 17:00 hold little, because a day job comes
  first.

The transcripts give an estimate by each of these methods:

- **METR's method** counts 10-minute windows with a human prompt:
  **59 hours**.
- **This note's model** gives a low, mid and high bound per day:
  - low: prompting sessions at a 5-minute gap, plus 5 minutes per
    decision;
  - mid: prompting sessions at a 15-minute gap, or the time to read
    the agents' output when that's longer, plus half the thinking gaps
    and 10 minutes per decision;
  - high: prompting sessions at a 30-minute gap, or the reading time
    when that's longer, plus all thinking gaps, half the other gaps of
    15 to 120 minutes, and 20 minutes per decision.

  Session time on a day shrinks with its share of prompts in weekday
  working hours: by up to half at low and mid, and up to a quarter at
  high. The result is **27 / 63 / 117 hours**.

The METR count and the mid estimate agree at about 60 hours, or 2.5 hours per
active day and 14 hours per week. Neither sees time spent away from the
keyboard, such as thinking on a walk or reading a brief on a phone, so
both are lower bounds in that sense.

| Signal | Value |
|---|---|
| Prompts | 702 with 20,525 words: 486 typed, 92 answers to questions, 78 slash commands, 46 shell commands |
| Prompt length | median 15 words, 75th percentile 34, longest 948 |
| Reading | 275,000 words of agent output in interactive sessions |
| Decisions | 118 `Decision (date):` comments on 95 issues. 43 of 46 briefs are decided or answered |
| Thinking gaps | 28 pauses of 15 to 120 minutes before a long prompt or after a question, 17 hours in total |
| Active days | 25 of 30. Every day with agent activity also had a human prompt |

When the maintainer works:

| Local time | 00-07 | 07-09 | 09-17 | 17-24 |
|---|---|---|---|---|
| Share of prompts | 6% | 14% | 37% | 44% |

| Day | Mon | Tue | Wed | Thu | Fri | Sat | Sun |
|---|---|---|---|---|---|---|---|
| Prompts | 77 | 54 | 90 | 123 | 64 | 99 | 195 |

Only 81 of 702 prompts (12%) fell in weekday working hours, on 10 of
the 25 days. The 09-17 column above is mostly weekends.

Human effort tracks AI activity loosely. Per day, the correlation
between prompting time and agent wall time is 0.54, and between
decisions and prompting time 0.66. The days with the most decisions
are the heaviest human days: 2026-10-04 (30 decisions on spike
results) and the week of 2026-10-12 (39).

The project also took over the maintainer's public GitHub activity.
Wraith Box was 19% of the maintainer's contributions in its first week,
then 65%, 92%, 95% and 74%. Other work dropped from 738 contributions
in the first week to between 8 and 194 a week after it.

For each agent-hour the maintainer spent about 20 minutes, a ratio of
about 3 agent-hours to 1 human hour. Anthropic reports 6 to 1 for its
own engineers, but it assumes a 40-hour human week. The maintainer's
measured week is about 14 hours.

## Velocity

| Week of | Active days | Commits | Spec and docs lines added | Code and test lines added | Issues closed | PRs merged | Decisions | Human h (METR) | Agent h | Cost $ |
|---|---|---|---|---|---|---|---|---|---|---|
| 2026-09-28 | 2 | 74 | 10,027 | 658 | 6 | 25 | 30 | 7.3 | 14 | 247 |
| 2026-10-05 | 5 | 180 | 18,705 | 33 | 49 | 49 | 17 | 12.5 | 46 | 802 |
| 2026-10-12 | 7 | 223 | 34,377 | 43,608 | 87 | 65 | 39 | 18.7 | 55 | 976 |
| 2026-10-19 | 6 | 111 | 2,876 | 29,220 | 21 | 25 | 5 | 9.0 | 24 | 486 |
| 2026-10-26 | 5 | 208 | 4,180 | 48,988 | 44 | 42 | 27 | 11.7 | 43 | 783 |
| **Total** | **25** | **796** | **70,165** | **122,507** | **207** | **206** | **118** | **59.2** | **182** | **3,294** |

Code and test lines are Go and Swift without generated code. The
"Cost" column is the transcript figure scaled to the `cost-state` total.

The project went through two phases:

- **Design, weeks 1 and 2.** 29,000 lines of spec and almost no code.
  Spikes answered their questions, and the maintainer recorded 47
  decisions in 7 active days.
- **Build, weeks 3 to 5.** 122,000 lines of code and tests, 55 work
  units. That's 3.9 units per active day: 3.1 on the parallel lanes and
  0.8 on the VM lane.

Between 2026-10-23 and 2026-10-31, five runs merged nothing, because
every ready item waited on the maintainer for a VM, host or spec step.
Gated waits run long:

- `needs-host`, from label to done: median 23 hours, 90th percentile
  164 hours.
- `ready-for-human`: median 47 hours, 90th percentile 269 hours.
- From the last approval to the merge: median 0.5 hours.

At HEAD:

| Measure | Value |
|---|---|
| Go | 97 packages, 35,900 code lines (52,400 with comments and blanks), 45,100 test lines, 11,000 generated |
| Go symbols | 3,146 functions, 1,994 methods, 833 types |
| Swift | 3,500 source lines, 3,300 test lines, 189 types, 519 functions |
| Specs | 111 files, 120,000 words, 40 requirements, 21 threats, 12 test case pages |
| Other docs | 31 spikes, 46 briefs, 9 research notes, the agent harness |
| CI | 1,055 runs over the month |

## Forecast

### Remaining work and its critical path

Agent E built a graph of the remaining work from the issues' `blockedBy`
links and the plan's order, with the VM lane serial:

- **The dependency chain alone** is 11 items, about 23 days: IMG.1 to
  IMG.5, IMG.7 and its VM part, the C06-model-api conformance run of
  M4.12, then M6.3, M6.7 and M6.8. IMG.4, the M4.12 run and M6.8 need
  the maintainer present.
- **The VM lane binds**: 32 items and 35 units, about 42 busy days.
  With one VM builder and three others, a schedule ends after about 49
  days, with the VM lane busy 87% of that time.
- **19 attended steps** sit on or feed the VM lane, among them the H4
  and H5 host setups, which have no issue yet. I253 has waited for the
  maintainer for 18 days and is in front of M4.10.

### Simulation

Methods built separately give close answers:

- **Throughput (this note).** Each step replays one of the 14 active
  days of the build phase, picked at random, with the units closed on
  each lane that day. A run ends when both lanes are done. Remaining work grows by a random factor
  between 1.0 and 1.4, for follow-ups (45 of the 93 issues added to V1
  since 2026-10-16) and for errors in sizing the unfiled packages. A
  variant sizes history by changed lines instead of issue counts.
- **Scheduling (agent E).** It schedules the dependency graph with one
  VM builder and two or three others, drawing durations from the closed
  issues.

| Method | P50 active days | P85 | P95 |
|---|---|---|---|
| Throughput, by issue units | 53 | 62 | 67 |
| Throughput, by changed lines | 60 | 71 | 78 |
| Scheduling, 3 builders | 53 | | |
| Scheduling, 2 builders | 61 | | |
| Scheduling, 2 builders, follow-ups ×1.3 | | 84 (P80) | 89 (P90) |

Active days become calendar days through the maintainer's attention.
Weeks are drawn at random from three patterns:

- **More**: 6 or 7 active days a week.
- **As before**: the weeks of 2026-10-05 to 2026-10-26, with 5, 5, 6 or
  7 active days.
- **Half**: 0, 2, 3, 3 or 4 active days, so some weeks off.

| Attention | P50 | P85 | P95 |
|---|---|---|---|
| More | 2026-12-29 to 2027-01-04 | 2027-01-07 to 2027-01-16 | 2027-01-13 to 2027-01-24 |
| As before | 2027-01-07 to 2027-01-15 | 2027-01-18 to 2027-01-29 | 2027-01-25 to 2027-02-07 |
| Half | 2027-04-06 to 2027-04-26 | 2027-05-11 to 2027-06-05 | 2027-06-01 to 2027-06-29 |

Each range spans the two throughput variants. Agent E, assuming that
merge days continue as in the first month, puts P50 at mid-January
and its pessimistic case at late February.

The simulation leaves out:

- **The holidays.** Late December can add attention or remove it.
- **New kinds of work.** Image building (V1-M7-image-build) and the
  policy engine of V1-M5-approvals have no precedent among the closed
  issues. The open spikes X30-installer-trust and X31-admin-channel can
  add work.
- **Gaps in the plan.** No package builds the secret scanner that
  IMG.5, M4.8 and M4.9 need (I306 is its spec issue). M4.1 still waits
  on M3.11, which moved to V1-M6-release-gate. V1-M6-release-gate has
  no dependencies in the plan.
- **A month of history.** The as-before pattern is the project's most
  intense month, when it pushed out nearly all other public work. With
  14 sampled days, the next day is inside the range seen so far 87%
  of the time (Magennis's rule).

### What finishing will cost

The build phase (2026-10-16 to 11-01) cost $1,892 of tokens and 28
maintainer hours at the mid estimate for its 55 units: $34 and 0.51
hours per unit. At those rates the remaining 143 units, grown by 1.0 to
1.4, take about **$4,900 to $6,900** of tokens at API list prices, and
about **73 to 102 maintainer hours** at the mid estimate, with a range
of 33 to 210 hours. The VM lane and the 19
attended steps need more of the maintainer per unit than the build
phase did, so expect the upper half.

### How the plan's own estimates held

- **Package size.** `docs/implementation-plan.md` (2026-10-16) sized a
  package at 300 to 1,500 lines. The median M2 package was about 7,000
  changed lines (3,900 source), 3 to 5 times the upper figure. Only 8 of
  43 package issues were inside the range. The figure fits one issue's
  source code, not a package.
- **Throughput.** The larger size didn't slow the agents much: the
  build phase still closed 3 package issues a day.
- **A stale status.** The plan's "Where things stand" section still
  says the code is about 500 lines.

## Quality

### Scorecard

| Area | Measure | Value now | Reference | Reading |
|---|---|---|---|---|
| Maintainability | Cognitive complexity per function | max 15, 45 functions in 11 to 15, 0 over the limit, over about 2,500 functions | The limit of 15 in `.golangci.yml` | Good, and enforced by CI |
| Maintainability | File size, lint exceptions | largest file 399 lines, 33 `//nolint`, each with a reason | Limit 800 | Good |
| Cohesion | Import graph | 97 packages, highest fan-out 19 (`hostd/hostservice`), highest fan-in 20 (`version`) | No current benchmark found | Neutral. `hostservice` is the place to watch |
| Tests | Statement coverage | Go 82.3%, Swift 72.0% (lines) | Google: 60% acceptable, 75% commendable, 90% exemplary | Good, but a floor |
| Tests | Test to code lines | Go 1.26, Swift 0.93 | | High |
| Tests | Fuzz targets on guest input | 41. Every host-side parser of guest bytes that exists has one. The corpus is 1 file, 10 s per target | AGENTS.md rule | Good coverage, shallow depth |
| Tests | Run only by hand | 18 conformance tests, 3 VM tests, the Swift boot suite | | All VM evidence depends on the maintainer |
| Docs | Doc comments | Go exported 97.7%, Go packages 100%, Swift public 65.7% | | Good for Go |
| Spec alignment | Requirement IDs cited in code or tests | SEC 12 of 15, NFR 3 of 8, FR 2 of 17, test case pages 9 of 12 | Traceability goes with fewer defects (Rempel and Mäder, 2017) | SEC06-repo-writes and SEC07-dep-gate aren't built yet. FR IDs are cited by milestones, not code |
| Spec alignment | Plan drift | stale status, package sizes off by 3 to 5 times, stale dependency edges, one missing package | | The specs are current. The plan isn't |
| Dependencies | Freshness and count | 10 direct Go modules at 0.24 libyears (4.25 with indirect), 22 Swift pins, 8 bun packages | `mise run vuln` is clean | Small and current |
| Rework | Line survival | 95 to 97% of the code and test lines added each build week are still at HEAD | GitClear: 5.7% two-week churn across the industry (vendor) | Comparable, and early |
| Rework | Reverts, fixes | 0 reverts, 128 fix and 106 feat commits | | Fixes outnumber features |
| Rework | Follow-up inflow | 45 of 93 issues added to V1 since 2026-10-16 are bugs or follow-ups, arriving at about half the closing rate | Agent pull requests get a follow-up fix 1.62 times as often as human ones (`2609.26847`) | Expected |
| Review | Rounds and verdicts | 2 rounds at the median for package issues (1 across all issues), and 6 issues reached the cap of 3. Out of 462 verdicts, 236 approved and about 215 asked for changes | | Reviews find things. Builders don't pass review on their first try |
| CI | Failure and flaky rates | failures 3.6 to 14% a week (31% in week 1, during setup). flaky runs at least 0 to 2.5% a week, then 7.4% in the week of 2026-10-26 | Google: 1.5% of test runs flaky | The last week needs a look |
| Fitness | What a user can do | 6 of 22 commands, no prompt in a guest yet | | Not testable yet |

### Fitness for use

Agent F walked the `wb claude` journey through the specs, the spike
results and the code. Its main findings, checked by the coordinator
where noted:

- **V1's exit criteria don't measure use.**
  - Each milestone's exit criteria cite requirement IDs only.
  - The NFRs a user feels were demoted to goals: NFR01-startup (I111)
    and NFR02-fs-speed (I19, after X05-fs-benchmark).
  - The first prompt in a guest comes in V1-M4-claude-end-to-end, after
    V1-M3-network-floor and V1-M7-image-build.
- **The work loop changes a lot, on purpose.**
  - The editor can't see files during a session, because nothing is
    mounted from the host.
  - Each session starts from a fresh worktree, without `node_modules` or
    ignored files.
  - Work comes back through `wb diff` and `wb land`.
  - No port forwarding or clipboard (V1-08-no-forward-clipboard).
  - These are decided controls, so changing them is a spec change.
- **Forge work through `gh` breaks by default.**
  - S07-11-http-policy allows REST writes to the session's
    repositories, but refuses GraphQL mutations unless their operation
    name is on an allowlist.
  - I502 starts that allowlist empty.
  - `gh pr create` and `gh issue comment` use GraphQL mutations, so the
    way this project itself is built can't run inside `wb claude` in V1.
  - The coordinator checked this in the spec and the issue.
  - It's a choice of entries in the built-in profile, not of design,
    so it's cheap to revisit. A user rule on that endpoint is a load
    error (S09-02-precedence). The change belongs in the built-in
    list.
- **Approvals during normal work.**
  - A name not yet allowed is held for 4 seconds at DNS and then
    refused. The first try fails.
  - Docs sites, release downloads and CDNs will need approvals.
  - X21-dep-gate-registries refused 4 of 10 real installs, 2 for package
    age and 2 for a critical CVE.
  - Nobody has counted how often prompts would fire in a real day.
- **Start time and first run.**
  - `wb vm start` took 14 to 19 seconds to the guest daemon in
    C11-host-daemons, against a 10-second goal.
  - The first session in a project adds a clone (33 seconds for 426 MiB
    in X07-git-round-trip).
  - `npm ci` runs again in each worktree, at 71% of host speed
    (X05-fs-benchmark).
  - First-time setup needs a signing team, a 27 GB restore image and an
    image build. That has never been timed.

Agent F's judgment:

- About 80% that a plain session works end to end on day one in a Go,
  Node or Python project.
- 30 to 40% that the maintainer runs most Claude sessions through `wb`
  in the first month.

V1's combination of controls is unique among the tools in R01 and R04
to R07. Each of those tools is cheaper to live with. No note compares
V1 with the real baseline yet: Claude Code on the host with its own
permission prompts, or a dev container.

## Prediction: V1's initial quality

Assuming the current way of working holds (spec first, parallel agents
with two reviewers, the maintainer deciding and testing on the host):

- **To spec: good, with uneven depth.** The code that exists has high
  coverage, fuzz targets at the guest boundary, no complexity
  violations, nearly complete doc comments and little churn. Expect
  V1's defects to cluster in three places:
  - **The network and policy code still to be written.** `wb-netd`,
    `wb-proxyd`, the HTTP policy, the dependency gate and the git
    transport hold the most guest-facing parsing in V1, and none of it
    exists yet.
  - **The VM integration.** Only the maintainer runs those tests, by
    hand, and the runs aren't recorded.
  - **Tests that mirror the code.** The same model writes both, and
    green CI can hide a shared misreading of the spec.

  The follow-up inflow suggests about one bug or follow-up for every two
  items closed. Expect that to continue after the release.
- **In use: uncertain, and not measured by the plan.** A first user
  will likely get a working, slow-to-start, secure session that
  changes how they work. The project's own workflow, with agents that
  open pull requests through `gh`, won't run inside it unless the
  GraphQL allowlist changes. Whether V1 becomes the maintainer's
  default depends on approval friction and start time, which no
  milestone measures.

Approval prompts counted against a week of real transcripts would test
the friction risk now. A timed first run and a week of real use at
V1-M4 would test the rest.

## What this report cost

| Item | Amount |
|---|---|
| Wave 1 agents | about 0.86 M tokens over six agents, 6 to 13 minutes each in parallel |
| Coordinator | one session at high effort, not counted here |
| Maintainer | four rounds of questions on the plan, and the review of this note |

The scripts on `spike/x32-project-metrics` can run again with the same
agents. X32-project-metrics reports whether to repeat them.

## Limits

- **Human effort is a model.** The METR method and the mid estimate
  agree, but they read the same transcripts. A time log over the next
  month would calibrate them. Prompts typed into subagent conversations
  aren't counted.
- **The transcripts miss about 30% of the cost.** Breakdowns use them
  scaled, assuming the missing calls spread evenly.
- **The shared GitHub login.** Agents and the maintainer post as one
  account, so GitHub data can't separate their actions. Only the
  `Assisted-by` trailer can.
- **One month, and the forecast rests on the last two weeks of it.**
  The forecast assumes that the remaining work looks like M2 and M3.
- **Traceability counts text.** An ID cited another way is missed, and
  the low FR share reflects where the project cites requirements as
  much as what's built.
- **Coverage is per package.** Tests in one package that exercise
  another don't count. Linux and Windows files weren't measured.
- **Fitness for use is judgment.** No VM was run for this note.
- **Outside numbers.** Most quality evidence on AI-written code comes
  from vendors, and the churn studies disagree. Agent A marked figures
  it took from search summaries, not the source. This note uses five
  of them as context only: Chen and Stratton, the ASOS back-test,
  Magennis's rule, Rempel and Mäder, and the ISO/IEC 25010 term.

## Recommendations

1. **Add a use criterion to V1.** Make a timed first run and a week
   of real use exit criteria of V1-M4-claude-end-to-end or
   V1-M5-approvals. Count approvals, refusals, fallbacks to host
   `claude`, time to the first prompt, memory and disk.
2. **Count approval prompts now.** Replay a week of the maintainer's
   host transcripts against the built-in profiles: hosts reached,
   WebFetch targets, `gh` writes. That gives an approvals-per-day number
   before V1-M5-approvals is designed.
3. **Decide the GraphQL allowlist for `gh` before V1-M4.** Either list
   the operations of `gh pr create`, `gh issue comment` and similar
   commands for the session's repositories (I502, SEC06-repo-writes
   still holds), or record that V1 doesn't support forge workflows in
   the guest.
4. **Repair the plan.** Update "Where things stand" and the package
   size estimates. Fix the M4.1 edge. File a package for the secret
   scanner, and give V1-M6-release-gate its dependencies.
5. **Shorten the gated waits.** The 19 attended steps and the
   `ready-for-human` queue (median 47 hours) are the human side of the
   critical path. Batch decisions and host steps into planned sessions,
   starting with I253.
6. **Make VM evidence visible.** Record each manual VM test run in its
   issue. When `wb-netd` and `wb-proxyd` land, add mutation testing on
   them and grow the fuzz corpora past one file.
7. **Look at last week's flaky CI runs** (7.4%) before they become
   normal.
8. **Repeat this measurement at each milestone,** as X32-project-metrics
   proposes, with the maintainer's time log to calibrate the human
   model.

**Status:** Measured 2026-11-02 over 2026-10-03 to 2026-11-01 at `6138336c`. V1 is about 28% implemented, the median forecast is January 2027 at the current attention, and quality is good to spec and unmeasured in use
