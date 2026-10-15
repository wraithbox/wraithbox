# S01 - Spec-Based Development

**Purpose:** How the design is written down: the kinds of design
documents, their IDs, the spec template, how a spec is split into
pages, and how packages are named.

Specs are written before the code, and they are authoritative: when
code and spec disagree, one of them is fixed in the same change
(`AGENTS.md`). A spec explains why each decision was made, and leaves
out how to implement it.

## Documents and IDs

| ID | Kind | Where | Index |
|---|---|---|---|
| `S<NN>-<slug>` | Spec | `docs/spec/` | S00-index |
| `S<NN>-<MM>-<slug>` | Page of a spec; `00` is its overview | `docs/spec/S<NN>-<slug>/` | S00-index |
| `C<NN>-<slug>` | Test cases of one area | `docs/tests/` | C00-index |
| `FR<NN>-<slug>` | Functional requirement | `docs/requirements/` | FR00-index |
| `NFR<NN>-<slug>` | Non-functional requirement | `docs/requirements/` | NFR00-index |
| `SEC<NN>-<slug>` | Security requirement | `docs/requirements/` | SEC00-index |
| `T<NN>-<slug>` | Threat | `docs/threats/` | T00-index |
| `X<NN>-<slug>` | Spike | `docs/spikes/` | X00-index |
| `R<NN>-<slug>` | Research note | `docs/research/` | R00-index |
| `B<NN>-<slug>` | Review brief, numbered after its issue | `docs/briefs/` | B00-index |
| `V<N>-<slug>` | Version | `docs/versions/` | V00-index |
| `V<N>-<NN>-<slug>` | Decision scoping version N | its version page | |
| `V<N>-M<N>-<slug>` | Milestone of version N | its version page | |
| `I<NN>` | GitHub issue | GitHub | |
| `PR<NN>` | GitHub pull request | GitHub | |

- **Numbers** have two digits, `01` to `99`, and grow a third past
  that (`S100`). Versions and milestones are few, so they are not
  padded: `V1`, `V1-M2`.
- **`00` is the index** of its kind: `S00-index`, `T00-index`. Its
  table (`| ID | Description | Status |`) lists every item of the kind,
  and is generated: `mise run doc:index` rewrites it, and the docs site
  build fails when it is out of date. The description is a page's title
  or a definition's name. The status is a page's `**Status:**` line, a
  brief's status line, a spike's issue state (or "Answered" once its
  result page exists), the milestones whose "Covers" names a
  requirement, or "Accepted" for a threat. An index has no status of
  its own.
- **Small items are definitions** in their index, one list item each,
  with a name: `- **SEC06-repo-writes: Repository-scoped writes.** …`. An item that
  outgrows a paragraph, such as a spike result, gets its own file next
  to the index (`docs/spikes/X03-network-path.md`). The definition stays
  in the index as its summary.
- **Numbers and slugs are fixed** once merged. A withdrawn item keeps
  its number and says it is withdrawn.
- **In text**, write the ID with its slug and nothing in front:
  S07-egress-gateway, not `spec 007`. The ID alone (`SEC06`) is
  accepted, and the docs site shows it in full, except a version (`V1`),
  which reads as a name. Issues and pull
  requests are I36 and PR13. On GitHub itself (issue and pull request
  bodies, commit messages) write `#36`, which is the only form GitHub
  links.

## Spec tree

A spec that grows past one page of the size limits below becomes a
tree. The spec moves into a directory named after it, as the overview,
page `00`. Its pages go next to it:

```text
docs/spec/S07-egress-gateway/
  S07-00-egress-gateway.md                   the overview
  S07-00-egress-gateway.svg                  its diagram
  S07-01-per-vm-policy.md                    a page
  S07-02-packet-path.md
  S07-02-packet-path.svg                     a page's diagram
```

- **The overview** is the spec, and keeps its ID: text refers to it as
  S07-egress-gateway, not `S07-00`. Number `00` sorts it first in a
  file browser, and its old path on the docs site redirects to it.
- **Its content** is the spec's purpose, requirements, tests and briefs,
  one diagram, and a table of its pages with one line each. It also
  holds what applies to every page. It aims for under 100 lines.
