# Image generator prompts: logo, icon, hero

Briefs to paste into an AI image generator (Midjourney, DALL-E / GPT image,
Imagen, Flux, Stable Diffusion, ...) for the Wraith Box brand artwork. This
file is outside `src/content/`, so it is not published and not checked by
the docs build.

The generated images are raw material, not final assets: pick a direction,
then redraw or vectorize it into clean SVG (see "From raster to assets" at
the end). Where the artwork goes in the site is marked in
`astro.config.mjs` (LOGO HOOK, HERO HOOK) and `scripts/gen-favicon.mjs`.

## What the product is (context for every prompt)

Wraith Box runs Claude Code, an AI coding agent, inside an isolated virtual
machine. Nothing from the host is mounted in, no secrets are in the guest,
and every network packet goes through a default-deny gateway on the host.
The image is "a ghost in a box": a capable, autonomous presence that is
contained in a sealed, secure environment. The feel is **calm, trustworthy,
precise and quiet**: a well-made instrument, a lantern, a specimen jar in a
museum. It is **not** spooky, cartoonish, Halloween, horror, or "hacker".

## Palette (exact hex)

These come from the site theme (`src/styles/custom.css`). Use them by name
in the prompts and, more importantly, snap the colors to them when
vectorizing.

| Role | Hex | Notes |
|---|---|---|
| Spectral green (pale) | `#a6f0cf` | The wraith. Dark-mode link color. Main highlight. |
| Spectral green (mid) | `#4cc79a` | Glow, edges, secondary strokes. |
| Deep green | `#17694d` | Light-mode link color; the mark on light backgrounds. |
| Accent border green | `#2e9470` | Light-mode borders; mid-tone on light backgrounds. |
| Green tint | `#e5f5ee` | Light-mode tinted surfaces. |
| Green shadow | `#213a31` | Dark-mode tinted surfaces; deep shadow inside the box. |
| Night (page) | `#222226` | Dark-mode page background. |
| Night (deepest) | `#1d1d20` | Dark-mode content background; favicon tile. |
| Graphite | `#343437` | Dark-mode cards; box walls in dark artwork. |
| Mist | `#f6f5f4` | Dark-mode body text; near-white highlights. |
| Ink | `#242428` | Light-mode headings; the mark's dark outline on light. |
| Paper | `#fafafb` | Light-mode page background. |

Use at most three of these in any single mark: one green, one neutral, and
optionally one more green.

## General negative prompt

Put this list in the generator's negative prompt or "avoid" field. If it has
none, add it to the end of every prompt as "Avoid: ...".

> text, letters, words, watermark, signature, faces, eyes, mouths,
> Halloween, horror, skulls, chains, padlock clip-art, glitch effects, code
> rain, neon cyberpunk, purple, orange, red, lens flare, photo noise, busy
> background, glossy 3D render, drop shadows

Generators routinely invent fake lettering; if a result has any, regenerate
rather than retouch. The wordmark "Wraith Box" is set in type later, never
generated.

## (a) Logo concepts

Generate each direction separately, 4 to 8 variations each. Ask for a
**square, 1:1, 1024x1024** canvas. Every direction has a dark prompt (on
`#1d1d20`) and a light prompt (on `#fafafb`). Both ask for a plain, flat
background, which is easy to remove. Do not ask for a transparent
background: generators that cannot output one tend to paint a fake
checkerboard instead.

### Direction 1: The sealed cube

Dark:

> Flat vector logo. A see-through isometric cube drawn with thin, even
> lines in graphite #343437, with pale green #a6f0cf edges. Inside the cube
> is a soft wisp of green light #4cc79a, shaped like a candle flame. The
> wisp stays inside the cube. Calm and precise, like a museum display case.
> Plain flat background #1d1d20. Square, centered, wide empty margin.

Light:

> Flat vector logo. A see-through isometric cube drawn with thin, even
> lines in ink #242428, with deep green #17694d edges. Inside the cube is a
> soft wisp of green light #2e9470, shaped like a candle flame. The wisp
> stays inside the cube. Calm and precise, like a museum display case.
> Plain flat background #fafafb. Square, centered, wide empty margin.

### Direction 2: Ghost in a tile

