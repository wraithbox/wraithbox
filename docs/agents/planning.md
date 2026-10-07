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

1. The current **milestone** (lowest-numbered open one).
2. Within it, **spikes** before anything that depends on them.
3. Then by `priority:` label, then by issue number.

## Labels

Every open work issue carries:

- exactly one **type**: `bug`, `enhancement`, `spike`, `documentation` or
  `chore`;
- one **triage status**: `needs-triage`, `needs-info`, `ready-for-agent`
  or `ready-for-human` (plus `blocked` when it waits on something, and
  `needs-vm` when it needs a running macOS guest);
- at least one **area** label (`area:*`);
- zero to two **component** labels (`comp:*`), when the work belongs to a
  process from S04-architecture;
- optionally a `priority:` label.

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
| `comp:wb` | `wb` CLI, dispatcher, TTY relay | S05-cli |
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
  the record. The user-level `spike` skill follows this convention.
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
  the **For review** block of the spec template (S01-spec-based-development), and the same
  pull request adds a review brief (`review-briefs.md`, the `brief`
  skill), so the maintainer can approve the answer without reading the
  spike code. The same pull
  request changes any spec the answer affects, or, if that is too big,
  files the spec change as an issue. It also links the result from the
spike's entry in X00-index. That pull request says
  `Closes #<spike issue>`. A "no" that makes a requirement unachievable
  is written up as a spec change and the work stops there
  (`AGENTS.md`).

## Milestones

Milestones group issues into deliverable steps toward a version. Each
milestone is defined on its version page (V1-initial for the first),
and is a GitHub milestone of the same name, not a label.

- **Name:** `V<N>-M<N>-<slug>`, numbered in order within the version,
  for example `V1-M1-spikes-closed`. The number gives the order. Agents
  work the lowest open one first.
- **Description:** the exit criterion from the version page, with the
  requirement IDs and spikes it covers. Example: `X01-model-credential
  to X09-keychain-unsigned answered and written up in docs/spikes/;
  specs updated where an answer changed the design. Covers
  SEC04-no-guest-secrets, NFR01-startup, NFR02-fs-speed.`
- **Membership:** every `ready-for-agent` issue is in exactly one
  milestone. An issue without one is not picked. Follow-ups found during
  a milestone go into it if they block its exit criterion, otherwise
  into the next one or none (`needs-triage`).
- **Closing:** the milestone closes when its exit criterion holds, not
  merely when its issues are closed. The closing agent or maintainer
  checks the criterion and comments on the last issue with the evidence.
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
- A pull request can't be a blocker target. Write `Blocked by #<pr>` on
  its own line near the top of the body instead, and add the `blocked`
  label.
- **Parent** records where an issue came from: a split, a kept entry of a
  list, or a follow-up from a review of issue N.
- A pull request links its issue with `Closes #N` in the body. To mention
  an issue without closing it, write `#N` with no closing keyword in
  front.

## Triage

Triage turns `needs-triage` issues into one of: `ready-for-agent` (with
the decisions written down), `ready-for-agent` + `blocked` (blocker set),
`ready-for-human` (the one action named), or closed with a reason.

- Record each decision in a comment that starts `Decision (YYYY-MM-DD):`
  or `Triage (YYYY-MM-DD):`, then change the labels. The comment is what
  the next agent reads.
- Agents decide routine triage themselves (labels, duplicates, splits)
  and report what they did. They ask the maintainer only when a decision
  touches a spec, a rule in `AGENTS.md`, the scope, or the cost.
- Fix stale paths in an old body in the triage comment rather than
  leaving them for the builder.

Every comment, issue body and pull request body an agent writes ends with
the attribution lines from `AGENTS.md`.