- **A page** covers one topic that a builder builds or a reviewer checks
  as a whole. It follows the spec template, with its own `**Purpose:**`
  and `**Status:**` lines.
- **Page numbers** start at `01` in reading order, and are fixed once
  merged, as other IDs are. A new page takes the next free number.
- **On the site**, a spec with pages is a group in the sidebar, and
  S00-index lists each page after its spec.

## Size and structure

Specs are read by people, and a page has to be short enough to review
in one sitting. These limits are checked by `src/plugins/structure.mjs` in
the docs site:

| What | Limit |
|---|---|
| A page | 300 lines |
| A section, from one heading to the next | 80 lines |
| A list | two levels deep |
| A paragraph or list item | 120 words |
| A sentence | 40 words |

- **A topic a reader might look for gets a heading**, `###` under `##
  Design`. A bold lead (`- **Disks.**`) names an item in a list, not a
  section.
- **One claim per sentence.** A sentence that adds a condition, a
  reason, and an exception becomes three sentences.
- `mise run doc:structure` lists where each spec breaks a limit. The
  docs build fails for the specs in the file's `STRICT` list. A spec
  goes on it once it meets the limits, and doesn't come off.

## References to a section

- **In another document:** the ID, then the section's name in quotes:
  `S07-egress-gateway, "Packet path"`. Once that section is a page of
  its own, write the page's ID instead: `S07-02-packet-path`.
- **In the same document:** the name in quotes, in parentheses or
  followed by "below" or "above": `("Disks")`, `"Disks" below`.
- **A section** is a heading, or the bold or italic lead of a list item.
  The name can be cut at a comma, colon or parenthesis: "Modes" names
  "Modes, per host".
- The docs build fails on a reference to a section that its document
  doesn't have (`src/plugins/sections.mjs`). So a renamed or moved section
  takes its references with it in the same change.

## Diagrams

A spec or page has at most one diagram, at the top, as an SVG file next
to it (`docs/agents/review-briefs.md`, "Diagrams"). It shows the design
as decided: the processes, the trust boundaries and the flows that
cross them. Red flows are allowed only for what a control stops. A
brief's figure of the chosen option is a good start. Remove the option
letters, the figure number, and the state before the decision. Then
check the drawing against the spec text. Where they differ, the text
is right.

## Spec template

A spec states the design, as something a builder can implement and,
later, as what is implemented. Review material goes in the review brief
and the issue, never in the spec: what approving commits to, the
controls touched, assumptions, and open decisions with their
recommendations (`docs/agents/review-briefs.md`). Where a design
relies on a property still to be measured, the spec states the design
and the property, and the issue tracks the measurement.

```markdown
# S<NN> - Feature Name

**Purpose:** One-line description of what this does and why

**Requirements:** The FR, NFR and SEC IDs this spec covers

**Tests:** The C pages with its test cases (S11-verification-and-spikes)

Brief: B<NN>-<slug>, when there is one

![What the diagram shows](S<NN>-<slug>.svg)

## Design

What the design is, in the present tense, as a builder needs it.

### A topic

One `###` heading for each topic a reader might look for.

## Decisions

### A decision

What was chosen, in one sentence.

- **Why:** the reasons.
- **Rejected:** each other option, and why.
- **Decided:** the brief or issue, with the date.

## Out of scope

- What a reader could reasonably assume is included, but isn't.

**Status:** [Draft/Approved/Implemented]
```

Status moves from Draft to Approved when the maintainer merges it, and
to Implemented when the code matches it.

The design and its reasons are kept apart. The design then reads as one
description, without the history of how it was chosen. "The maintainer
decided on I68 that…" and "X was rejected, because…" go under
Decisions. Nothing that was decided is dropped. A decision whose reason
is one sentence can stay in the design.

## Package naming

Packages under `packages/` are suffixed by language so each toolchain can
discover what it owns:

- `packages/<name>-go/`: Go (module listed in `go.work`)
- `packages/<name>-swift/`: Swift (standalone SwiftPM package; macOS only)
- `packages/<name>-dotnet/`: C# on .NET (Windows only; reserved, not yet
  present; S12-platforms)
- `packages/<name>-doc/`: Docs site (Astro Starlight; bun; standalone)

When one feature spans languages, pick one `<name>` and let the suffix
distinguish the implementation.

**Status:** Implemented
