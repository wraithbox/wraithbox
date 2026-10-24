# Completing work

How a session finishes its work. A builder, spike builder or researcher
follows its file in `.claude/agents/` instead, and a run follows
`orchestration.md`, "Runs".

## Interactive session

A session the maintainer works in directly.

1. **Done** means the branch is pushed, its pull request is open, and CI
   is green. Merge only when the maintainer says so in plain words.
   Don't ask what "complete" means, and never push to `main`.
2. **Branch.** `<type>/<issue>-<slug>` from `origin/main`
   (`working.md`, "Picking up an issue", step 4). Work without an
   issue uses `<type>/<slug>`.
3. **Gate.** `mise run ci` before every push (`AGENTS.md`, "Session
   completion").
4. **Commit.** Check `git status` and `git diff --cached`, then commit as
   a Conventional Commit that ends with the attribution lines of
   `AGENTS.md`. No `Signed-off-by`.
5. **Push.** `git push -u origin <branch>`. After a rebase,
   `git push --force-with-lease`, and only on a branch nothing is
   stacked on.
6. **Pull request.** `gh pr create`, with `Closes #<n>` when it finishes
   an issue, the requirement IDs it implements, a "Choices made" list
   for the calls the session made (`working.md`, "Decisions"), and the
   attribution lines.
7. **CI.** `mise run ci-watch`. On a failure, `gh run view <id>
   --log-failed`, fix, push, watch again.
8. **Merge**, on the maintainer's word: `gh pr merge <n> --rebase`, or
   `--merge` when another branch is stacked on it. Then
   `git pull --ff-only` in the main checkout, remove your own worktree,
   and watch the run on `main`. After a docs merge, check the Pages
   deploy too (`orchestration.md`, "Working with the platform").
9. **Issues.** Follow-ups go by `working.md`, "Follow-ups". Anything the
   next session needs goes in an issue comment, never only in memory.

Never `git stash`.
