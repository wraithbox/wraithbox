# X32 - Measuring V1 progress, effort and quality

**Purpose:** The result of the spike for I543. Can progress toward V1,
human and AI effort, velocity and quality be measured again and again
from git, GitHub and Claude Code transcripts, at a cost the project can
pay each release or month?

**Requirements:** none. The spike measures the project, not the
product.

**Brief:** B543-project-metrics

## For review

- **Decides:** whether to make the measurement of
  R09-v1-progress-and-quality repeat at each milestone close, as I544
  proposes. It should, with the conditions below.
- **You are approving:** the answer and these conditions:
  - totals of AI cost come from Claude Code's `cost-state` records,
    because the transcripts miss about 30% of the calls;
  - human effort stays a model, reported as a range, until a time log
    calibrates it;
  - GitHub data can't tell the maintainer from an agent, because both
    post as one login. The `Assisted-by` trailer and the transcripts
    tell them apart;
  - raw data stays on the maintainer's disk, and only aggregates are
    published.
- **Controls touched:** none.
- **Assumed:**
  - that the maintainer's attention patterns are the right input for
    the forecast;
  - that the report's findings, rather than its numbers, are the reason
    to repeat it.
- **Open decisions:** whether to do I544, and how often: each milestone
  close (recommended) or monthly.

## Question

Can progress, effort and quality be measured repeatably from the data
the project already has, and what does a run cost?

## Answer

**Yes, with conditions.** The agents measured the first month in 6 to
13 minutes each, in parallel, with about 0.86 M tokens in total. The
coordinator combined their outputs into R09-v1-progress-and-quality.
The scripts run again on new data, apart from hard-coded paths. The
report turned up several findings that no other check had flagged:

- **Forge workflows.** The empty GraphQL allowlist of I502 breaks
  `gh pr create` in the guest.
- **Use.** V1's exit criteria don't measure it.
- **Plan drift.** The plan's package sizes are off by 3 to 5 times,
  and it has stale dependencies and a missing package.
- **Flaky CI.** The flaky-run rate jumped to 7.4% in the last week.
- **The forecast.** The date depends on the maintainer's attention
  far more than on agent capacity.

## Measurements

| What | Works | Limit |
|---|---|---|
| Commits, lines, structure, line survival | yes, from `git log`, `git ls-tree` and `git blame` | moves across files count as new lines |
| Issues, pull requests, reviews, CI | yes, from GitHub REST and GraphQL | the review `must-fix` count is a heuristic |
| Who did what on GitHub | partly | one login for agents and maintainer, so only the trailer separates them (13 commits, 8 not from Dependabot, and 12 comments without it) |
| AI tokens and cost | yes, deduplicated by message ID | the transcripts miss about 30% of the cost. `cost-state` has the total, for main sessions only |
| Agent time and concurrency | yes, from event timestamps | a long tool call looks idle |
| Human time | as a model: METR's 10-minute windows gave 59 h, and this spike's model gave 27, 63 and 117 h as low, mid and high | no ground truth. Time away from the keyboard is invisible |
| Spec traceability | yes, by ID text | depends on how code cites IDs |
| Coverage, complexity, doc comments, dependencies | yes | coverage is per package, on darwin only |
| Remaining work and critical path | yes, from issues, `blockedBy` links and the plan | unfiled packages are sized by average |
| Forecast | yes. Methods built separately agree within 15% | rests on 14 days of build history |
| Fitness for use | as judgment, from specs and spike results | needs a VM and real use to become measurement |

Things that went wrong and would go wrong again without I544:

- **Summaries.** The harness refuses report files written by
  subagents, so four of the six agents returned their summaries as
  text, and the coordinator saved them.
- **Units.** The first forecast run mixed units (lines against issue
  counts) and doubled the date. Checking the units against a second
  method caught it.
- **Lanes.** A lane test matched `nonvm` as `vm`.

The spike code is on
[`spike/x32-project-metrics`](https://github.com/wraithbox/wraithbox/tree/d250e4a005feb9c4c94b4bb5830dc5ee40c2ed3f/spikes/x32-project-metrics),
with each agent's summary, the aggregate outputs and the agents' cost
(`results/wave1-cost.md`) under `results/`.

## What it means for the specs

Nothing in `docs/spec/` changes. R09-v1-progress-and-quality
recommends changes to the plan (`docs/implementation-plan.md`) and to
V1's exit criteria, and it leaves those to the maintainer. I544
proposes the harness changes: an agent file, a `mise` task and a
time-log format.

**Status:** Answered 2026-11-02: yes, with conditions. Repeat it at each milestone close through I544, using `cost-state` for cost and a time log to calibrate the human model
