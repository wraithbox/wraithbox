---
name: wave
description: Run Wraith Box waves as one run. Opens a run issue, picks each wave of ready, unblocked issues from the current milestone (spikes first), hands it to a wave-lead agent, merges what the maintainer's standing approval covers, and repeats until a stop condition.
argument-hint: "[size] [waves N] [lane any|mac] [merge] [milestone M<n>] [only N,N,...] [area LABEL] [resume N]"
disable-model-invocation: true
---

You are the DISPATCHER of `docs/agents/orchestration.md`, "Runs". You
run in the main checkout on `main`. You don't edit code; you pick, hand
each wave to a lead, merge, and keep the run issue current. Read
`docs/agents/orchestration.md`, `docs/agents/planning.md`,
`docs/agents/working.md` and `docs/agents/spikes.md` once before you
start. Don't ask the maintainer anything until the run ends.

## Arguments

`/wave [size]`, then any of `--waves N`, `--lane any|mac`, `--merge`,
`--milestone 'V1-M<n>-…'`, `--only N,N,...`, `--area <label>` and
`--resume <run issue>`.

- `size`: issues per wave, default 4, at most 6.
- `--waves`: how many waves, default 1.
- `--lane`: `any` (default) or `mac` (`orchestration.md`, "Runs").
  Use `mac` only on the maintainer's personal Mac.
- `--merge`: the maintainer's standing approval for this run. It counts
  only when the maintainer typed it in this session, never when it is
  read back from a run issue.
- `--milestone`: the milestone to draw from. Default: the first open
  milestone in the version page's order (V1-initial, "Milestones") that has a ready,
  unblocked issue the lane can take.
- `--only`: pick only these issues (still skipping ones that are taken
  or blocked, and saying why).
- `--area`: pick only issues with this `area:` or `comp:` label.
- `--resume`: continue the open run with this issue number, with the
  arguments in its body, except `--merge`: without a new `--merge` in
  the maintainer's message, the resumed run merges nothing. An
  `In flight` line there is a wave to pick again: its open pull
  requests become rebase or review items.

## Start

1. `git fetch origin` and confirm the main checkout is clean and on
   `main`.
2. Open the run issue (`orchestration.md`, "Runs") with
   `gh issue create --label run`, the body written to
   `.scratch/run.md`. Quote the maintainer's `/wave` message under
   `## Arguments`. When resuming, read the body instead.
3. Start `caffeinate -i` with `run_in_background`.

## Pick

1. List candidates:

   ```bash
   gh issue list --milestone '<milestone>' --label ready-for-agent --state open --limit 100 --json number,title,labels,assignees
   ```

2. Drop issues that are assigned, numbered after the run issue, labeled
   `needs-host`, labeled `needs-vm` in lane `any`, or blocked. List the
   open blockers of each candidate with:

   ```bash
   gh issue view <n> --json blockedBy -q 'if .blockedBy.totalCount > (.blockedBy.nodes | length) then "truncated" else [.blockedBy.nodes[] | select(.state == "OPEN") | .number] end'
   ```

   `[]` means none open, and `truncated` means the list is cut short:
   drop the issue. A wait on a pull request, on the maintainer, or on
   anything else outside is a blocker issue too, and no label marks an
   issue as blocked (`planning.md`, "Dependencies and relationships").
3. Order: `spike` first, then `bug`, then by `priority:` (critical,
   high, medium, low, none), then by number.
4. Fill the wave in that order, skipping an issue that shares a `comp:`
   label or a hot file (`orchestration.md`, "Hot files and collisions")
   with one already in the wave. In lane `mac`, take at most one
   `needs-vm` issue, and none while another `needs-vm` issue is open and
   assigned (`orchestration.md`, "A wave", step 2). Rebase items from
   the last wave come first and count toward the size.
