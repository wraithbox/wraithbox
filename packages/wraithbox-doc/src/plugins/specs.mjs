// @ts-check
// Publishes the specs in docs/spec/ (the source of truth, outside this
// package) as site pages under /spec/. Each spec is copied into
// src/content/docs/spec/, which is gitignored, with Starlight frontmatter
// taken from the spec itself: the first `# ` heading becomes the title and
// the `**Purpose:**` line the description. Relative links between specs
// become site links. Links to other repository files go to GitHub.
//
// The copy runs whenever Astro loads its config (dev, build, check). The
// dev server watches the specs and reloads the config when one changes,
// so specs never need frontmatter of their own and nobody edits the
// copies.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { refsSources } from './remark-refs.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, '../../../..');
export const specDir = path.join(repoRoot, 'docs/spec');
const outDir = path.resolve(here, '../content/docs/spec');
const githubBlob = 'https://github.com/wraithbox/wraithbox/blob/main/';
const githubEdit = 'https://github.com/wraithbox/wraithbox/edit/main/';

/** @param {string} dir @returns {string[]} spec paths relative to dir */
function listSpecs(dir, prefix = '') {
	/** @type {string[]} */
	const found = [];
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const rel = path.join(prefix, entry.name);
		if (entry.isDirectory()) found.push(...listSpecs(path.join(dir, entry.name), rel));
		else if (entry.name.endsWith('.md')) found.push(rel);
	}
	return found;
}

/** The site path of a spec file, relative to docs/spec. */
export function specUrl(/** @type {string} */ rel) {
	return `/spec/${rel.replace(/\.md$/, '').split(path.sep).join('/').toLowerCase()}/`;
}

/** Turns a spec's Markdown into a site page. */
export function toPage(/** @type {string} */ rel, /** @type {string} */ source) {
	const heading = source.match(/^# (.+)$/m);
	if (!heading) throw new Error(`docs/spec/${rel}: no "# " title heading`);
	const title = heading[1].trim();
	const purpose = source.match(/^\*\*Purpose:\*\*\s*([\s\S]*?)(?:\n\n|$)/m);
	const description = purpose ? plain(purpose[1]) : undefined;
	let body = source.replace(heading[0], '').replace(/^\n+/, '');
	body = body.replace(/\]\(([^)\s]+)\)/g, (whole, /** @type {string} */ target) => {
		if (/^(https?:|mailto:|\/|#)/.test(target)) return whole;
		const [file, hash] = target.split('#');
		const fromRepo = path.relative(repoRoot, path.resolve(specDir, path.dirname(rel), file));
		const anchor = hash ? `#${hash}` : '';
		if (fromRepo.startsWith('docs/spec/') && fromRepo.endsWith('.md')) {
			return `](${specUrl(fromRepo.slice('docs/spec/'.length))}${anchor})`;
		}
		return `](${githubBlob}${fromRepo.split(path.sep).join('/')}${anchor})`;
	});
	const frontmatter = [
		'---',
		`title: ${JSON.stringify(title)}`,
		...(description ? [`description: ${JSON.stringify(description)}`] : []),
		`editUrl: ${JSON.stringify(`${githubEdit}docs/spec/${rel.split(path.sep).join('/')}`)}`,
		'---',
		'',
		'',
	].join('\n');
	return frontmatter + body;
}

/** Markdown inline text to plain text, on one line. */
export function plain(/** @type {string} */ text) {
	return text
		.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
		.replace(/[`*_]/g, '')
		.replace(/\s+/g, ' ')
		.trim();
}

/** Copies every spec into the content tree, removing copies of deleted specs. */
export function syncSpecs() {
	const specs = listSpecs(specDir);
	const wanted = new Set();
	for (const rel of specs) {
		const target = path.join(outDir, rel.toLowerCase());
		wanted.add(target);
		const page = toPage(rel, fs.readFileSync(path.join(specDir, rel), 'utf8'));
		fs.mkdirSync(path.dirname(target), { recursive: true });
		const current = fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : null;
		if (current !== page) {
			// Write and rename, so `astro check` and `astro build` running at
			// the same time (mise run ci) never read a half-written page.
			const temp = `${target}.${process.pid}.tmp`;
			fs.writeFileSync(temp, page);
			fs.renameSync(temp, target);
		}
	}
	for (const rel of fs.existsSync(outDir) ? listSpecs(outDir) : []) {
		const target = path.join(outDir, rel);
		if (!wanted.has(target)) fs.rmSync(target);
	}
}

/** @returns {import('astro').AstroIntegration} */
export function specsIntegration() {
	return {
		name: 'wraithbox-specs',
		hooks: {
			'astro:config:setup': ({ addWatchFile }) => {
				syncSpecs();
				// A change to a spec or to the issue snapshot reloads the config,
				// which gives the reference plugin a new digest (remark-refs.mjs).
				for (const file of refsSources()) addWatchFile(file);
			},
		},
	};
}
