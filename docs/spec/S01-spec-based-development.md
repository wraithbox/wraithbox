# S01 - Spec-Based Development

**Purpose:** How the design is written down: the kinds of design
documents, their IDs, the spec template, and how packages are named.

Specs are written before the code, and they are authoritative: when
code and spec disagree, one of them is fixed in the same change
(`AGENTS.md`). A spec explains why each decision was made, and leaves
out how to implement it. Aim for under 100 lines.

## Documents and IDs

| ID | Kind | Where | Index |
|---|---|---|---|
| `S<NN>-<slug>` | Spec | `docs/spec/` | S00-index |
| `FR<NN>-<slug>` | Functional requirement | `docs/requirements/` | FR00-index |
| `NFR<NN>-<slug>` | Non-functional requirement | `docs/requirements/` | NFR00-index |
| `SEC<NN>-<slug>` | Security requirement | `docs/requirements/` | SEC00-index |
| `T<NN>-<slug>` | Threat | `docs/threats/` | T00-index |
| `X<NN>-<slug>` | Spike | `docs/spikes/` | X00-index |
| `R<NN>-<slug>` | Research note | `docs/research/` | R00-index |
| `V<N>-<slug>` | Version | `docs/versions/` | V00-index |
| `V<N>-<NN>-<slug>` | Decision scoping version N | its version page | |
| `V<N>-M<N>-<slug>` | Milestone of version N | its version page | |
| `I<NN>` | GitHub issue | GitHub | |
| `PR<NN>` | GitHub pull request | GitHub | |

- **Numbers** have two digits, `01` to `99`, and grow a third past
  that (`S100`). Versions and milestones are few, so they are not
  padded: `V1`, `V1-M2`.
- **`00` is the index** of its kind: `S00-index`, `T00-index`. It lists
  every item of the kind, and the docs site build fails when one is
  missing.
- **Small items are definitions** in their index, one list item each:
  `- **SEC06-repo-writes: Repository-scoped writes.** …`. An item that
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

## Spec template

The **For review** block is what the maintainer reads first: it says
what approving the spec commits to, without needing the rest of the
spec or the requirement indexes open (`docs/agents/review-briefs.md`).

```markdown
# S<NN> - Feature Name

**Purpose:** One-line description of what this does and why

**Requirements:** The FR, NFR and SEC IDs this spec covers

## For review

- **Decides:** what this spec settles, in a sentence or two
- **You are approving:** the commitments, in plain words
- **Controls touched:** each SEC ID with its meaning, kept or how it
  changes; or "none"
- **Assumed:** what was assumed rather than checked or asked
- **Open decisions:** numbered, each with a recommendation; or "none"
- **Brief:** `/review/<slug>/` when there is one

## Design

The sections the topic needs. Each decision says what was chosen, why,
and which options were rejected and why. A diagram for a flow between
processes or a trust boundary, as an SVG next to the spec
(`docs/agents/review-briefs.md`, "Diagrams").

## Open questions

1. A question, the current leaning, and what settles it (a spike
   `X<NN>` or a maintainer decision).

## Out of scope

- What a reader could reasonably assume is included, but isn't.

**Status:** [Draft/Approved/Implemented]
```

Status moves from Draft to Approved when the maintainer merges it, and
to Implemented when the code matches it.

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
