// Generate the site favicon + apple-touch-icon from the LSD Warm palette.
//
// A simple "document" glyph: an accent-coloured rounded page with a few text
// lines, legible at favicon sizes. Swap this out for your own brand mark.
//
//   cd docs && bun run scripts/gen-favicon.mjs
//
// Writes public/favicon.svg and public/apple-touch-icon.png.
import sharp from 'sharp';
import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const docsDir = join(dirname(fileURLToPath(import.meta.url)), '..');

const ACCENT = '#c17a23'; // LSD Warm accent
const PAGE = '#ffebd2'; // warm off-white "paper"
const LINE = '#c17a23'; // text lines

// A page (rounded rect) with a folded corner and three text lines.
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64" role="img" aria-label="Docs">
  <rect x="6" y="6" width="52" height="52" rx="10" fill="${ACCENT}"/>
  <path d="M20 16 h18 l8 8 v24 a2 2 0 0 1 -2 2 H20 a2 2 0 0 1 -2 -2 V18 a2 2 0 0 1 2 -2 z" fill="${PAGE}"/>
  <path d="M38 16 v8 h8 z" fill="${ACCENT}" opacity="0.35"/>
  <g stroke="${LINE}" stroke-width="2.5" stroke-linecap="round">
    <line x1="24" y1="34" x2="40" y2="34"/>
    <line x1="24" y1="40" x2="40" y2="40"/>
    <line x1="24" y1="46" x2="34" y2="46"/>
  </g>
</svg>
`;

writeFileSync(join(docsDir, 'public/favicon.svg'), svg);

// apple-touch-icon: the glyph on a white 180x180 tile with a little padding.
const inner = await sharp(Buffer.from(svg), { density: 400 })
	.resize(160, 160, { fit: 'contain', background: '#ffffff' })
	.png()
	.toBuffer();
await sharp({ create: { width: 180, height: 180, channels: 4, background: '#ffffff' } })
	.composite([{ input: inner, gravity: 'center' }])
	.png()
	.toFile(join(docsDir, 'public/apple-touch-icon.png'));

console.log('wrote public/favicon.svg, public/apple-touch-icon.png');
