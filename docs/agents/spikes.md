# Spikes

This page says how a spike is filed, built, written up and cleaned
up. The spike builder (`.claude/agents/spike-builder.md`) and the
coordinators read it, and `working.md` has what every agent needs.

Spikes are the open questions `X01-model-credential` to `X24-openshell-artifacts` in X00-index. They are
first-class issues, and the first milestone is mostly spikes.

A spike follows the same steps as any issue (`working.md`, "Picking up
an issue"), but its code branch gets no pull request; the result pull
request does ("Result").

- **One issue per spike**, title `X<NN>-<slug>: <name>` as X00-index defines them, for example
  `X03-network-path: Network path`. Labels: `spike`, the area where the
  throwaway code will live, and the component it informs. The body quotes
  the question from X00-index and states the yes/no answer it needs, what
  will be measured, and which requirement IDs depend on it.
- **IDs are stable.** A new open question is added to X00-index first,
  with the next free `X` number, then filed. A spike is never renumbered.
- **Dependent work is blocked on the spike.** Any issue that builds on
  a spike's answer is `blocked by` the spike issue (GitHub relationship,
  see `planning.md`, "Dependencies and relationships"). `AGENTS.md`
  forbids building on an unanswered spike; the relationship is how a
  picker sees that.
- **Branch and code.** A spike runs on branch `spike/x<NN>-<slug>` (for
  example `spike/x3-network-path`), with its throwaway code under
  `spikes/x<NN>-<slug>/` on that branch. That code is not held to the
  project gates, is never merged to `main`, and the branch is kept as
  the record.
- **VM bundles.** A spike that boots a macOS guest keeps its bundles
  outside the worktree, in `~/Library/Caches/wraithbox-spikes/`:
  - `restore-images/` holds the shared restore images, which stay.
  - `x<NN>/` and `x<NN>-<anything>/` hold the bundles of spike
    `X<NN>`. They are throwaway once the result is written up and its
    numbers are committed to the spike branch.
  - `dev/` holds the test bundles that `mise run vm:install` made, such
    as `dev/base`, and `i<n>/` the clones that issue `<n>` provisions
    and boots (`host-work.md`, "Gotchas"). `mise run vm:clean i<n>`
    removes them, with the same refusals as `spike:clean`, plus a
    bundle whose VM runs, and a `KEEP` file anywhere in the tree.

  `mise run spike:clean x<NN>` removes those directories and prints
  what it removed and the space freed. It doesn't check for a running
  VM, so stop the VM and detach any volume mounted from a bundle first.
  It refuses `restore-images/`, a symbolic link, any path outside the
  cache, a directory with a volume mounted in it, and a directory that
  holds a `KEEP` file. Without an argument it lists the cache with
  sizes and doesn't remove anything. Agents may run it without asking.

  The spike builder runs it before it hands back a spike, and reports the
  space freed. When review or an open issue still needs the bundles,
  it keeps them. It comments `Keeping x<NN>/ for #<n>` on the
  spike's issue, naming the issue that needs them, and writes a `KEEP`
  file into `x<NN>/` whose first line names that issue
  (`#228: X08 data disk follow-up`). The maintainer removes the `KEEP`
  file once that issue closes. When a result merges, the coordinator
  reads the spike's issue for such a comment. On the personal Mac, it
  runs the task unless that comment's issue is still open
  (`orchestration.md`, "A wave", step 6).
- **Tag.** When the result pull request merges, the coordinator tags
  the spike commit that the result page links to with an annotated tag
  `spike-x<NN>-<slug>` (for example `spike-x19-terminal-filter`) and
  pushes it. The tag keeps the commit reachable if the branch is ever
  deleted or rewritten, so the permalink keeps working. The result page
  links to the head of the spike branch: a builder who adds a spike
  commit after the first result updates the permalink in the same
  round. A tag is never moved. If later spike work needs a new record,
  it gets its own result and tag.
- **Result.** The answer lands on `main` as a short pull request that
  adds `docs/spikes/X<NN>-<slug>.md`: the question, the answer
  (yes / no / yes-with-conditions), the measurements, what it means for
  the specs, and a permalink to the spike branch commit. It opens with
  a short **For review** block: what approving the answer commits to,
  the controls touched, the assumptions, and the open decisions. A
  result records the answer to a question, and keeps that block, which
  S01-spec-based-development keeps out of specs. The same
  pull request adds a review brief (`review-briefs.md`, the `brief`
  skill), so the maintainer can approve the answer without reading the
  spike code. The same pull
  request changes any spec the answer affects, or, if that is too big,
  files the spec change as an issue. It also links the result from the
  spike's entry in X00-index. That pull request says
  `Closes #<spike issue>`. A "no" that makes a requirement unachievable
  is written up as a spec change and the work stops there
  (`AGENTS.md`).
- **No spec text for an undecided option.** When the result leaves a
  decision open, the spec keeps failing closed (the stricter behavior,
  or "refused") until the maintainer decides, and the brief holds the
  options.
- **A spike only when** the answer could change a `SEC*` control or the
  design. A measurement that confirms the design becomes a test case
  (`C*`), not a spike.
