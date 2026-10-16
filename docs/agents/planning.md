# Planning: issues, labels, spikes, and milestones

How work on Wraith Box is cut into GitHub issues, so that many agents can
pick it up in parallel without asking. `orchestration.md` says how the
agents then build and review it. Generic labels and issue templates (type,
priority, triage status) belong to the issue-tracker setup; this page uses
them by name.

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

## Labels

Every open work issue carries:

- exactly one **type**: `bug`, `enhancement`, `spike`, `documentation` or
  `chore`;
- one **triage status**: `needs-triage`, `needs-info`, `ready-for-agent`
  or `ready-for-human` (none in the `post-V1` milestone,
  "Milestones"), plus `needs-vm` when it needs a running macOS guest
  and `needs-host` when it needs the maintainer's Mac itself
  (`host-work.md`);
- at least one **area** label (`area:*`);
- zero to two **component** labels (`comp:*`), when the work belongs to a
  process from S04-architecture;
- optionally a `priority:` label.

No label says an issue is blocked. A `blockedBy` relationship does
("Dependencies and relationships").

The area and component labels are recorded in `.github/labels-areas.yml`
(the generic ones in `.github/labels.yml`, see `issue-tracker.md`).
Change the file, then apply it with `mise run gh:labels`.

### Area: where the change lands

| Label | Covers | `mise run` tasks to run while working |
|---|---|---|
| `area:go` | `packages/wraithbox-go` | `go:lint`, `go:test`, `go:cross` |
| `area:swift` | `packages/wraithbox-swift` | `swift:lint`, `swift:test` |
| `area:doc` | `packages/wraithbox-doc` | `doc:check`, `doc:build` |
| `area:spec` | `docs/spec/` | read-through against the requirement IDs |
| `area:ci` | `.github/`, `.mise.toml`, lockfiles, dependabot | `gha:lint`, `audit`, `vuln` |
| `area:agents` | `AGENTS.md`, `.claude/`, `docs/agents/` | read-through; restart to test |

The area is mechanical: it follows the paths the change touches, and it
tells the builder which toolchain and the reviewer which checks matter.
`mise run ci` is the gate before every push regardless.

An issue that touches two packages gets both area labels. Prefer
splitting it, so each half has one area, unless the halves must land
together (a `.proto` change and both its Go and Swift sides, for
example).

### Component: which process it belongs to

| Label | Process or interface | Main specs |
|---|---|---|
| `comp:wb` | `wb` CLI, dispatcher, TTY relay | S05-cli, S16-terminal-stream |
| `comp:wb-hostd` | sessions, git gateway, policy store, approvals, audit | S06-vm-lifecycle, S08-workspace-and-git, S09-policy-credentials-audit |
| `comp:wb-vmd` | VM provider (Swift on macOS, Go elsewhere) | S06-vm-lifecycle, S12-platforms |
| `comp:wb-netd` | userspace network stack, DHCP, DNS | S07-egress-gateway |
| `comp:wb-proxyd` | HTTP policy, credential replacement, dependency gate | S07-egress-gateway, S09-policy-credentials-audit |
| `comp:wb-guestd` | in-guest agent: users, PTY exec, git transport | S06-vm-lifecycle, S08-workspace-and-git |
| `comp:platform` | `internal/platform` interfaces and per-OS code | S12-platforms |

Component labels make it possible to see one process's backlog across
packages (`wb-vmd` is Swift on macOS and Go elsewhere) and to keep two
agents from rewriting the same process in one wave. The labels for
`comp:wb-netd`, `comp:wb-proxyd`, `comp:wb-guestd` and the host-guest
socket handlers in `comp:wb-hostd` also mark security-sensitive code: a
pull request with one of them gets a security review
(`orchestration.md`).

Shared Go code that belongs to no single process (`internal/version`,
logging, the `.proto` contracts) has an area label and no component.

No label for the conformance suite, benchmarks, or guest images yet. Add
one to the YAML file when the first issue needs it, and keep the families
small: a label nobody filters on is noise.

## Spikes

Spikes are the open questions `X01-model-credential` to `X24-openshell-artifacts` in X00-index. They are
first-class issues, and the first milestone is mostly spikes.

- **One issue per spike**, title `X<NN>-<slug>: <name>` as X00-index defines them, for example
  `X03-network-path: Network path`. Labels: `spike`, the area where the
  throwaway code will live, and the component it informs. The body quotes
  the question from X00-index and states the yes/no answer it needs, what
  will be measured, and which requirement IDs depend on it.
