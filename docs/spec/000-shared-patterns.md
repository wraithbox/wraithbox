# 000 - Shared Patterns Reference

This document contains templates and boilerplate code that specs can reference to avoid repetition.

## Spec Template

Standard template for new specification documents. The **For review**
block is what the maintainer reads first: it says what approving the
spec commits to, without needing the rest of the spec or spec 003-requirements open
(`docs/agents/review-briefs.md`).

```markdown
# XXX - Feature Name

**Purpose:** One-line description of what this does and why

**Requirements:** The spec 003-requirements IDs this spec covers

## For review

- **Decides:** what this spec settles, in a sentence or two
- **You are approving:** the commitments, in plain words
- **Controls touched:** each `S*` ID with its meaning, kept or how it
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

1. A question, the current leaning, and what settles it (a spike `X<n>`
   or a maintainer decision).

## Out of scope

- What a reader could reasonably assume is included, but isn't.

**Status:** [Draft/Approved/Implemented]
```

## Naming Convention

Packages under `packages/` are suffixed by language so each toolchain can
discover what it owns:

- `packages/<name>-go/`: Go (module listed in `go.work`)
- `packages/<name>-swift/`: Swift (standalone SwiftPM package; macOS only)
- `packages/<name>-dotnet/`: C# on .NET (Windows only; reserved, not yet
  present; spec 012-platforms)
- `packages/<name>-doc/`: Docs site (Astro Starlight; bun; standalone)

When one feature spans languages, pick one `<name>` and let the suffix
distinguish the implementation.

## Spec Numbering

- `000` to `002`: how this repository works (patterns, process, toolchain).
- `003` onward: the product. `003` holds the requirement identifiers
  (`F*`, `S*`, `N*`, `C*`, `R*`) that later specs reference.
- A spike result is written up as its own spec before dependent work
  starts (see spec 011-verification-and-spikes).
