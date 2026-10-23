# Planning: issues, milestones, and triage

How work on Wraith Box is cut into GitHub issues, so that many agents can
pick it up in parallel without asking. `orchestration.md` says how the
agents then build and review it. Generic labels and issue templates (type,
priority, triage status) belong to the issue-tracker setup; this page uses
them by name.

This page is for coordinators: the dispatcher, a wave lead, and a
session that files or triages issues. The labels a worker sees,
follow-ups and decisions are in `working.md`, and spikes in
`spikes.md`.

## Where work comes from

The specs in `docs/spec/` are the plan. Work is filed as issues that
implement a spec section, answer a spike, or fix something. An issue that
would change a design decision is a spec change first (`AGENTS.md`,
"Source of truth"): file it with `area:spec`, and block the
implementation issues on it.

Order of work, top down:

1. The current **milestone** for the lane: the first open one in the
   version page's order ("Milestones" below).
2. Within it, **spikes** before anything that depends on them.
3. Then by `priority:` label, then by issue number.

## Milestones

Milestones group issues into deliverable steps toward a version. Each
milestone is defined on its version page (V1-initial for the first),
and is a GitHub milestone of the same name, not a label.

- **Name:** `V<N>-M<N>-<slug>`, for example `V1-M1-spikes-closed`.
  Numbers are fixed once merged (S01-spec-based-development), so a
  milestone added later takes the next free number.
- **Order:** the version page lists the milestones in the order they
  are worked, which can differ from their numbers. Each lane of a run
  (`orchestration.md`, "Runs") takes the first open milestone in that
  order that has ready work for the lane.
- **Description:** the exit criterion from the version page, with the
  requirement IDs and spikes it covers. Example: `X01-model-credential
  to X09-keychain-unsigned answered and written up in docs/spikes/;
  specs updated where an answer changed the design. Covers
  SEC04-no-guest-secrets, NFR01-startup, NFR02-fs-speed.`
- **Membership:** every `ready-for-agent` issue is in exactly one
  milestone. An issue without one is not picked. Follow-ups found during
  a milestone go into it if they block its exit criterion, otherwise
  into the next one in the version page's order, or none
  (`needs-triage`).
- **`post-V1`:** a GitHub milestone for work deferred past the version,
  not a milestone of the version page. Its issues have no triage
  status label, and no run picks from it. They are triaged again when
  the next version is planned.
- **Closing:** the milestone closes when its exit criterion holds, not
  merely when its issues are closed. The closing agent or maintainer
  checks the criterion and comments on the last issue with the evidence.
  Every brief of the milestone has a current status line, and every
  open issue it decided is in a milestone.
- **Changing one** means changing the version page first, then the
  GitHub milestone.
- **Due dates** are optional and set by the maintainer only.

```bash
gh api repos/wraithbox/wraithbox/milestones -f title='V1-M1-spikes-closed' -f description='...'
gh issue edit <n> --milestone 'V1-M1-spikes-closed'
gh issue list --milestone 'V1-M1-spikes-closed' --label ready-for-agent --state open
```

## Writing a ready-for-agent issue

The builder starts with no context and does not ask questions, so the
issue (body plus any `Decision (YYYY-MM-DD):` comment) holds:

- What exists today, with file paths, so the builder reads before it
  writes.
- The spec sections and requirement IDs (`FR*`, `NFR*`, `SEC*`)
  it implements, and the `SEC*` controls it must not weaken.
- Each decision the work needs, answered. Where two designs were
  possible, the one chosen and why, in a clause.
- **Done when:** checks a builder can run: tests that must exist (a fuzz
  target for every new parser of guest bytes), the `mise` tasks that must
  pass, the spec status to update.
- Out of scope, where a reader could reasonably assume otherwise.
- Blockers, set as relationships.
- Labels (`working.md`, "Labels") and a milestone as above.

An issue that lacks a decision only the maintainer can make is
`ready-for-human`, with the problem, the options with their cost, and one
recommendation, so the maintainer can decide from the issue alone. When
the decision touches a `SEC*` control or more than one spec, add a
review brief as well (`review-briefs.md`, the `brief` skill).

An issue that holds a list (ideas, deferred items) is not a task. Triage
splits it: each kept entry becomes its own sub-issue
(`gh issue create --parent <list>`), each dropped entry is named in the
closing comment with the reason, and the list issue closes.

## Large doc changes

A rewrite that spans many specs or pages (a split, a restructure, a
style pass) gets a fixed definition of done before it starts, at most
one revision of the plan after a pilot page, and no design changes on
the way. Findings go on a list and are settled after it merges.

## Dependencies and relationships

Use GitHub's native relationships rather than body text (needs gh 2.94 or
later):

```bash
gh issue create --title '...' --body-file <file> --blocked-by 12 --parent 7
gh issue edit 34 --add-blocked-by 12      # order: 34 can't start before 12
gh issue edit 34 --parent 7               # origin: 34 was split from 7
gh issue view 34 --json blockedBy,blocking,parent,subIssues,milestone
```

- **Blocked by** is for order only. A follow-up does not block the issue
  it came from.
- An issue is blocked when it has an open `blockedBy` issue, and only
  then. GitHub drops the block when the blocker closes, so nothing has
  to be cleaned up. A label or a line in the body doesn't block an
  issue.
- **Blocked by a pull request:** a pull request can't be a blocker
  target, so block on the issue the pull request closes. A pull request
  without an issue gets one, and its body says `Closes #<it>`, so the
  merge closes the issue and lifts the block. A stacked pull request
  gets `main` as its base before it merges (`working.md`,
  "Stacked pull requests"). The closing keyword fires on a merge into
  `main`. A pull request closed without merging leaves the issue open,
  which keeps the block, as it should.
- **Waiting on the maintainer** (a decision, or a machine setup such as
  a test account): file an issue for that action, labeled
  `ready-for-human`, and block the waiting issue on it. The maintainer
  closes the action issue once done.
- **Any other outside wait** (an upstream release, a vendor fix) also
  gets an issue, closed when the wait ends, and the waiting issue is
  blocked on it.
- **Parent** records where an issue came from: a split, a kept entry of a
  list, or a follow-up from a review of issue N.
- A pull request links its issue with `Closes #N` in the body. To mention
  an issue without closing it, write `#N` with no closing keyword in
  front.

## Triage

Triage turns `needs-triage` issues into one of: `ready-for-agent` (with
the decisions written down, and any blockers set as relationships),
`ready-for-human` (the one action named), or closed with a reason.

- Record each decision in a comment that starts `Decision (YYYY-MM-DD):`
  or `Triage (YYYY-MM-DD):`, then change the labels. The comment is what
  the next agent reads.
- Agents decide routine triage themselves (labels, duplicates, splits)
  and report what they did. They ask the maintainer only when a decision
  touches a spec, a rule in `AGENTS.md`, the scope, or the cost. A
  `Decision` comment holds only the maintainer's answer (`working.md`,
  "Decisions").
- Fix stale paths in an old body in the triage comment rather than
  leaving them for the builder.
- Set `needs-vm` and `needs-host` on every `ready-for-agent` issue that
  needs them (`host-work.md`, "The labels"). A run merges issues without
  either label under the maintainer's standing approval, so leave a
  label off only when the work clearly needs neither.

Every comment, issue body and pull request body an agent writes ends with
the attribution lines from `AGENTS.md`.
