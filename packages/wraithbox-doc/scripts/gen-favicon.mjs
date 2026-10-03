// Generate the site favicons + apple-touch-icon from the brand icon masters
// in design/. The SVGs there are canonical; this script only derives files.
//
//   public/favicon.svg         design/icon-small.svg, switching to the colors of
//                              design/icon-small-inverted.svg when the browser
//                              is in dark mode (prefers-color-scheme)
//   public/favicon.ico         a copy of design/icon.ico (16 px entry is the
//                              small variant), for browsers without SVG icons
//   public/apple-touch-icon.png  design/icon.svg at 180x180 on a full tile: iOS
//                              rounds the corners itself
//
// The header logo is not copied: astro.config.mjs points at design/ directly.
//
//   mise run doc:favicon   (or: bun run scripts/gen-favicon.mjs here)
import sharp from 'sharp';
import { copyFileSync, readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const docsDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const design = (name) => join(docsDir, 'design', name);

// Pull the tile color, ghost color, ghost transform and ghost outline out of a
// small icon. Fails loudly if the master no longer has the expected shape.
function parseSmallIcon(name) {
	const svg = readFileSync(design(name), 'utf8');
	const tile = svg.match(/<rect width="64" height="64" rx="11" fill="(#[0-9a-f]{6})"\/>/);
	const ghost = svg.match(/<path fill="(#[0-9a-f]{6})" transform="([^"]+)" d="([^"]+)"\/>/);
	if (!tile || !ghost) {
		throw new Error(`${name}: expected one tile <rect> and one ghost <path>`);
	}
	return { tile: tile[1], ghost: ghost[1], transform: ghost[2], d: ghost[3] };
}

const light = parseSmallIcon('icon-small.svg');
const dark = parseSmallIcon('icon-small-inverted.svg');
if (light.d !== dark.d || light.transform !== dark.transform) {
	throw new Error('icon-small.svg and icon-small-inverted.svg must have the same ghost shape');
}

const favicon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="Wraith Box">
  <style>
    .tile { fill: ${light.tile}; }
    .ghost { fill: ${light.ghost}; }
    @media (prefers-color-scheme: dark) {
      .tile { fill: ${dark.tile}; }
      .ghost { fill: ${dark.ghost}; }
    }
  </style>
  <rect class="tile" width="64" height="64" rx="11"/>
  <path class="ghost" transform="${light.transform}" d="${light.d}"/>
</svg>
`;
writeFileSync(join(docsDir, 'public/favicon.svg'), favicon);

copyFileSync(design('icon.ico'), join(docsDir, 'public/favicon.ico'));

// Flatten onto the tile color so the transparent corners do not turn black.
await sharp(design('icon.svg'), { density: 300 })
	.resize(180, 180)
	.flatten({ background: light.tile })
	.png()
	.toFile(join(docsDir, 'public/apple-touch-icon.png'));

console.log('wrote public/favicon.svg, public/favicon.ico, public/apple-touch-icon.png');
