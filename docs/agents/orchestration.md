# Orchestrating agents

How one coordinating session runs several builder and reviewer agents in
parallel against the GitHub issues. How a single agent picks up,
claims, and finishes one issue is in `working.md`. How issues are cut
and ordered is in `planning.md`, and spikes are in `spikes.md`. This
page is for coordinators: the dispatcher and the wave lead.

No hooks enforce the rules below. "Runs" says how several waves run
without the maintainer, and "Later" at the end lists what this setup
leaves out and when to add it.

## Roles

| Role | Agent | How many | Works in |
|---|---|---|---|
| Coordinator | the session itself, or the `/wave` skill | one | the main checkout, on `main` |
| Wave lead | `wave-lead`, one per wave of a run ("Runs") | one at a time | the main checkout, writing only `.scratch/` |
| Builder | `builder` | one per issue | its own worktree and branch |
| Spike builder | `spike-builder` | one per `spike` issue | its own worktree, a spike branch and a result branch |
| Researcher | `researcher` | one per `area:research` issue | its own worktree and branch |
| Reviewer | `code-reviewer` | one per pull request | its own worktree, detached at the PR head |
| Security reviewer | `security-reviewer` | per security-sensitive pull request, or a periodic pass | its own worktree |

The agents are defined in `.claude/agents/`, so model, effort, turn limit
and tools come from the file and not from the prompt. Spawn them by name
with `isolation: "worktree"`.

The coordinator does not edit code. It picks issues, dispatches agents,
posts reviews, decides what goes back to a builder, and asks the
maintainer to approve merges. Builder and reviewer never talk to each
other; the coordinator relays.

## A wave: the coordinator's flow

A wave starts only when the maintainer says so: they type `/wave`. A
prerequisite step they take, such as making a VM clone, is not a go.
Once it runs, nobody asks the maintainer anything until the wave ends.
A point that needs them gets a fail-closed default, or is left as it
is, and goes on the report's maintainer line. A wave is finished when
its pull requests are approved and green. Merge approvals and decisions
come as one batch at the end.

In a run ("Runs"), the dispatcher does steps 1, 2, 6 and 7, and a
`wave-lead` agent does steps 3 to 5 and reports back.

1. **Triage first** (`planning.md`, "Triage"). Every issue in the wave
   is `ready-for-agent`, has no open `blockedBy` issue, and is in the
   current milestone.
2. **Choose the wave.** Up to six issues. Avoid two issues with the same
   `comp:` label, or two that both change a hot file (below), unless one
   is stacked on the other. At most one `needs-vm` builder runs at a
   time. Every open, assigned `needs-vm` issue counts as running until
   it closes (its pull request merges) or is released. The count
   includes builders of other sessions and a builder sent back to fix
   review findings
   (`gh issue list --label needs-vm --state open --json number,assignees`).
   macOS runs at most two macOS guests per host
   (NFR05-two-macos-vms), and the agents share the maintainer's Mac
   with the maintainer's own guests. Tests that need a guest run on that
   Mac until the runner question is revisited near v1
   (S11-verification-and-spikes, I50). On the work laptop, take no
   `needs-vm` issue at all (`host-work.md`).
3. **Dispatch one agent per issue**, in one batch: `spike-builder` for
   an issue labeled `spike`, `researcher` for one labeled
   `area:research`, and `builder` for any other. The prompt holds
   only what changes per issue: the issue number, the branch name, the
   definition of done beyond the agent file's (normally: PR open, CI
   green, not merged), and what sibling builders are doing that could
   collide, including files they also edit. Before you give a builder
   design direction, check it against the flows the specs already define. When two issues
   change one mechanism, set one rule, send each builder its half, and
   merge the pull request that defines the rule first.
