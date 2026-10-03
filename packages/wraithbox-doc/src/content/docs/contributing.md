---
title: Contributing
description: How the site is built and how to contribute.
---

This site is built with [Astro Starlight](https://starlight.astro.build/) and
published to GitHub Pages at [wraithbox.nl](https://wraithbox.nl). Contributions are welcome - see
[CONTRIBUTING.md](https://github.com/wraithbox/wraithbox/blob/main/CONTRIBUTING.md)
in the repository root.

## The site

Tools are pinned in `.mise.toml`; run `mise install` once. Then:

- `mise run doc:install` - install the site dependencies (bun).
- `mise run doc:dev` - start the live-reloading dev server.
- `mise run doc:build` - build the static site into `packages/wraithbox-doc/dist`.
- `mise run doc:check` - run the Astro type/content check.

Content lives in `packages/wraithbox-doc/src/content/docs/`; static assets in `packages/wraithbox-doc/public/`.

## Conventions

Commit messages follow [Conventional Commits](https://conventionalcommits.org/);
use the language or component as the scope when relevant (`docs(doc): ...`).
The full CI gate runs with `mise run ci`, and `mise run doc:check` plus
`mise run doc:build` cover the docs site. Design changes start in the
specifications under `docs/spec/`. See `AGENTS.md` at the repo root for the
guidelines.
