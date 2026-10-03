---
title: Slide decks with Quarto
description: Author slide decks in Quarto and render them to HTML and PDF.
---

Slide decks are authored in [Quarto](https://quarto.org/) (`.qmd`) and rendered
to two static outputs that live in `packages/wraithbox-doc/public/presentations/`:

- **HTML** - a self-contained [reveal.js](https://revealjs.com/) deck
  (`example.html`), themed to match the site.
- **PDF** - a printable version (`example.pdf`) via Quarto's Beamer output.

There is a worked example at
[`packages/wraithbox-doc/public/presentations/example.qmd`](/presentations/example.qmd). Open the
rendered [HTML slides](/presentations/example.html) or the
[PDF](/presentations/example.pdf).

## Render

Quarto is pinned in `.mise.toml`. Render the example (both formats) with:

```bash
mise run doc:slides
```

That runs `quarto render packages/wraithbox-doc/public/presentations/example.qmd`, which produces
`example.html` and `example.pdf` beside the source. The PDF output uses LaTeX
(Beamer); if it is missing, install it once with `quarto install tinytex`.

The rendered outputs are committed to git, because decks change rarely and this
keeps the deployed site a pure static build (CI does not run Quarto). Re-run
`mise run doc:slides` and commit the results whenever you edit a deck.

## Theme

The reveal.js theme lives in `packages/wraithbox-doc/public/presentations/reveal.scss` and maps
the deck's fonts and accent colour onto the site's LSD Warm palette. Adjust the
SCSS variables there to restyle the HTML slides.

## Add a deck

1. Copy `example.qmd` to a new name in `packages/wraithbox-doc/public/presentations/`.
2. Edit the frontmatter `title`/`author` and write your slides (`#` starts a
   section, `##` starts a slide).
3. Render with `quarto render packages/wraithbox-doc/public/presentations/your-deck.qmd`.
4. Link it from the sidebar in `astro.config.mjs` (see the "Example slides"
   group), and add a redirect for the extensionless URL if you want a clean
   sidebar link.
