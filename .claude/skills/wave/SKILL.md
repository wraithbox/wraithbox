---
name: wave
description: Coordinate one wave of Wraith Box issues. Picks ready, unblocked issues from the current milestone (spikes first), spawns one builder per issue and a reviewer per pull request, relays verdicts, and merges only on the maintainer's approval.
argument-hint: "[size] [milestone M<n>] [only N,N,...] [area LABEL]"
disable-model-invocation: true
---

You are the COORDINATOR of `docs/agents/orchestration.md`, "A wave". You
run in the main checkout on `main`. You don't edit code; you pick,
dispatch, relay, and report. Read `docs/agents/orchestration.md` and
`docs/agents/planning.md` once before you start.

## Arguments

`/wave [size]`, then any of `--milestone 'M<n>: …'`, `--only N,N,...`
and `--area <label>`.

- `size`: issues per wave, default 4, at most 6.
- `--milestone`: the milestone to draw from. Default: the open
  milestone with the lowest `M<n>`.
- `--only`: pick only these issues (still skipping ones that are taken
  or blocked, and saying why).
- `--area`: pick only issues with this `area:` or `comp:` label.

## Pick

1. `git fetch origin` and confirm the main checkout is clean and on
   `main`.
2. List candidates:

   ```bash
   gh issue list --milestone '<milestone>' --label ready-for-agent --state open --limit 100 --json number,title,labels,assignees
   ```

3. Drop issues that are assigned, labeled `blocked`, or have an open
   `blockedBy` (`gh issue view <n> --json blockedBy`). Drop issues whose
   body has a `Blocked by #<pr>` line for an open PR.
4. Order: `spike` first, then `bug`, then by `priority:` (critical,
   high, medium, low, none), then by number.
5. Fill the wave in that order, skipping an issue that shares a `comp:`
   label or a hot file (`orchestration.md`, "Hot files") with one
   already in the wave. Say which you skipped and why.
6. Tell the maintainer the wave (number, title, branch name for each)
   and start. No confirmation needed unless the wave touches a spec
   decision or `area:agents`.

When nothing is pickable, say why (empty milestone, everything blocked,
everything taken) and stop. If the milestone's issues are all closed,
check its exit criterion (`planning.md`, "Milestones") and report
whether it can close.

## Run

Follow `orchestration.md`, "A wave", steps 3 to 7:

- Spawn one `builder` per issue with `isolation: "worktree"`, in one
  batch. Prompt: issue number, branch name (`<type>/<n>-<slug>`, or
  `spike/x<k>-<slug>` for a spike), and the other builders' issues and
  the files they are likely to touch.
- When a builder hands back a PR, spawn `code-reviewer` (and
  `security-reviewer` when `orchestration.md` says so) with
  `isolation: "worktree"`, naming the PR, branch, issue and the risks to
  probe.
- Post each review on the PR with `gh pr review <n> --comment
  --body-file .scratch/review-<n>.md`, then send the builder one message
  with what to fix.
- Wait by ending your turn; notifications wake you. Never poll.
- When a PR is green and approved, ask the maintainer to approve the
  merge. Merge with `gh pr merge <n> --rebase` only after they say so.
  Check the other PRs' `mergeable` state after each merge. After a
  spike result merges, tag its spike commit `spike-x<NN>-<slug>`
  (`planning.md`, "Spikes", "Tag").
- File follow-ups as issues (`--parent` the issue whose review named
  them) before you report.

## Report

At the end, in at most 200 words:

```text
Wave <date>: <merged count> merged, <open count> open
Merged: #a #b ...
Open: #c (<state, what it waits on>) ...
Left out: #d (<reason>) ...
Review caught: <one line per notable finding>
Filed: #e <title> ... (or none)
For the maintainer: <decisions needed, or none>
Agents still running / worktrees left: <list, or none>
```
