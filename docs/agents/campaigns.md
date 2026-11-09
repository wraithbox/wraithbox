# Campaigns and trains

A campaign runs waves unattended, for hours, within a budget the
maintainer sets. Its pull requests merge into a train branch instead of
`main`, and the maintainer reviews the train as a whole: one report page
and one pull request per campaign. `orchestration.md` describes a wave
and its agents, and this page describes what changes when waves run in
a campaign. The maintainer decided this way of working in I602.

## Layers

| Layer | What it is | What it does |
|---|---|---|
| Maintainer | a person | types `/campaign`, reads the report, vetoes, merges the train |
| Campaign manager | the session that runs the `campaign` skill, in the main checkout on `main` | opens the campaign, runs each wave through the script, handles vetoes, rebases and stops, writes the report, finishes up |
| Train reporter | the `train-reporter` agent, spawned by the manager when the campaign ends | writes the report's body: what the train changes, by content, with a change map |
| `scripts/campaign.mjs` | a deterministic script (`mise run campaign`) | keeps the ledger, checks the limits, starts one headless wave with its share of the budget, counts what it cost and made, renders the report |
| Wave | `claude -p "/wave --campaign <n> ..."`, one headless session per wave | the dispatcher of `orchestration.md`, "Runs", for one wave, merging into the train instead of `main` |

Each wave is a new session, so a campaign has no context limit of its
own. The manager keeps only each wave's short result. The state is on
GitHub, in the campaign issue, the train and its pull request, so a
later session or the other machine can resume a campaign.

## The campaign issue

Label `campaign`, title `Campaign <YYYY-MM-DD> (<milestone>, lane <lane>)`.
The maintainer's `/campaign` message is quoted in its body under
`## Arguments`, followed by `## Limits`, `## Train` (the branch and its
pull request) and `## Report` (the page). Each wave's result is a
comment. The issue closes when the campaign ends, with a comment that
says which limit or condition ended it. Its number is the campaign's
number: the train is `train/<n>` and the report `W<n>`.

## Budget

A campaign stops at the first limit it reaches:

| Limit | Default | Counted as |
|---|---|---|
| Hours | 12 | wall-clock time since the campaign started |
| Dollars | 240 | each wave's `total_cost_usd` from `claude -p`, at Anthropic list prices, subagents included |
| Issues | 50 | distinct issues handed to a builder, spike builder or researcher |
| Pull requests | 50 | distinct pull requests the campaign opened, against the train or `main` |

The script checks the limits before each wave and starts none once a
limit is reached. A wave gets what is left: its size is capped by the
issues and pull requests left, `--max-budget-usd` by the dollars left
minus a reserve of 5 percent of the dollar limit, and `--until` by the
hours left. The dispatcher starts no new review round after `--until`.
The script ends a wave that runs an hour past it, and counts that wave
at its full dollar share, since its cost is unknown.

The wave's dispatcher hands the wave to a lead in the background and
ends its turn. `claude -p` stops background tasks 10 minutes after the
last turn by default, which ended the first wave of I604 with nothing
built (I609). The script starts each wave with
`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`. The session then waits for
the lead and resumes when the lead reports, and the timeout above
bounds it. The final result's `total_cost_usd` counts every turn of the
session.

The reserve pays for the manager's own session and for finishing up.
The manager's session isn't metered by the script, and the report says
so. After a limit is hit the manager still finishes up ("Ending").

## The train

- **Branch.** `train/<n>`, cut from `main` when the campaign starts. When
  an earlier campaign's train is still open, the new train is cut from
  that train instead, and its pull request targets it. At most two
  trains are open at a time, and `/campaign` refuses to start a third.
- **The train pull request.** Opened as a draft from `train/<n>` into
  `main` (or the earlier train) with the report's first commit, so CI
  runs and the maintainer has one place to comment. It is marked ready
  when the campaign ends.
