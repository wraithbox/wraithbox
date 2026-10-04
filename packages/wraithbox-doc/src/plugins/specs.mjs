// @ts-check
// Publishes the design documents in docs/ (the source of truth, outside
// this package) as site pages: docs/spec/ under /spec/, docs/requirements/
// under /requirements/, and so on for each of `designDirs`
// (S01-spec-based-development lists what goes where). Each document is
// copied into src/content/docs/<dir>/, which is gitignored, with Starlight
// frontmatter taken from the document itself: the first `# ` heading
// becomes the title and the `**Purpose:**` line the description. Relative
// links between design documents become site links. Links to other
// repository files go to GitHub.
//
// The copy runs whenever Astro loads its config (dev, build, check). The
// dev server watches the documents and reloads the config when one
// changes, so documents never need frontmatter of their own and nobody
// edits the copies.
//
// The copy also checks that every item with its own file is listed in
// the index of its kind (X03-network-path.md in X00-index.md), and fails
// the build when one is missing.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { failures, purposeOf, refsSources } from './remark-refs.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, '../../../..');
export const docsDir = path.join(repoRoot, 'docs');
/** The directories under docs/ that are published, each under /<dir>/. */
export const designDirs = ['spec', 'requirements', 'threats', 'spikes', 'research', 'versions'];
const contentDir = path.resolve(here, '../content/docs');
const githubBlob = 'https://github.com/wraithbox/wraithbox/blob/main/';
const githubEdit = 'https://github.com/wraithbox/wraithbox/edit/main/';

/** @param {string} dir @param {string} ext @returns {string[]} paths relative to dir */
function listFiles(dir, ext, prefix = '') {
	/** @type {string[]} */
	const found = [];
	if (!fs.existsSync(dir)) return found;
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const rel = path.join(prefix, entry.name);
		if (entry.isDirectory()) found.push(...listFiles(path.join(dir, entry.name), ext, rel));
		else if (entry.name.endsWith(ext)) found.push(rel);
	}
	return found;
}

/**
 * Every design document, or every file with another extension, as a path
 * relative to docs/ (`spec/S04-architecture.md`).
 */
export function designFiles(ext = '.md') {
	return designDirs.flatMap((dir) => listFiles(path.join(docsDir, dir), ext, dir)).sort();
}

/** The site path of a design document, from its path relative to docs/. */
export function designUrl(/** @type {string} */ rel) {
	return `/${rel.replace(/\.md$/, '').split(path.sep).join('/').toLowerCase()}/`;
}

/** Turns a design document's Markdown into a site page. */
export function toPage(/** @type {string} */ rel, /** @type {string} */ source) {
	const heading = source.match(/^# (.+)$/m);
	if (!heading) throw new Error(`docs/${rel}: no "# " title heading`);
	const title = heading[1].trim();
	const description = purposeOf(source) || undefined;
	let body = source.replace(heading[0], '').replace(/^\n+/, '');
	body = inlineFigures(rel, body);
	body = body.replace(/\]\(([^)\s]+)\)/g, (whole, /** @type {string} */ target) => {
		if (/^(https?:|mailto:|\/|#)/.test(target)) return whole;
		const [file, hash] = target.split('#');
		const fromRepo = path.relative(repoRoot, path.resolve(docsDir, path.dirname(rel), file));
		const anchor = hash ? `#${hash}` : '';
		const fromDocs = path.relative('docs', fromRepo);
		if (designDirs.includes(fromDocs.split(path.sep)[0]) && fromDocs.endsWith('.md')) {
			return `](${designUrl(fromDocs)}${anchor})`;
		}
		return `](${githubBlob}${fromRepo.split(path.sep).join('/')}${anchor})`;
	});
	const frontmatter = [
		'---',
		`title: ${JSON.stringify(title)}`,
		...(description ? [`description: ${JSON.stringify(description)}`] : []),
		`editUrl: ${JSON.stringify(`${githubEdit}docs/${rel.split(path.sep).join('/')}`)}`,
		'---',
		'',
		'',
	].join('\n');
	return frontmatter + body;
}

/**
 * Replaces `![caption](name.svg)` for an SVG next to the document with the
 * SVG itself in a `wb-figure`, so its classes take the site's theme colors
 * (src/styles/brief.css). The SVG's own <style> is for viewers that show
 * the file alone, such as GitHub, and is dropped here.
 */
function inlineFigures(/** @type {string} */ rel, /** @type {string} */ body) {
	return body.replace(/^!\[([^\]]*)\]\(([^)\s]+\.svg)\)$/gm, (whole, caption, file) => {
		const svgPath = path.resolve(docsDir, path.dirname(rel), file);
		if (!svgPath.startsWith(docsDir) || !fs.existsSync(svgPath)) return whole;
		const svg = fs
			.readFileSync(svgPath, 'utf8')
			.replace(/<\?xml[^>]*>/, '')
			.replace(/<style>[\s\S]*?<\/style>/, '')
			.split('\n')
			.filter((line) => line.trim())
			.join('\n');
		return `<figure class="wb-figure">\n${svg}\n<figcaption>${caption}</figcaption>\n</figure>`;
	});
}

/** Markdown inline text to plain text, on one line. */
export function plain(/** @type {string} */ text) {
	return text
		.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
		.replace(/[`*_]/g, '')
		.replace(/\s+/g, ' ')
		.trim();
}

/**
 * Fails when an item with its own file (`X03-network-path.md`) is not
 * named in the index of its kind (`X00-index.md`) in the same directory.
 * @param {string[]} files paths relative to docs/
 */
function checkIndexes(files) {
	for (const rel of files) {
		const m = path.basename(rel).match(/^([A-Z]+?)(\d+)-/);
		if (!m || /^0+$/.test(m[2])) continue;
		const id = `${m[1]}${m[2]}`;
		const dir = path.dirname(rel);
		const index = files.find((f) => path.dirname(f) === dir && path.basename(f).startsWith(`${m[1]}00-`));
		if (!index) throw new Error(`docs/${rel}: no ${m[1]}00-index.md next to it`);
		const text = fs.readFileSync(path.join(docsDir, index), 'utf8');
		if (!new RegExp(`\\b${id}\\b`).test(text)) {
			throw new Error(`docs/${rel}: ${id} is not listed in docs/${index}`);
		}
	}
}

/** Copies every design document into the content tree, removing copies of deleted ones. */
export function syncDesign() {
	const files = designFiles();
	checkIndexes(files);
	const wanted = new Set();
	for (const rel of files) {
		const target = path.join(contentDir, rel.toLowerCase());
		wanted.add(target);
		const page = toPage(rel, fs.readFileSync(path.join(docsDir, rel), 'utf8'));
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
	for (const dir of designDirs) {
		for (const rel of listFiles(path.join(contentDir, dir), '.md', dir)) {
			const target = path.join(contentDir, rel);
			if (!wanted.has(target)) fs.rmSync(target);
		}
	}
}

/** @returns {import('astro').AstroIntegration} */
export function specsIntegration() {
	return {
		name: 'wraithbox-design-docs',
		hooks: {
			'astro:config:setup': ({ addWatchFile }) => {
				syncDesign();
				// A change to a document or to the issue snapshot reloads the
				// config, which gives the reference plugin a new digest
				// (remark-refs.mjs).
				for (const file of refsSources()) addWatchFile(file);
				for (const rel of designFiles('.svg')) addWatchFile(path.join(docsDir, rel));
			},
			'astro:build:done': () => {
				if (failures.length > 0) {
					throw new Error(`references that are wrong:\n${failures.join('\n')}`);
				}
			},
		},
	};
}
