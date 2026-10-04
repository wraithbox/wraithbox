# Review briefs

A review brief is one page on the docs site that puts one thing in front
of the maintainer to approve: a decision (a `ready-for-human` issue), a
spike result, or a spec change. The maintainer decides the security model
and the scope, but is not a Go, Swift, or virtualization expert. A brief
lets them decide from the page alone, in a few minutes, with the
technical detail one click away.

The `brief` skill writes briefs and the `spec-draft` skill calls it.
Builders write one for a spike result (`planning.md`, "Spikes").

## What a brief is not

A brief explains, it does not decide. The record stays where it is:

- a decision in the issue comment that starts `Decision (YYYY-MM-DD):`,
  and then in the spec it changes;
- a spike result in `docs/spec/spikes/X<n>-<slug>.md`;
- a spec change in `docs/spec/`.

A brief doesn't add requirements or rules. If a brief and its source
disagree, the source is right, and the brief gets fixed in the same pull
request.

## Where briefs go

- Page: `packages/wraithbox-doc/src/content/docs/review/<slug>.mdx`,
  served at `/review/<slug>/`.
- Slug: `issue-<n>-<slug>` for a decision, `x<n>-<slug>` for a spike,
  `spec-<nnn>-<slug>` for a spec change.
- Add a row to the table in `review/index.md`, newest first.
- Link the brief from its source: a comment on the issue, a line under
  the title of the spike result or spec, and the pull request body.

The brief goes in the same pull request as its source when there is
one. A brief for an open decision issue gets a pull request of its own, with
`Refs #<n>`, and is updated when the decision is made.

## Layout

Top to bottom. Skip a section that has nothing to say, but keep the
order.

1. **Frontmatter.** `title` names the question, not the component
   ("Can each project get its own egress policy in a shared VM?").
   `description` is the one-sentence answer or recommendation.
2. **Status line.** `Awaiting decision`, `Decided YYYY-MM-DD: <option>`,
   `Answered YYYY-MM-DD` (spike), or `Superseded by <link>`. Then links
   to the source: issue, spec section, spike result, pull request.
3. **The ask.** A `<Aside type="tip" title="You are deciding">` with
   one sentence on what is being decided, the recommendation, and what
   happens next if the maintainer accepts it (which issues unblock,
   which spec changes). For a spike, title it `What we learned` and give
   the answer (yes, no, or yes with conditions) first.
4. **The picture.** One diagram that shows the problem: who talks to
   whom, and where the trust boundary is. Add a before/after pair when
   the decision changes the structure. See "Diagrams" below.
5. **Options.** At most four, as `wb-option` cards in a `wb-options`
   grid, the recommended one first with `data-recommended`. Each card
   says in plain words what the option does, what it costs (effort,
   speed, user friction), and what it weakens. Name any `S*` control it
   touches with a `<Badge>`: `variant="success"` when kept,
   `variant="caution"` when kept with a condition, `variant="danger"`
   when weakened. AGENTS.md forbids weakening an `S*` control, so an
   option that does needs a spec change, and the card says so.
6. **Impact.** A `wb-impact` table: one row per requirement or control
   affected, columns per option. Restate each ID's meaning in the row
   ("S10: egress is default-deny per project"), so the reader does not
   need spec 003 open. Cells are `yes`, `partial`, `no` or `na`, and
   each holds a word or two, never only a color.
7. **Evidence.** The claims the recommendation rests on, each with its
   source (a `file:line`, a spec section, a primary doc URL, a probe or
   spike measurement) and a badge: `verified` (checked by running or
   reading it, `variant="success"`), `inferred` (follows from verified
   facts, `variant="note"`), or `assumed` (not checked,
   `variant="caution"`). An `assumed` claim that the recommendation
   depends on is a reason for a spike, and the brief says which.
8. **What it unblocks.** Issues, spikes, and milestones that wait on
   this, as links.
9. **Details.** A `<details>` block with the deeper explanation and a
   short glossary of the terms a non-expert needs (vsock, ClientHello,
   SNI, …). A reader who stops before this block must still be able to
   decide.

The ask and the picture fit on the first screen at 1280×1000, with the
options right after them.

## Writing

- Plain words first. Define a technical term in a phrase on first use,
  and put the longer explanation in the glossary.
- Give a requirement ID's meaning in a few words wherever it appears
  outside the impact table.
- No claim without a source. "Probably" gets the `inferred` or
  `assumed` badge instead.
- The house prose rules apply (Vale and cspell run on `.mdx`).

## Diagrams

Draw inline SVG in the MDX, in a `<figure class="wb-figure">` with a
`<figcaption>`. Shapes take classes from `src/styles/brief.css`, never
colors, so one drawing works in both themes:

| Class | Use |
|---|---|
| `box host` / `box guest` | a process on the host / in the guest |
| `boundary` + `boundary-label` | a trust boundary (dashed) and its name |
| `edge` + marker path `arrow` | a flow |
| `edge-bad` + marker path `arrow-bad` | a flow the design must stop |
| `label` / `note` | text / secondary text |

Rules:

- Give the `<svg>` a `viewBox` and no fixed size, plus `role="img"` and
  an `aria-label`.
- Marker `id`s are page-global, so prefix them with the figure number
  (`f1-arrow`).
- MDX is JSX: comments are `{/* … */}`, not `<!-- … -->`.
- At most about a dozen boxes. A diagram that needs a paragraph to be
  understood gets redrawn, not explained.
- Use the process names from spec 004, and draw the guest as untrusted.

When the site gains a LikeC4 model or Mermaid support, a brief may embed
a view from it instead. Until then, inline SVG is the one way.

## Check

Before opening the pull request:

```bash
mise run doc:check && mise run doc:build
mise run prose:lint && mise run prose:spell
```

Then look at the page in both themes (`mise run doc:dev`), or take
screenshots with Playwright, and check that the ask, the picture, and the
options read correctly without the details block.
