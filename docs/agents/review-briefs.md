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
- a spike result in `docs/spikes/X<NN>-<slug>.md`;
- a spec change in `docs/spec/`.

A brief doesn't add requirements or rules. If a brief and its source
disagree, the source is right, and the brief gets fixed in the same pull
request.

## Where briefs go

- Page: `docs/briefs/B<NN>-<slug>.mdx`, served at
  `/briefs/b<NN>-<slug>/`. `<NN>` is the number of the GitHub issue the
  brief belongs to: B36-flow-attribution is the brief for I36. A
  spike's brief takes the number of the spike's issue, and a spec
  change or pull request the number of the issue it closes. File an
  issue first when there is none.
- **One brief per issue.** When an issue needs two briefs, split the
  issue first. The build fails on two briefs with the same number, and
  on a brief numbered after a pull request.
- Run `mise run doc:index` to add its row to B00-index.
- Link the brief from its source: a `Brief:` line at the end of the
  issue body, above the attribution lines, with the brief's URL
  (`Brief: https://wraithbox.nl/briefs/b36-flow-attribution/`), a line
  under the title of the spike result or spec, and the pull request
  body. The build warns when an issue doesn't link its brief (after
  `mise run doc:refs`).

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
   `Answered YYYY-MM-DD` (spike), or `Superseded by <link>`. Then the
   source as references (below): `I36`, `S07-egress-gateway`, `PR13`.
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
   speed, user friction), and what it weakens. Name any `SEC*` control it
   touches with a `<Badge>`: `variant="success"` when kept,
   `variant="caution"` when kept with a condition, `variant="danger"`
   when weakened. AGENTS.md forbids weakening a `SEC*` control, so an
   option that does needs a spec change, and the card says so.
6. **Impact.** A `wb-impact` table: one row per requirement or control
   affected, columns per option. Write the ID with its slug and a few
   words of meaning ("SEC05-default-deny: only allowlisted hostnames are
   reachable"), so the reader does not need SEC00-index open. Cells are `yes`, `partial`, `no` or `na`, and
   each holds a word or two, never only a color.
7. **Evidence.** The claims the recommendation rests on, each with its
   source (a `file:line`, a spec section, a primary doc URL, a probe or
   spike measurement) and a badge: `verified` (checked by running or
   reading it, `variant="success"`), `inferred` (follows from verified
   facts, `variant="note"`), or `assumed` (not checked,
   `variant="caution"`). An `assumed` claim that the recommendation
   depends on is a reason for a spike, and the brief says which.
8. **What it unblocks.** Issues, spikes, and milestones that wait on
   this, as references.
9. **Details.** A `<details>` block with the deeper explanation and a
   short glossary of the terms a non-expert needs (vsock, ClientHello,
   SNI, …). A reader who stops before this block must still be able to
   decide.

The ask and the picture fit on the first screen at 1280×1000, with the
options right after them.

## Writing

- Plain words first. Define a technical term in a phrase on first use,
  and put the longer explanation in the glossary.
- Write requirement IDs with their slug (`SEC06-repo-writes`). The hover
  card gives the full text, so a few words of meaning are enough where
  the point depends on it.
- No claim without a source. "Probably" gets the `inferred` or
  `assumed` badge instead.
- The house prose rules apply (Vale and cspell run on `.mdx`).

## References

Write references as bare IDs (S01-spec-based-development, "Documents
and IDs"). The docs site turns them into links with a hover card that
shows the title and a one-sentence description
(`src/plugins/remark-refs.mjs`):

| Write | Links to |
|---|---|
| `SEC06-repo-writes`, `FR01-drop-in`, `NFR01-startup`, `T05-cross-proj-clones` | the definition in its index |
| `X14-flow-attribution` | the spike in X00-index, or its result page |
| `S07-egress-gateway`, `V1-initial`, `X00-index` | the page |
| `V1-01-macos-first`, `V1-M2-walking-skeleton` | the decision or milestone in V1-initial |
| `I36`, `PR13` | the issue or pull request on GitHub |

- Write the ID with its slug. The ID alone (`SEC06`) works, and is shown
  in full. A version (`V1`) stays as written.
- Nothing goes in front: `S07-egress-gateway`, not "spec
  S07-egress-gateway", and `I36`, not "issue #36". GitHub bodies and
  commit messages are the exception: there, write `#36`.
- These fail the build: a wrong slug, an ID without its leading zero
  (`S6`, `X3`), and an ID of the old scheme (`F1-drop-in`,
  `007-egress-gateway`). An unknown ID, "spec 007" and `#36` give a
  warning.
- Don't link specs on GitHub. Design pages are on the site under
  `/spec/`, `/requirements/`, `/threats/`, `/spikes/`, `/research/` and
  `/versions/`.
- Issue and pull request titles come from a snapshot. After citing a
  new one, run `mise run doc:refs` and commit
  `src/data/github-refs.json`.
- References inside code spans and headings stay plain text. A
  `<Badge>` whose text starts with a reference becomes a link with the
  hover card.

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
- Text in the SVG is not linked or checked, so an ID there stays as
  written. Keep it short (`X06`), and reference it with its slug in the
  caption, which is checked.
- At most about a dozen boxes. A diagram that needs a paragraph to be
  understood gets redrawn, not explained.
- Use the process names from S04-architecture, and draw the guest as untrusted.

A spec's diagram is an SVG file next to it (`docs/spec/S<NN>-<slug>.svg`),
linked as an image: `![caption](S<NN>-<slug>.svg)`. It uses the same
classes, plus its own `<style>` block with colors for both light and
dark, so GitHub shows it too. The site copies the file into the spec
page, drops that `<style>`, and turns the image's alt text into the
caption. `docs/spec/S04-architecture.svg` is the example to copy.

## Check

Before opening the pull request:

```bash
mise run doc:check && mise run doc:build
mise run prose:lint && mise run prose:spell
```

Then look at the page in both themes (`mise run doc:dev`), or take
screenshots with Playwright, and check that the ask, the picture, and the
options read correctly without the details block.
