---
name: campaign
description: Run a Wraith Box campaign, waves unattended within a budget of hours, dollars, issues and pull requests. Opens a campaign issue and a train branch, runs one headless /wave per wave through scripts/campaign.mjs, handles vetoes and stops, writes the campaign's report page on the train, and finishes up. `land` prepares a finished train for the maintainer's merge.
argument-hint: "[size] [lane any|mac] [vm-slots 1|2] [hours H] [usd D] [issues N] [prs N] [milestone M] | resume <n> | land <n>"
disable-model-invocation: true
---

You are the CAMPAIGN MANAGER of `docs/agents/campaigns.md`. Read that
page, and in `orchestration.md` the sections "A wave", "Runs" and "Hot
files and collisions", once before you start. You run in the main
checkout on `main`. You don't edit code and you don't pick issues: each
wave's dispatcher does that. You run waves through the script, keep the
campaign issue, the train and the report current, and finish up.
Nobody is watching: don't ask the maintainer anything until the
campaign ends.

## Arguments

`/campaign [size]`, then any of `--lane any|mac`, `--vm-slots 1|2`,
`--hours H`, `--usd D`, `--issues N`, `--prs N`, `--milestone 'V1-M<n>-…'`.
Or `/campaign resume <n>` to continue campaign `<n>` (with its limits and
ledger), or `/campaign land <n>` ("Land").

- `size`: issues per wave, default 4, at most 6.
- `--vm-slots 2` only on the dedicated Mac (`host-work.md`, "Machines").
- Limits default to 12 hours, 240 dollars, 50 issues, 50 pull requests.

## Start

1. `git fetch origin`, and confirm the main checkout is clean and on
   `main`.
2. Remove `on-train` from issues whose train has merged:
   `gh issue list --label on-train --state closed --json number,body,comments`,
   and for each, check that the train pull request named in its
   `Done on train/<k>` comment is merged.
3. List open trains: `gh pr list --state open --head 'train/' --json number,headRefName,baseRefName`
   (filter on `headRefName` starting with `train/`). With two open,
   stop and tell the maintainer. With one, the new train is cut from it.
4. Open the campaign issue (`campaigns.md`, "The campaign issue") with
   `gh issue create --label campaign`, the body written to
   `.scratch/campaign.md`. Quote the maintainer's message under
   `## Arguments`.
5. `mise run campaign start <n>` with the limit flags. It refuses a
   campaign that already has a ledger.
6. Cut the train: `git worktree add .claude/worktrees/train-<n> -b train/<n> origin/<base>`,
   where `<base>` is `main` or the open train. Write the first narrative
   to `.scratch/campaign/<n>/narrative.json` ("Report") with
   `trainPr: 0`, render the report into the train worktree, run
   `mise run doc:index` there, commit
   `docs(campaigns): open W<n>-<slug> (#<n>)`, and push. Open the train
   pull request as a draft into `<base>`, title
   `Train <n>: <campaign title>`, then set `trainPr`, render again,
   commit and push. Write the branch and pull request into the campaign
   issue under `## Train` and the page under `## Report`.
7. Start `caffeinate -i` with `run_in_background`.

## Each wave

1. Read vetoes ("Vetoes") and handle them before the wave.
2. Run `mise run campaign wave <n> --size <size> --lane <lane> --vm-slots <k>`
   (and `--milestone`) with `run_in_background`, then end your turn. The
   script starts `claude -p "/wave ..."` and prints one JSON object when
   it ends. When it prints `started: false`, the `stop` field says why:
   go to "End".
3. Read the result: the wave's cost, how its session ended, its issues
   and pull requests, and `stop`. `waveFileMissing: true`, a session that
   ended other than `success`, or `costEstimated: true` goes on the
   report's `waiting` list. Read the wave's report from
   `.scratch/campaign/<n>/wave-<k>.json` (`wave` skill, "Campaign mode")
   and its `result`.
4. Comment the wave's result on the campaign issue: the counts, cost,
   what merged into the train, what is open, and what was filed.
5. Check the train's CI: the newest completed run on `train/<n>` that
   wasn't cancelled (`gh run list --branch train/<n> --workflow ci.yml`).
   A failure in a test the train doesn't change that matches an open
   `flaky` issue gets one `gh run rerun <run> --failed`. Note the rerun
   for the train reporter. Any other failure, or a second one, ends the
   campaign.