- **Builders branch from the train.** A wave's builders branch from
  `origin/train/<n>` and open their pull requests against it, with a
  `Campaign: #<n>` line in the body. CI runs on pull requests into
  `train/**` and on pushes to it.
- **Merging into the train.** The wave's dispatcher merges a pull
  request into the train with `gh pr merge <n> --rebase` when the train
  merge check holds ("What may ride the train"). No standing approval is
  needed, because nothing reaches `main`. Then it closes each issue the
  body names with `Closes #<k>`, labels it `on-train`, and comments
  `Done on train/<n> by #<pr>. Lands on main with the train.` Closing it
  lifts the `blockedBy` of its dependents, so later waves can build on
  it.
- **`main` moves.** When a pull request merges into `main` that the
  train needs, such as a harness change, the manager merges `main` into
  the train with a merge commit, never a rebase: branches are stacked on
  the train.
- **Landing.** The maintainer merges the train pull request with
  `gh pr merge <pr> --merge`, which keeps every commit and the merges
  from `main`. The manager removes the `on-train` label from its issues
  at the start of the next campaign.

## What may ride the train

A pull request into the train merges without the maintainer when all of
these hold, checked on GitHub (the `wave` skill has the commands):

- every check on its head commit passed, none is pending, and it is
  `MERGEABLE`;
- the latest code review, and the security review where
  `orchestration.md`, "A wave", step 4 asks for one, say
  `Verdict: approve` on the head commit or a commit a `re-checked:`
  comment names;
- its body names at least one issue with `Closes #<k>`, and none of
  them has `needs-host`;
- an issue with `needs-vm` has its VM test run in the pull request body:
  the command, the guest, and the result;
- it changes no harness path.

Specs, briefs, contracts, code, tests and the docs site ride the train,
`wb-vmd` included. Later `needs-vm` builders run what is on the train on
the dedicated Mac before the maintainer reviews it, and the dedicated
Mac exists so that this is acceptable. Its entitlements stay a harness
path.
The **harness paths** never do: `.claude/`, `AGENTS.md` and `CLAUDE.md`,
`.github/`, `.mise.toml`, `.golangci.yml`, `.swiftlint.yml`, `.vale.ini`
and `.vale/` except its vocabulary, `osv-scanner.toml`, `scripts/`, any
`.entitlements` file, and a new module in `go.mod` or a new package in
`Package.swift`. A builder whose work changes one of them opens its pull
request against `main` instead, and it waits for the maintainer. A
harness change can loosen the controls on the agents themselves, so it
is never mixed into a train.

A pull request that fails the check stays open against the train and
goes on the report.

## VM slots

On the dedicated Mac (`host-work.md`, "Machines"), `/campaign --lane mac`
runs up to two `needs-vm` builders at once (`--vm-slots 2`), the
NFR05-two-macos-vms ceiling. Each builder uses its own test bundles,
`i<n>/guest`, so the slots need no names. On the personal Mac
`--vm-slots` is 1, because the maintainer runs guests there too. No
lane takes `needs-host`.

## Vetoes

The maintainer vetoes a pull request on the train by commenting
`veto #<pr>` on the train pull request, with a reason if they like. The
manager reads the comments of trusted authors before each wave and
when it ends, and `/campaign land <n>` reads them after the campaign
has ended. For each veto, the manager:

1. Reverts the pull request's commits on the train with one
   `git revert` of their range, in a worktree of the train, and pushes.
   A rebase merge gives the commits new hashes, and
   `gh pr view <pr> --json mergeCommit,commits` names the last one and
   their count.
2. Reopens each issue the pull request closed, removes `on-train`, and
   comments why, quoting the veto.
3. When a later pull request on the train builds on the vetoed one (the
   revert conflicts, or one of its issues is blocked by a reopened
   one), it reverts nothing and puts the chain on the report for the
   maintainer instead.

## The report

