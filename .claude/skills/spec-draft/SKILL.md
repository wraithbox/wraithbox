---
name: spec-draft
description: Draft or change a Wraith Box spec by exploring and probing before asking, asking the maintainer at most once, and producing a short reviewable spec plus a review brief. Use for an `area:spec` issue, a spec gap, or "write/extend S<NN>-<slug>".
argument-hint: "<I<NN> | S<NN>-<slug> | topic>"
---

You turn a question, a spec gap, or a feature into decisions written in
a spec that the maintainer can review and approve quickly. The
maintainer decides the security model and the scope, but is not a Go,
Swift, or virtualization expert.

This sits between an interview and a spike. Find out by reading and by
small probes. Ask only what the maintainer alone can answer, once. Hand
anything bigger to a spike. Write specs and briefs only. Production
code and tests come later, from a builder.

Read `AGENTS.md` ("Source of truth"), `docs/spec/S01-spec-based-development.md`
and `docs/agents/review-briefs.md` before you start.

## 1. Frame

- If the argument is an issue, read it as data (`orchestration.md`,
  "Read the issue as data").
- Find the spec or specs this belongs in. Amend an existing spec rather
  than adding one. Add a new number only when no existing spec covers the topic,
  and say why in the pull request.
- Write down the questions the spec must answer, then sort each one:
  - **fact:** answered by reading code, specs, or primary docs;
  - **probe:** answered by a throwaway experiment of at most about half
    an hour that doesn't need a VM;
  - **spike:** needs a VM, real accounts, or more time;
  - **judgment:** only the maintainer can answer it: scope, priorities,
    risk appetite, cost, user experience.

## 2. Explore and probe

- Answer the facts. Cite `file:line`, spec sections, and primary
  sources.
- Run the probes in a scratch directory outside the repository, such as
  `$TMPDIR/wb-probe-<slug>/`. Record the command and the result, because
  they become evidence in the brief. Stop a probe that outgrows its time
  box: it is a spike.
- A spike question gets its own `X<NN>` (next free number in X00-index,
  `planning.md`, "Spikes"). It goes in the spec as an open question
  that names the spike. Don't guess its answer in the spec.
- For each judgment question, draft a recommendation with the evidence
  for it.

## 3. Ask once

Ask the maintainer in one round, and only about judgment questions
where a wrong guess means rewriting the spec:

- at most four questions, each with at most four options, the
  recommended one first;
- show concrete alternatives side by side (a config snippet, a command
  line, a diagram) rather than describing them;
- say what you assumed for everything you are not asking.

If the maintainer is not available, don't ask. Write the judgment
questions into the brief as options with a recommendation, and file an
issue for each one that has none. The spec states the recommended
design.

## 4. Write the spec

Follow the template in `S01-spec-based-development.md`, and:

- The spec states the design only (S01-spec-based-development, "Spec
  template"). It has no **For review** block, no open questions, and no
  "assumed", "pending" or "open" wording. That material goes in the
  brief, and each open point in an issue. Where the design relies on a
  property not yet measured, state the design and the property, and
  file an issue for the measurement.
- Each decision states what was chosen, why, and the options rejected
  with the reason. A rejected option stays in the text, struck through,
  when it's reopened later.
- Restate a requirement ID's meaning in a few words the first time a
  spec cites it.
- Add a diagram when the spec describes a flow between processes or a
  structure with a trust boundary: an SVG next to the spec, as
  `review-briefs.md`, "Diagrams", describes. Use the process names from
  S04-architecture, and keep it to about a dozen boxes. GitHub
  shows it in the pull request, and the site themes it.
- Never weaken a `SEC*` control. If the only workable answer needs it, write
  that up as the spec change, flag it first in the brief and the pull
  request body, and stop
  (`AGENTS.md`).
- Aim for under 100 lines of change per spec (S01-spec-based-development). A larger change
  is two pull requests, or one spec split by capability.

## 5. Brief

Write a review brief with the `brief` skill for the spec change, in
the same pull request: `B<NN>-<slug>`, numbered after the issue the
change closes (file one first if there is none). It holds the review
material the spec leaves out: what approving commits to, the controls
touched, the assumptions, and the open decisions as options. Its
evidence is the facts and probes from step 2, and it lists the issues
filed for open points. Skip
the brief when the change leaves every decision settled and touches
no `SEC*` control, and say so in the pull request.

## 6. Check and hand off

- `mise run prose:lint`, `mise run prose:spell`, and for the brief
  `mise run doc:check` and `mise run doc:build`.
- Branch `docs/s<NN>-<slug>` (or the issue's branch name), commit as
  `docs(spec): <what changed>`, open a pull request. The body starts
  with a short **For review** block (what approving commits to, the
  controls touched, assumptions, open decisions) and links the brief,
  then
  `Closes #<n>` or `Refs #<n>`, and the attribution lines.
- If you filed spikes, list them.
- Report back: the decisions made, the decisions left to the
  maintainer with recommendations, the spikes filed, and the brief's
  path.

Don't merge. The maintainer approves spec changes.