Dark:

> Flat vector logo. A rounded square tile in deep green #17694d. Inside it
> is a simple ghost shape in pale green #a6f0cf, with a round top and a
> wavy bottom edge, and no face. A thin pale green line runs around the
> inside edge of the tile, and the ghost does not touch it. Symmetrical
> and quiet. Plain flat background #1d1d20. Square, centered, wide empty
> margin.

Light:

> Flat vector logo. A rounded square tile in deep green #17694d. Inside it
> is a simple ghost shape in pale green #a6f0cf, with a round top and a
> wavy bottom edge, and no face. A thin pale green line runs around the
> inside edge of the tile, and the ghost does not touch it. Symmetrical
> and quiet. Plain flat background #fafafb. Square, centered, wide empty
> margin.

### Direction 3: Bell jar

Dark:

> Flat vector logo. A glass bell jar on a short flat base, drawn with one
> thin, even line in off-white #f6f5f4. Under the glass floats a small,
> soft ball of pale green light #a6f0cf with a faint green glow #4cc79a.
> Calm and minimal, like a scientific instrument. Plain flat background
> #1d1d20. Square, centered, wide empty margin.

Light:

> Flat vector logo. A glass bell jar on a short flat base, drawn with one
> thin, even line in ink #242428. Under the glass floats a small, soft ball
> of green light #4cc79a with a faint pale green glow #a6f0cf. Calm and
> minimal, like a scientific instrument. Plain flat background #fafafb.
> Square, centered, wide empty margin.

### Direction 4: Ghost hem in a frame (monogram)

The idea is that the shape also reads as a "W". The prompts leave that out
on purpose, because naming a letter makes generators draw letters. Bring
out the "W" in the vector redraw.

Dark:

> Flat vector logo. A square frame with rounded corners, drawn with an even
> graphite #343437 line. Inside it is one smooth pale green #a6f0cf shape:
> the bottom edge of a floating ghost, made of three soft scallops pointing
> down. Even line weights, perfectly symmetrical. Plain flat background
> #1d1d20. Square, centered, wide empty margin.

Light:

> Flat vector logo. A square frame with rounded corners, drawn with an even
> ink #242428 line. Inside it is one smooth deep green #17694d shape: the
> bottom edge of a floating ghost, made of three soft scallops pointing
> down. Even line weights, perfectly symmetrical. Plain flat background
> #fafafb. Square, centered, wide empty margin.

### What makes a logo direction usable

- Reads in one flat color (test: fill it all with `#17694d` on white and
  with `#a6f0cf` on `#1d1d20`).
- Still recognizable at 32 px wide.
- No more than about 6 to 10 distinct shapes after vectorizing.

## (b) Icon / favicon

The favicon must read at **16 and 32 px**, so it is a simplification of the
chosen logo, not a separate idea. Generate it at **512x512, 1:1**, then
shrink to 16 and 32 px to check it before vectorizing.

Dark, on a tile:

> Flat app icon. A rounded square tile in #1d1d20. In the middle is a solid
> ghost shape in pale green #a6f0cf, with a round top, straight sides and a
> bottom edge of three soft scallops. No face. Flat color only: no outline,
> no glow, no gradient. The ghost fills about two thirds of the tile
> height. Two colors only. Bold and simple enough to read at 16 pixels.
> Square, crisp edges.

Light, without a tile, for light browser tabs (if the dark tile looks
heavy). Remove the background when vectorizing:

> Flat app icon. A solid ghost shape in deep green #17694d, with a round
> top, straight sides and a bottom edge of three soft scallops. No face.
> Flat color only: no outline, no glow, no gradient. The ghost fills about
> two thirds of the image height. Plain flat background #fafafb. Two colors
> only. Bold and simple enough to read at 16 pixels. Square, crisp edges.

Negative prompt additions for the icon: `fine detail, thin lines, small
shapes, shading, perspective, 3D, background scene`.

Size checks to do before accepting a design:

- At 16 px the ghost should still be 10 to 11 px tall, with the scallops
  merging into a wavy bottom edge rather than vanishing.
- The silhouette must not touch the tile edge.
- It must be distinguishable from the generic "ghost" emoji and from the
  Snapchat logo (no yellow, no outline-only ghost).