4. **Spawn a reviewer when a pull request opens.** `code-reviewer`
   always. Add `security-reviewer` when the PR has `comp:wb-netd`,
   `comp:wb-proxyd` or `comp:wb-guestd`, touches the host-guest socket
   handlers in `wb-hostd` or the git transport, or changes
   `internal/platform` self-sandboxing, local IPC, or secret store code.
   The prompt names the PR, the branch, and the specific risks to probe
   (a new parser of guest bytes without a fuzz target, a policy check
   that fails open, a `SEC*` control weakened). The reviewer returns its
   review as its final text: findings rated `must-fix`, `fold` or
   `drop` (`working.md`, "Follow-ups"), each with `file:line` and a
   concrete failure scenario, then `Verdict: approve` or
   `Verdict: needs changes`, then the attribution lines. The coordinator
   adds the first line `Code review, round <k>` or
   `Security review, round <k>`, and posts it with
   `gh pr review <n> --comment --body-file …` (GitHub refuses
   `--request-changes` from the PR's own author).
5. **Decide what goes back.** One message to the builder: required
   findings, suggestions to apply unless they read worse, findings to
   skip. Cheap `fold` findings go back even on approve. A finding that
   needs the maintainer (a spec change, a choice between designs) goes
   to the maintainer instead. When the two reviewers disagree on a
   security trade-off, take the strict side and say so in the report.
   Resume the builder with the message. Auto-compaction keeps its
   context small enough to resume (R03-fix-rounds-compaction).
   After `needs changes`, the same reviewer re-checks. After `approve`,
   the coordinator reads each later commit with `git show` and comments
   `re-checked: <commit link>`. A commit that does more than the
   findings asked for, or a rebase onto changes to the same code, gets
   a new review round instead.
6. **Merge queue.** The maintainer approves each merge, or a run's
   standing approval covers it ("Runs"). The coordinator then runs
   `gh pr merge <n> --rebase`. After each merge, list the open
   PRs with their `mergeable` state (GitHub says `UNKNOWN` for a minute);
   send anything `CONFLICTING` back to its builder with the likely files
   named. A PR that adds a gate or touches a hot file merges first, and
   the others rebase once onto it. After a rebase, diff the generated
   and list files against `main` yourself (`cspell-words.txt`,
   `github-refs.json`, B00-index and the other indexes) and rerun
   `mise run doc:index` and `doc:refs`: a rebase has duplicated lines
   there without a conflict. When a spike result merges, tag its
   spike commit and remove its leftover VM bundles (`spikes.md`, "Tag"
   and "VM bundles").
7. **Finish.** Report what merged, what review caught, what is left and
   which follow-ups were filed. On the personal Mac, the report also
   lists what is still in the spike cache, from `mise run spike:clean`
   without an argument. Close the milestone if its exit criterion
   holds (`planning.md`). Stop agents that still run with `TaskStop`.

Follow-ups that review or a wave names are handled before the session
ends, by the rules in `working.md`, "Follow-ups": fixed, named in a
pull request body, or filed with `--parent` the issue whose review
named them. Nothing waits in a transcript.

## Runs

Every `/wave` is a run of one wave or more (`--waves N`). The session
is the dispatcher: it picks each wave,
hands it to a `wave-lead` agent, keeps only the lead's short report,
merges what the standing approval covers, and picks the next wave. The
run's state is in a GitHub issue, so a later session, or the other
machine, can see it and resume it.

- **The run issue.** Label `run`, title
  `Run <YYYY-MM-DD> (<milestone>, lane <lane>)`. Its body has these
  sections:
  - `## Arguments`: the `/wave` arguments as given, and the
    maintainer's approval sentence quoted from the session, or `none`.
  - `## Waves`: one line per finished wave,
    `wave <k>: merged #a #b, open #c`, and while a lead runs,
    `In flight: wave <k>, issues #d #e`.
  - `## For the maintainer`: the pull requests that wait for a merge
    approval and why, the decisions with a recommendation each, and the
    commands the permission system refused.
  - `## Attended queue`: steps for the personal Mac, with the commands
    written out.

  Each wave's report is a comment on the run issue. The issue closes
  when the run ends, with a comment that says why.
- **Cutoff.** A run picks only issues with a lower number than its run
  issue, so it doesn't refill its own queue.
- **Lanes.** `--lane any`, the default, takes no `needs-vm` and no
  `needs-host` issue, and runs on either machine. `--lane mac` also
  takes one `needs-vm` issue at a time ("A wave", step 2), on the
  personal Mac. No lane takes `needs-host` (`host-work.md`).
- **Standing approval.** Given only by `--merge` in a `/wave` message
  the maintainer typed in this session. A `--merge` read back from a
  run issue on resume doesn't count. It covers a pull request when all
  of these hold, and the dispatcher checks each one on GitHub before it
  merges (the `/wave` skill has the commands):
  - it closes at least one issue, and none of the issues it closes has
    `needs-vm`, `needs-host` or `comp:wb-vmd`;
  - it got a security review when an issue it closes has
    `comp:wb-netd`, `comp:wb-proxyd`, `comp:wb-guestd` or
    `comp:wb-hostd`;
  - every changed file is on this list, and the list of changed files
    is complete:
    - `packages/wraithbox-go/`, except `go.mod` and `go.sum`;
    - `packages/wraithbox-swift/`, except `Package.swift`,
      `Package.resolved` and any `.entitlements` file;
    - `docs/tests/`, `cspell-words.txt`, and
      `.vale/styles/config/vocabularies/wraithbox/accept.txt`;
  - every check on its head commit passed, and none is pending;
  - the latest code review, and the latest security review when it got
    one, say `Verdict: approve`, no other review on that commit asked
    for changes, and either the reviews were posted on the head commit
    or a `re-checked:` comment names the head commit.

  Everything else goes on the `For the maintainer` list and waits:
  specs, briefs, spike results, the docs site, the harness, CI, gates
  and lint settings, dependencies, and VM, image and Keychain work. Those can do
  damage that a review doesn't catch, so they stay attended. The dispatcher merges, never the lead,
  so the approval is in the transcript that the auto-mode classifier
  reads.
- **Who stops an agent.** The lead stops its own background commands,
  and tries `TaskStop` on an agent it spawned that still runs when it
  reports. In some harness versions that agent's task is in the
  lead's registry, and in others it isn't and `TaskStop` refuses
  with "cannot stop it". A refused agent goes on the report's
  `Running` line. The dispatcher stops each agent on that line with
  `TaskStop` and its agent ID, and puts its issue and worktree on
  `For the maintainer`, with the agent ID when its `TaskStop` is
  refused too.
- **After a merge.** A pull request that turns `CONFLICTING` goes into
  the next wave as a rebase of its branch, and a failed CI run on `main`
  stops the run.
- **Stop** after `N` waves, after a wave with nothing to pick, after a
  wave that left nothing merged or ready, or when the dispatcher's
  context is past about half. Then update the run issue, close it, and
  give the maintainer the batch: merge approvals and decisions, up to
  four per question dialog.
- **Keep the Mac awake.** The dispatcher starts `caffeinate -i` in the
  background at the start and stops it at the end.

## Effort

Each agent runs at the effort in its file, and a spawn doesn't set
one. Builders, spike builders, researchers and code reviewers run at
medium. Security reviewers run on Fable at high, or on Opus when Fable has no
credits. Wave leads run at high. R02-builder-effort has the trial
behind this.

## Hot files and collisions

- **Shared config:** `.mise.toml`, `.github/workflows/ci.yml`,
  `AGENTS.md`, `packages/wraithbox-go/go.mod` and `go.sum`,
  `Package.swift` and `Package.resolved`. Two issues that change these in
  one wave means one rebases through conflicts. Keep the `ci` task list
  and the workflow steps in the same order in both files.
- **Contracts:** the `.proto` files and the `internal/platform`
  interfaces. A change there ripples into every implementation; land it
  first, alone, and stack dependents on it.
- **Specs:** two branches editing the same spec section collide in
  meaning even without a git conflict. One issue per spec section per
  wave.
- **New gates on old code:** a PR that adds a lint rule, fuzz target
  requirement or CI job forces every later PR to comply. Tell the other
  builders what landed on `main` since they branched.

## Working with the platform

- **The coordinator waits by ending its turn.** Each agent's
  notification wakes it. It does not sleep or poll, except in a lead's
  background wait (below). It waits in the foreground only for a
  check it started itself (`mise run ci`, `gh pr checks --watch`,
  `gh run watch`).
- **An interim notice is not a hand-back.** In some harness versions,
  an agent that ends its turn while its own background command runs
  sends a notice with status `completed`, a summary that says it "is
  waiting on its own background work", and a note that the result "may
  be interim". It still works, and the same task ID notifies again
  when it hands back. A lead that then ends its turn with no task of
  its own running is told by the harness to report at once, and the
  pull request that agent opens later isn't reviewed. So a lead keeps a
  background wait of its own running until each agent it spawned has
  handed back
  (`wave-lead.md`, "Waiting").
- **What every agent does** (`cd`, the `code-review` skill, never
  `git stash`, `.scratch/`, short hand-backs) is in `working.md`,
  "Working with the platform".
- **Concurrent agents are capped.** A spawn that hits the cap fails with
  a clear message; retry when one finishes.
- **Resuming an agent** with a message keeps its context, and the
  resumed agent reads that whole context again on every call. A
  subagent's prompt cache lasts five minutes unless its agent file sets
  `experimental: cacheTtl: 1h`, as `builder`, `spike-builder`,
  `researcher` and `wave-lead` do. A resume after the cache expired pays the whole context as a cache
  write (R03-fix-rounds-compaction).
- **The Pages deploy.** A deploy run that waits at the `github-pages`
  environment gate holds the `pages` concurrency group, and every newer
  deploy is cancelled while it waits. After a docs merge, check with
  `gh run list --workflow deploy.yml --limit 3`, and cancel a run stuck
  at `waiting`. Until I243 is fixed, a green Docs check doesn't prove
  the deploy builds, so check the deploy run itself.

## History

Builders may `git push --force-with-lease` their own branch after a
rebase when nothing is stacked on it. Nobody force-pushes `main`, pushes
to `main` directly, deletes a branch they did not create, or merges
without the maintainer's approval. Spike branches are kept.

## Later

Left out on purpose, to add when the need shows:

- **Guardrail hooks** that enforce this page with `PreToolUse` hooks:
  a Bash guard (no force push, no push to `main`, no `git stash`, merge
  only by the dispatcher, no long `sleep` or `gh` polling), read-only
  Bash for reviewers, a trusted-comments issue reader, a deny rule on
  editing `.claude/settings.json`, and an auto-formatter after edits.
  Add a hook only when a run breaks the rule it would enforce.
- **Integration branches**, which batch many small approved branches
  into one pull request. Pull requests here merge one by one.
- **Periodic harness reviews.** Instead, each milestone gets one review
  when it closes: pull requests merged, first-review approve rate,
  rounds per pull request, follow-ups filed per issue closed,
  `ready-for-human` count and age, and tokens by role from the session
  transcripts. It files at most five issues.
