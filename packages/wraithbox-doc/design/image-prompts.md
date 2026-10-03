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

Append this (or the generator's equivalent "avoid" field) to every prompt:

> no text, no letters, no words, no watermark, no signature, no logo
> lettering, no gibberish typography, no Halloween, no jack-o-lantern, no
> cartoon ghost with a face, no eyes, no mouth, no skull, no horror, no blood,
> no chains, no padlock clip-art, no glitch effects, no matrix code rain, no
> neon cyberpunk, no purple, no orange, no red, no lens flare, no photographic
> noise, no busy background, no 3D render plastic look, no drop-shadow clutter

Generators routinely invent fake lettering; if a result has any, regenerate
rather than retouch. The wordmark "Wraith Box" is set in type later, never
generated.

## (a) Logo concepts

Generate each direction separately, 4 to 8 variations each. Ask for a
**square, 1:1, 1024x1024** canvas, **flat vector style**, centred mark with
generous padding (about 15% margin on every side), on a **plain solid
background** (`#1d1d20` for the dark version; regenerate on `#fafafb` for the
light one, or invert when vectorizing). Request transparency if the
generator supports it; otherwise a flat solid background is easiest to
remove.

### Direction 1: The sealed cube

> Minimal flat vector logo mark: an isometric cube drawn with clean, even
> line weight in graphite `#343437` with pale spectral green `#a6f0cf` edges.
> Inside the cube, seen through one translucent face, a soft, faceless,
> tapering wisp of green light `#4cc79a` rises like a candle flame or a
> curl of mist, fully contained by the walls. The cube's seams are crisp and
> closed. Geometric, calm, precise, trustworthy, like a museum specimen case.
> Solid background `#1d1d20`, centred, generous padding, 1:1 square, no
> gradients except a subtle inner glow on the wisp.

### Direction 2: Ghost silhouette as negative space

> Minimal flat vector logo mark: a rounded square, like an app icon tile, in
> deep green `#17694d`. Cut out of it as negative space is the simple
> silhouette of a classic ghost shape: a dome top and a gently scalloped
> hem, no face, no eyes. The silhouette fills about 60% of the tile and is
> pale spectral green `#a6f0cf`. A thin inset border runs around the inside
> of the tile, suggesting a container wall that the ghost does not cross.
> Swiss design, geometric, symmetrical, quiet. Solid background `#fafafb`,
> 1:1 square, centred.

### Direction 3: Lantern / bell jar

> Minimal flat vector logo mark: a glass bell jar (cloche) on a short flat
> base, drawn in a single continuous line of even weight in ink `#242428`.
> Under the glass floats a small soft orb of pale spectral green light
> `#a6f0cf` with a faint halo `#4cc79a`, like a firefly kept safe or a pilot
> light. Calm, scientific, Victorian-instrument elegance but modern and
> minimal. Plenty of empty space. Solid background `#fafafb`, 1:1 square,
> centred, no shading except the soft glow.

### Direction 4: The "W" in a box (monogram)

> Minimal flat vector monogram mark: a square frame with rounded corners,
> stroke in graphite `#343437`. Inside, an abstract shape that reads both as
> the hem of a floating ghost (three soft downward scallops) and as a
> stylized letter W, drawn as one smooth pale spectral green `#a6f0cf` form.
> Geometric construction, even stroke weights, perfectly symmetrical,
> works in one color. Solid background `#1d1d20`, 1:1 square, centred.

(For this one the generator may try to draw letters; if so, describe it
only as "three soft downward scallops" and leave the W reading to the
vector redraw.)

### What makes a logo direction usable

- Reads in one flat color (test: fill it all with `#17694d` on white and
  with `#a6f0cf` on `#1d1d20`).
- Still recognizable at 32 px wide.
- No more than about 6 to 10 distinct shapes after vectorizing.

## (b) Icon / favicon

The favicon must read at **16 and 32 px**, so it is a simplification of the
chosen logo, not a separate idea. Generate it at **512x512, 1:1**, then
shrink to 16 and 32 px to check it before vectorizing.

> App icon / favicon, extremely simple bold flat silhouette: a rounded square
> tile in night `#1d1d20`, corner radius about 20% of the width. In the
> centre, a single solid ghost silhouette in pale spectral green `#a6f0cf`:
> a rounded dome top, straight sides, and a hem of three soft scallops. No
> face, no eyes, no outline, no gradient, no glow, no texture. The
> silhouette fills about 65% of the tile height with even margins. Two
> colors only. Must remain recognizable at 16x16 pixels. 1:1, 512x512,
> centred, crisp edges, vector style.

Variant for light browser tabs (if the dark tile looks heavy):

> Same silhouette in deep green `#17694d` on a transparent background, no
> tile.

Negative prompt additions for the icon: `no fine detail, no thin lines, no
small shapes, no shading, no perspective, no 3D, no background scene`.

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

Dark version:

> Calm, atmospheric illustration, flat vector with soft gradients: a single
> translucent glass cube floating in a dark, quiet space of night `#222226`.
> Inside the cube, a soft, faceless wisp of pale spectral green light
> `#a6f0cf` curls upward, gently glowing `#4cc79a`, fully contained by the
> glass walls; the walls catch a faint green reflection. Thin, precise
> graphite `#343437` lines extend from the cube's base like a circuit or a
> blueprint grid, fading out with distance, suggesting a controlled
> environment. A few tiny motes of light inside the cube only, none outside.
> Mood: serene, secure, trustworthy, quiet focus. Edges of the image fade
> smoothly into `#222226`. 4:3, 1600x1200, subject centred, lots of
> negative space.

Light version:

> The same composition on paper `#fafafb`: a translucent glass cube with
> deep green `#17694d` edges, the wisp inside in spectral green `#4cc79a`
> with a pale tint `#e5f5ee` glow; blueprint lines in light grey `#e0e0e1`.
> Edges fade into `#fafafb`. Same mood, same framing, 4:3, 1600x1200.

Negative prompt additions for the hero: `no people, no hands, no computers,
no screens, no keyboards, no cloud icons, no busy detail, no dramatic
lighting, no fog that covers the subject, no frame or border`.

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