## (c) Hero image for the docs landing page

Starlight shows the hero image to the right of the title on wide screens
and above it on narrow screens. It should be calm and atmospheric, not
compete with the text, and work at about 400 to 600 px wide on screen.

Generate two versions: one for dark mode and one for light mode.

- Aspect ratio **4:3** (or 1:1); generate at **1600x1200** so it stays sharp
  on high-density screens.
- Subject centred with soft edges that fade into the page color, so no
  hard rectangle shows on the page.

Dark:

> Calm flat vector illustration with soft gradients. A glass cube floats in
> a dark, quiet space #222226. Inside the cube, a soft wisp of pale green
> light #a6f0cf curls upward with a gentle green glow #4cc79a. The wisp
> stays inside the cube. Thin graphite #343437 lines spread out from the
> base of the cube like a blueprint grid and fade away. A few small specks
> of light inside the cube, none outside. Serene and secure. The edges of
> the image fade into #222226. 4:3, subject in the middle, lots of empty
> space.

Light:

> Calm flat vector illustration with soft gradients. A glass cube with deep
> green #17694d edges floats in a light, quiet space #fafafb. Inside the
> cube, a soft wisp of green light #4cc79a curls upward with a pale green
> glow #e5f5ee. The wisp stays inside the cube. Thin light grey #e0e0e1
> lines spread out from the base of the cube like a blueprint grid and fade
> away. A few small specks of light inside the cube, none outside. Serene
> and secure. The edges of the image fade into #fafafb. 4:3, subject in the
> middle, lots of empty space.

Negative prompt additions for the hero: `people, hands, computers, screens,
keyboards, cloud icons, busy detail, dramatic lighting, fog over the
subject, frame or border`.

Once a logo direction is chosen, repeat its shape (cube, jar, tile) in the
hero prompt so the two match.

## From raster to assets

1. **Pick and clean.** Choose one result per asset. Crop to the mark and
   remove the background (Preview's Instant Alpha, GIMP, or a remove-bg
   step).
2. **Vectorize the logo and icon.** Trace with Inkscape (Path > Trace Bitmap,
   "Multiple scans: colors", 2 to 4 colors, smooth on, stack scans off),
   `potrace` (one color at a time) or `vtracer`. Then clean up by hand:
   delete stray nodes, make curves symmetrical, snap the fills to the exact
   palette hex values, and set a square `viewBox` such as `0 0 64 64`. A
   redraw from scratch with simple shapes is often cleaner than a trace;
   the generated image is the sketch.
3. **Optimize.** Run the SVG through `svgo` (already in the docs site's
   dependency tree via Astro) or SVGOMG. Remove editor metadata. Keep a
   `role="img"` and `aria-label="Wraith Box"` on the root.
4. **Logo files.** Save as `src/assets/logo-dark.svg` (pale artwork, for the
   dark theme) and `src/assets/logo-light.svg` (deep-green artwork), then
   uncomment the LOGO HOOK in `astro.config.mjs`. The header logo renders
   about 32 to 40 px tall.
5. **Favicon.** Put the icon's SVG markup into `scripts/gen-favicon.mjs`
   (replacing the placeholder document glyph) and run
   `mise run doc:favicon`. It writes `public/favicon.svg` and the 180x180
   `public/apple-touch-icon.png`. For a favicon that follows the browser
   theme, add a `<style>@media (prefers-color-scheme: dark) {...}</style>`
   block inside the SVG that swaps the fill colors. Check the result at
   16 and 32 px in a real browser tab, both light and dark.
6. **Hero.** Keep the hero raster (it relies on soft gradients). Export to
   WebP or AVIF at 1600x1200 and about 150 KB or less, save as
   `src/assets/hero-dark.webp` and `src/assets/hero-light.webp`, and add
   them to `src/content/docs/index.mdx` as shown in the HERO HOOK in
   `astro.config.mjs`. Astro's image pipeline (sharp) resizes them at build
   time.
7. **License check.** Read the generator's terms: the output must be usable
   commercially and must not carry a license that conflicts with the
   project's own (spec 010). Record the tool and date in the commit message
   that adds the assets.