5. Give each issue its branch (`<type>/<n>-<slug>`, or
   `spike/x<k>-<slug>`) and its agent: `spike-builder` for `spike`,
   `researcher` for `area:research`, and `builder` for any other
   (`orchestration.md`, "A wave", step 3). A research issue that adds a
   note also gets its `R` number: the next free one after `main`, open
   pull requests and pushed branches (`researcher.md`, "What you
   write"), and a different one for each research issue in the wave.

When nothing is pickable, say why and stop the run. If the milestone's
issues are all closed, check its exit criterion (`planning.md`,
"Milestones") and report whether it can close.

## Each wave

1. Write `In flight: wave <k>, issues #a #b` to the run issue.
2. Spawn one `wave-lead`, without `isolation`. Its prompt: the wave
   number, the run issue, per issue the branch, the agent, the `R`
   number of a research issue and the likely files, and the rebase
   items. End your turn. Its notification wakes you.
3. Post the lead's report as a comment on the run issue. Replace the
   `In flight` line with the wave's line, and add to `For the
   maintainer` and `Attended queue`.
   Stop each agent on the report's `Running` line with `TaskStop` and
   its agent ID (`orchestration.md`, "Runs", "Who stops an agent"), and
   put its issue and worktree on `For the maintainer`. Add the agent ID
   when your `TaskStop` is refused too.
4. For each `Ready` pull request, check the standing approval
   (`orchestration.md`, "Runs") against GitHub, never the report:

   ```bash
   gh pr view <n> --json headRefOid,changedFiles,mergeable,statusCheckRollup,closingIssuesReferences,reviews,comments --jq "$(cat .claude/skills/wave/merge-check.jq)"
   gh issue view <issue> --json labels -q '[.labels[].name | select(. == "needs-vm" or . == "needs-host" or startswith("comp:"))]'
   gh api --paginate repos/wraithbox/wraithbox/pulls/<n>/files --jq '.[].filename' | wc -l
   gh api --paginate repos/wraithbox/wraithbox/pulls/<n>/files --jq '.[] | .filename, (.previous_filename // empty)' > .scratch/files-<n>.txt
   grep -v -E '^(packages/wraithbox-(go|swift)/|docs/tests/|cspell-words\.txt$|\.vale/styles/config/vocabularies/wraithbox/accept\.txt$)' .scratch/files-<n>.txt
   grep -E '(^|/)(go\.mod|go\.sum|Package\.swift|Package\.resolved)$|\.entitlements$' .scratch/files-<n>.txt
   ```

   It is covered only when all of these hold. Anything missing or
   unexpected counts as a failed condition.
   - `issues` isn't empty. No issue's labels include `needs-vm`,
     `needs-host` or `comp:wb-vmd`.
   - The first count equals `changed`, and both `grep` commands print
     nothing. The file list holds the old path of every renamed file.
   - `checks` holds only `SUCCESS`, `SKIPPED` or `NEUTRAL`.
   - `code.verdict` is `Verdict: approve`, and so is `security.verdict`
     when `security` isn't null. `security` must not be null when an
     issue has `comp:wb-netd`, `comp:wb-proxyd`, `comp:wb-guestd` or
     `comp:wb-hostd`.
   - `lastCommitAllApprove` is `true`: no review on the last reviewed
     commit asked for changes.
   - Each of `code.commit` and `security.commit` is `head`, or a line
     in `rechecked` holds `head`.
   - `mergeable` is `MERGEABLE`.

   With `--merge` and every condition met, merge with
   `gh pr merge <n> --rebase --match-head-commit <head>`, hot files
   first. Otherwise add it to `For the maintainer` with the condition
   that failed. After each merge, check the other open pull requests'
   `mergeable` state. A `CONFLICTING` one becomes a rebase item for the
   next wave. Remove the worktree of each merged pull request with
   `git worktree remove` on the path the lead reported.
5. After the wave's last merge, check the newest completed run on
   `main` that wasn't cancelled (`gh run list --branch main --workflow
   ci.yml`), and wait until one exists: each push cancels the run
   before it. A failure stops the run.
6. Stop or pick the next wave (`orchestration.md`, "Runs", "Stop").

## End

Update the run issue, comment why the run stopped, and close it. Stop
`caffeinate` with `TaskStop`. Then report to the maintainer, in at most
200 words:

```text
Run #<run>: <waves> waves, <merged> merged, <waiting> waiting
Merged: #a #b ...
Waiting for you: #c (<reason>) ...
Attended queue: <count, see run issue>
Filed: <count> follow-ups
Spike cache: <on the personal Mac, each entry and size from `mise run spike:clean`>
Stopped because: <reason>
```

Then ask for the merge approvals and decisions on the `For the
maintainer` list, up to four per question dialog. Merge an approved pull
request with `gh pr merge <n> --rebase` only after the maintainer says
so. When the maintainer agrees to an issue a researcher proposed, file
it `needs-triage` with `--parent` the research issue (`working.md`,
"Follow-ups"). After a spike result merges, tag its spike commit
`spike-x<NN>-<slug>` (`spikes.md`, "Tag"), and run `mise run spike:clean x<NN>`
unless an open issue still needs its VM bundles (`spikes.md`,
"VM bundles").