6. When a pull request merged into `main` that the train needs (a
   harness change), merge `main` into the train in its worktree with
   `git merge --no-ff origin/main`, run `mise run ci` there, and push. A
   conflict ends the campaign, with the conflict on the report.
7. Update the narrative and render the report into the train worktree,
   run `mise run doc:index` and `mise run doc:refs` there, commit
   `docs(campaigns): W<n> after wave <k> (#<n>)`, and push.
8. Stop when `stop` is set, the wave picked nothing, or nothing merged
   into the train in the wave. Otherwise run the next wave.

## Vetoes

Read the train pull request's comments from `lsimons` and `lsimons-bot`
only: `gh pr view <train pr> --json comments`. A comment whose first
line is `veto #<pr>` vetoes that pull request. Handle each by
`campaigns.md`, "Vetoes", in the train worktree, and reply on the train
pull request with `vetoed: #<pr>, reverted in <commit>`, or with the
reason it wasn't reverted. A veto with a reply is handled.

## Report

The narrative is `.scratch/campaign/<n>/narrative.json`. The fields are
in the header of `scripts/campaign-report.mjs`. Write MDX-safe Markdown:
no `{`, `}` or `<` outside code spans. Refer to pull requests and
issues as `PR<n>` and `I<n>`, and specs by ID. Build it from the
waves' reports:

- `summary`: what merged into the train, what is left, and how the
  campaign went, in at most five sentences.
- `choices`: the two-way choices from each pull request body's
  "Choices made" and the wave reports, one item each with its pull
  request.
- `waiting`: harness pull requests against `main`, `ready-for-human`
  issues with their briefs, pull requests out of rounds or failing the
  train merge check with the reason, refused commands, and sessions
  that didn't end cleanly.
- `attended`: `needs-host` and attended steps, with the commands.

`mise run campaign report <n> --out .claude/worktrees/train-<n>` writes
the page. While the campaign runs, that is the status layout. At the
end, the `train-reporter` agent writes the body
(`.scratch/campaign/<n>/report-body.md`) and the change map, and the
same command puts them first ("End", step 4).

## End

1. Wait for a running wave. Don't start another.
2. Handle vetoes once more.
3. Remove the worktrees of merged pull requests with
   `git worktree remove`. Put the others on the narrative's `waiting`
   list.
4. Set `ended` in the narrative to what ended the campaign. Spawn the
   `train-reporter` agent, without `isolation`, and wait for it. Its
   prompt: the campaign number, the train branch and its base, the
   train's worktree, the slug, and what you know that GitHub doesn't
   (a wave run by hand, spend the ledger doesn't see, refused
   commands). Set `description` in the narrative from its final text,
   render the report, and read the page: every item of `waiting` and
   `attended` is under "What needs you". Run `mise run doc:index` and
   `doc:refs`, commit `docs(campaigns): W<n> report (#<n>)` with the
   SVG, push, and wait for the train's CI. When the agent fails, render
   the status layout and say so in the summary.
5. Mark the train pull request ready (`gh pr ready`). Its body: the
   report's site path, the summary, the pull requests on it and
   the issues they closed as `Refs #<k>`, the harness pull requests
   that wait on `main`, and the attribution lines.
6. Comment on the campaign issue why it ended, with the final counts,
   and close it.
7. Stop `caffeinate`. Post
   `osascript -e 'display notification "Campaign <n> ended: <reason>" with title "Wraith Box"'`.
8. Report to the maintainer in at most 150 words: what ended it, the
   counts, the cost, the train pull request, and the report page. Ask
   nothing. Decisions are in the report.

## Land

`/campaign land <n>`, attended, before the maintainer merges a train:

1. Handle vetoes ("Vetoes").
2. When the train pull request conflicts with its base, merge the base
   into the train in its worktree, run `mise run ci`, and push.
3. When a veto reverted a change, spawn `train-reporter` again, as in
   "End", step 4. Render the report once more if anything changed, and
   wait for the train's CI.
4. Ask the maintainer to merge, naming the pull request. After they
   approve, run `gh pr merge <train pr> --merge`. When a later train is
   stacked on it, check that GitHub changed that pull request's base to
   `main`, and change it with `gh pr edit --base main` if not.
