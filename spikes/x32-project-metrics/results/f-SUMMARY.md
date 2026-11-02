# X32 agent F: fitness for use (saved by coordinator from the agent's hand-back, condensed)

## Assessment (judgment)

- About 80% that a plain session works end to end on day one (prompt, edit, commit, `wb diff`, `wb land`) in a Go, Node or Python repo.
- 30 to 40% that the maintainer runs most Claude sessions through `wb` in the first month.

The reasons:

- **No exit criterion measures use.** NFR01-startup was demoted on I111 and NFR02-fs-speed on I19. Only NFR03 (which has no number) and NFR06 gate the release.
- **Nothing a user touches can be tried yet.** 16 of the 22 commands print "not implemented yet", `wb claude` among them. `wb-netd` and `wb-proxyd` are stubs.
- **Real-VM tests run only by hand.** That covers `TestC11HostdVM`, `TestC09ShellVM` and the attended checks on #471.
- **The first prompt only works in M4,** after M3 and M7.

## Risks

1. **The work loop differs from host Claude Code.**
   - The IDE can't see files, because there are no host mounts.
   - No `/ide`, MCP, plugins, hooks, port forwarding or clipboard.
   - Each session is a fresh worktree, without `node_modules` or ignored files.
   - Work is seen only through `wb diff` and `wb land`.
   - All of this was decided deliberately (V1-08, S14, S06-08).
2. **The forge workflow is cut.**
   - The guest has no forge credential, and GraphQL mutations are refused: #502 lists `gh pr create` as refused.
   - So the maintainer's own agent and wave workflow can't run inside `wb claude` in V1.
   - Neither can Wraith Box's own development: its VM work, and `xcodebuild test`, which fails in a guest (X06).
3. **Approvals and refusals.**
   - Unknown names are held 4 s at DNS, then refused, so the first try fails.
   - Docs sites, GitHub release assets, CDNs and probably WebFetch will prompt. The model API profile doesn't cover WebFetch.
   - The dependency gate refused 2 of 10 real installs for version age and 2 of 10 for a CRITICAL CVE (X21).
   - The notification helper isn't designed yet (M5.4).
4. **Start time and per-session cost.**
   - No warm start before M6.4.
   - `wb vm start` took 14.1 s and 19.4 s (C11), against a 10 s goal.
   - The first session in a project adds export and clone: 33 s for 426 MiB (X07).
   - `npm ci` runs again in each worktree at 71% of host speed (X05).
   - `wb vm stop` takes 30 s (#414).
5. **First run and upkeep.**
   - Needs an Xcode Apple ID and team, a build from source, a 26.6 GB restore image and about 7 min of install. The Xcode variant adds about 25 min. Never timed. Estimated at 30 to 90 min in total.
   - There is no release build.
   - A free team's profile may last 7 days (X29).
   - Claude Code updates need an image rebuild, and the image pins the stable channel.

Smaller risks: no `brew install`, Xcode URLSession clients fail closed, 8 GB per VM on a 16 GB host, disk growth, terminal rough edges (#474, #475, #378) and guest git not fsyncing (#231).

## Alternatives

V1 uniquely combines:

- a macOS guest with Xcode behind a separate kernel;
- default-deny egress with TLS inspection;
- no secrets in the guest;
- scoped repository writes;
- a dependency gate;
- a filtered terminal.

Every alternative is cheaper to live with. Neither the specs nor the research notes compare V1 with the real baseline: host Claude Code with its own permission prompts and sandboxing, or a devcontainer.

## Missing evidence

Cheap now:

- Replay transcripts against the policy profiles to estimate approval prompts per day and `gh` write calls.
- Run the X21 check on the maintainer's active repos.
- List the top 10 workflows and mark each as works, works differently, or doesn't work.
- Check WebFetch against the model API profile.

At M4:

- A timed first run on a clean account.
- Time to the first prompt in a session.
- A dogfooding week as an M4 or M5 exit criterion.

## Doubts

- No VM was run. Timings come from spikes and from the two C11 runs.
- The WebFetch preflight and the 7-day free-team profile are not verified.
- The workflow assumption rests on CLAUDE.md and `docs/agents`.
- Open PRs were not checked.

## Files

journey.md, nfr.md, friction.md, alternatives.md, cli-run.txt