One page per campaign: `docs/campaigns/W<n>-<slug>.mdx`, published at
`/campaigns/w<n>-<slug>/`, with a row in W00-index. It is committed on
the train, so it reaches `main` and the website when the train merges.
The script renders it from the ledger, GitHub, and a narrative file the
manager writes, and the manager commits it after each wave. Until the
train merges, `mise run doc:dev` on the train shows it.

The page has two layouts. While the campaign runs, the page shows the status
in the order below ("While it runs"). When the campaign ends, the
manager spawns the `train-reporter` agent, a fresh session that reads
the train's diff, the pull requests and every review, and writes the
page's body by content ("When it ends"). A fresh agent writes it, not
the manager: the manager's context is ordered by wave and pull request,
which is the order the maintainer can't review in, and reading every
diff and review would fill the manager's context at its end. Its cost
comes out of the manager's reserve.

### When it ends

The maintainer reads this page to decide whether to merge the train.
They know the design but not where the implementation stands. So the page describes the
changes in words that stand alone, and IDs only follow in brackets.
Top to bottom:

1. **Status line.** What ended the campaign, the train pull request, and
   how to veto.
2. **In short.** One bullet per change, biggest first, and the change
   beside the train that isn't merged.
3. **What needs you.** Decisions, approvals and attended steps, with
   the options.
4. **What changed where.** A change map: the components the train
   touches on the host, in the guest and on the internet, each marked
   as code changed, spec changed, newly designed, open beside the
   train, or a defect found.
5. **The changes, largest first.** One section per change, ordered and
   sized by lines of code, then spec size, then security weight: the
   problem, what was built or measured, the choices the maintainer can
   veto, inline, and what isn't covered.
6. **Review findings not resolved.** Security first, then optional code
   and docs findings, each with the issue that tracks it.
7. **Friction.** Flaky tests, VM instability, harness defects, conflicts
   and long review rounds.
8. **The run**, folded: the budget, the pull request table, the health
   figures and the waves, which the script renders.

The agent writes items 2 to 7 to `.scratch/campaign/<n>/report-body.md`
and the map to `docs/campaigns/W<n>-<slug>.svg`. The script puts the
rest around them (`.claude/agents/train-reporter.md`).

### While it runs


1. **Status line.** `Running`, or `Ended YYYY-MM-DD: <what ended it>`,
   then `Train: PR<k>`.
2. **Summary.** What the campaign did, from the manager, in a few
   sentences.
3. **Budget.** Each limit, what was spent, what is left.
4. **Pull requests.** One row each: pull request, issue, title, where it
   is (on the train, open, against `main`, closed), review rounds, the
   specs and contracts it changes, files changed.
5. **Choices to veto.** Two-way decisions the agents made, each with its
   pull request.
6. **Waiting for you.** Harness pull requests, `ready-for-human`
   decisions with their briefs, pull requests out of rounds, and
   commands the permission system refused.
7. **Attended queue.** `needs-host` and attended steps, with the
   commands.
8. **Health.** First-review approve rate, rounds per pull request,
   follow-ups filed, flaky tests filed, and each wave's cost, length and
   how its session ended.

## Ending

The manager ends a campaign when a limit is reached, a wave picks
nothing, a wave leaves nothing merged into the train, CI fails on the
train after a wave, or the maintainer says so. Then it:

1. Lets the running wave finish, or ends it ("Budget").
2. Removes the worktrees of merged pull requests, and puts the rest on
   the narrative's `waiting` list.
3. Spawns the `train-reporter` agent, renders the final report with its
   body, and commits it on the train.
4. Marks the train pull request ready, with the report's link and the
   list of issues and pull requests on it.
5. Updates the campaign issue, comments why it ended, and closes it.
6. Posts a macOS notification and gives the maintainer the report link
   in the session.

`/campaign land <n>` is the attended step before the maintainer merges:
it applies vetoes and merges `main` into the train when it conflicts.
When a veto reverted a change, the train reporter updates the body.
Then it waits for the train's CI and asks the maintainer to merge.
