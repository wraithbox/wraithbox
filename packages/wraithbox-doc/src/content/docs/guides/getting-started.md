---
title: Getting started
description: Set up a development environment for Wraith Box.
---

Wraith Box is in its design phase, so "getting started" currently means
getting set up to work on it.

## Prerequisites

- An Apple Silicon Mac with Xcode installed. Swift, `swift-format` and
  `xcodebuild` come from Xcode.
- [mise](https://mise.jdx.dev/), which installs every other pinned tool:
  Go, linters, bun (docs site), Quarto (slide decks).

```bash
mise trust
mise install
mise run install   # docs site dependencies
```

## The gate

```bash
mise run ci        # lint + typecheck + test + build, offline
mise run vuln      # dependency vulnerability scan, needs network
```

Tasks are namespaced by language: `go:test`, `swift:lint`, `doc:build`.
`mise tasks` lists them all.

## The docs site

```bash
mise run doc:dev     # dev server at http://localhost:4321/wraithbox/
mise run doc:build   # static build into packages/wraithbox-doc/dist
mise run doc:check   # Astro type/content check
```

Content lives in `packages/wraithbox-doc/src/content/docs/`. Pushing to
`main` deploys the site to GitHub Pages via `.github/workflows/deploy.yml`.

## Next steps

- Read the [design overview](/design/), then the specifications in
  `docs/spec/`, starting with `003-requirements.md`.
- [Writing pages](/guides/writing-pages/) for adding documentation.