- **IDs are stable.** A new open question is added to X00-index first,
  with the next free `X` number, then filed. A spike is never renumbered.
- **Dependent work is blocked on the spike.** Any issue that builds on
  a spike's answer is `blocked by` the spike issue (GitHub relationship,
  see "Dependencies"). `AGENTS.md` forbids building on an unanswered
  spike; the relationship is how a picker sees that.
- **Branch and code.** A spike runs on branch `spike/x<NN>-<slug>` (for
  example `spike/x3-network-path`), with its throwaway code under
  `spikes/x<NN>-<slug>/` on that branch. That code is not held to the
  project gates, is never merged to `main`, and the branch is kept as
  the record.
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
  into the next one or none (`needs-triage`).
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
- Labels and a milestone as above.

An issue that lacks a decision only the maintainer can make is
`ready-for-human`, with the problem, the options with their cost, and one
recommendation, so the maintainer can decide from the issue alone. When
the decision touches a `SEC*` control or more than one spec, add a
review brief as well (`review-briefs.md`, the `brief` skill).

An issue that holds a list (ideas, deferred items) is not a task. Triage
splits it: each kept entry becomes its own sub-issue
(`gh issue create --parent <list>`), each dropped entry is named in the
closing comment with the reason, and the list issue closes.

## Follow-ups

Most review findings are small. These rules keep the backlog to work
that needs its own issue.

- **Reviewers rate each finding** `must-fix`, `fold` or `drop`. Only a
  `must-fix` finding makes the verdict `needs changes`. A `fold` finding
  goes to the builder to fix in the same pull request when it is cheap,
  or into the component's next issue. A `drop` finding is reported and
  left.
- **A follow-up gets its own issue only when** it changes a contract
  (a `.proto` file or an `internal/platform` interface), belongs to
  another component, moves where a `SEC*` control is enforced, needs a
  VM or the maintainer's Mac, needs a maintainer decision, or is more
  than about 300 lines. Otherwise it is fixed in the pull request,
  decided in code with a test, or named in the pull request body as part
  of the component's next issue.
- **Small spec edits are made, not filed.** When a spike result or a
  review implies a few lines of spec change and no decision, edit the
  spec in the same pull request or the next one.
- **Values belong to code.** A spec states the property and names the
  rule. Defaults, buffer sizes, retry delays, step lists and message wording
  are in code, with a comment that names the spec section. The spec
  changes only when the property changes.
- **A spec pull request gets one review round**, unless it touches a
  `SEC*` control or a contract. A second round only for `must-fix`
  findings.
- **Test case pages (`C*`) are an index.** A case is done when a test
  with its ID exists. Detail goes in the test, with a one-line entry on
  the page.
- **A follow-up issue is filed `needs-triage` with a parent**, the issue
  whose work named it. A run's report counts them.

## Decisions

- **One-way decisions go to the maintainer.** A `SEC*` control, the
  scope, behavior the user sees, or anything hard to undo gets a
  `ready-for-human` issue, and a brief when it touches a `SEC*` control
  or more than one spec (`review-briefs.md`).
- **Two-way decisions are the builder's call.** Internal design, values
  and naming. The builder makes the call and lists it under "Choices
  made" in the pull request body, one line each, so the maintainer can
  skim and veto. Merging the pull request accepts them.
- **When a security trade-off is unclear, choose the strict option.**
  Name the likely breakage and its later fix, but don't design the fix
  until the breakage shows up.
- **Questions wait for the end** of a wave or a run, and go to the
  maintainer as one batch, up to four per question dialog.
- **The maintainer posts `Decision (YYYY-MM-DD):` comments.** An agent
  drafts the text and gives the maintainer the command
  (`gh issue comment <n> --body-file <file>`). The auto-mode classifier
  refuses an agent that posts a decision for the maintainer.

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
  gets `main` as its base before it merges (`orchestration.md`,
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
  `Decision` comment is the maintainer's to post ("Decisions").
- Fix stale paths in an old body in the triage comment rather than
  leaving them for the builder.
- Set `needs-vm` and `needs-host` on every `ready-for-agent` issue that
  needs them (`host-work.md`, "The labels"). A run merges issues without
  either label under the maintainer's standing approval, so leave a
  label off only when the work clearly needs neither.

Every comment, issue body and pull request body an agent writes ends with
the attribution lines from `AGENTS.md`.
