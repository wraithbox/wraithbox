// @ts-check
// Publishes the design documents in docs/ (the source of truth, outside
// this package) as site pages: docs/spec/ under /spec/, docs/requirements/
// under /requirements/, docs/briefs/ under /briefs/, and so on for each
// of `designDirs`
// (S01-spec-based-development lists what goes where). Each document is
// copied into src/content/docs/<dir>/, which is gitignored, with Starlight
// frontmatter taken from the document itself: the first `# ` heading
// becomes the title and the `**Purpose:**` line the description. A
// review brief is MDX with its own frontmatter, which is kept. Relative
// links between design documents become site links. Links to other
// repository files go to GitHub.
//
// The copy runs whenever Astro loads its config (dev, build, check). The
// dev server watches the documents and reloads the config when one
// changes, so documents never need frontmatter of their own and nobody
// edits the copies.
//
// The copy also fails the build when the table of an index (S00-index,
// X00-index, …) is out of date (indexes.mjs), when a reference to a
// section names no section (sections.mjs), or when a spec breaks a size
// limit (structure.mjs).
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { updateIndexes } from './indexes.mjs';
import { failures, purposeOf, refsSources } from './remark-refs.mjs';
import { checkSections } from './sections.mjs';
import { checkStructure } from './structure.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, '../../../..');
export const docsDir = path.join(repoRoot, 'docs');
/** The directories under docs/ that are published, each under /<dir>/. */
export const designDirs = ['briefs', 'spec', 'tests', 'requirements', 'threats', 'spikes', 'research', 'versions'];
const contentDir = path.resolve(here, '../content/docs');
const githubBlob = 'https://github.com/wraithbox/wraithbox/blob/main/';
const githubEdit = 'https://github.com/wraithbox/wraithbox/edit/main/';

/** @param {string} dir @param {RegExp} ext @returns {string[]} paths relative to dir */
function listFiles(dir, ext, prefix = '') {
	/** @type {string[]} */
	const found = [];
	if (!fs.existsSync(dir)) return found;
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const rel = path.join(prefix, entry.name);
		if (entry.isDirectory()) found.push(...listFiles(path.join(dir, entry.name), ext, rel));
		else if (ext.test(entry.name)) found.push(rel);
	}
	return found;
}

/**
 * Every design document (Markdown or MDX), or every file matching `ext`,
 * as a path relative to docs/ (`spec/S02-toolchain.md`).
 */
export function designFiles(ext = /\.mdx?$/) {
	return designDirs.flatMap((dir) => listFiles(path.join(docsDir, dir), ext, dir)).sort();
}

/** The site path of a design document, from its path relative to docs/. */
export function designUrl(/** @type {string} */ rel) {
	return `/${rel.replace(/\.mdx?$/, '').split(path.sep).join('/').toLowerCase()}/`;
}

/** Turns a design document's Markdown into a site page. */
export function toPage(/** @type {string} */ rel, /** @type {string} */ source) {
	const editUrl = `editUrl: ${JSON.stringify(`${githubEdit}docs/${rel.split(path.sep).join('/')}`)}`;
	if (rel.endsWith('.mdx')) {
		if (!source.startsWith('---\n')) throw new Error(`docs/${rel}: no frontmatter`);
		// The sidebar shows the ID in front, as for the other documents
		// ("B36 - Can projects …").
		const id = path.basename(rel).match(/^([A-Z]+\d+)-/)?.[1];
		const title = source.match(/^title: (.+)$/m)?.[1].replace(/^"(.*)"$/, '$1');
		const label = id && title ? `\nsidebar:\n  label: ${JSON.stringify(`${id} - ${title}`)}` : '';
		return source.replace(/\n---\n/, `\n${editUrl}${label}\n---\n`);
	}
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
		if (designDirs.includes(fromDocs.split(path.sep)[0]) && /\.mdx?$/.test(fromDocs)) {
			return `](${designUrl(fromDocs)}${anchor})`;
		}
		return `](${githubBlob}${fromRepo.split(path.sep).join('/')}${anchor})`;
	});
	const frontmatter = [
		'---',
		`title: ${JSON.stringify(title)}`,
		...(description ? [`description: ${JSON.stringify(description)}`] : []),
		editUrl,
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

/** A spec's overview, which is the spec itself: `spec/S07-egress-gateway/S07-00-egress-gateway.md`. */
const isOverview = (/** @type {string} */ rel) => /^S\d{2,}-00-/.test(path.basename(rel));

/**
 * The sidebar entries of the specs. A spec with pages of its own
 * (`docs/spec/S07-egress-gateway/`, S01-spec-based-development, "Spec
 * tree") is a group: its overview, then its pages in order. Starlight's
 * own `autogenerate` would label that group with the directory name.
 * @returns {any[]}
 */
export function specSidebar() {
	const slug = (/** @type {string} */ rel) => designUrl(rel).slice(1, -1);
	const files = designFiles().filter((rel) => rel.startsWith(`spec${path.sep}`));
	const specs = files.filter((rel) => path.dirname(rel) === 'spec' || isOverview(rel));
	return specs.map((rel) => {
		if (!isOverview(rel)) return { slug: slug(rel) };
		const pages = files.filter((other) => path.dirname(other) === path.dirname(rel) && other !== rel);
		const heading = fs.readFileSync(path.join(docsDir, rel), 'utf8').match(/^# (.+)$/m)?.[1] ?? path.dirname(rel);
		return {
			label: heading,
			collapsed: true,
			items: [{ slug: slug(rel), label: 'Overview' }, ...pages.map((page) => ({ slug: slug(page) }))],
		};
	});
}

/**
 * Redirects from the site path a spec had before it became a tree
 * (`/spec/s07-egress-gateway/`) to its overview, so older links keep
 * working.
 * @returns {Record<string, string>}
 */
export function specRedirects() {
	return Object.fromEntries(
		designFiles()
			.filter((rel) => rel.startsWith(`spec${path.sep}`) && isOverview(rel))
			.map((rel) => [designUrl(path.dirname(rel)).slice(0, -1), designUrl(rel)]),
	);
}

/** Markdown inline text to plain text, on one line. */
export function plain(/** @type {string} */ text) {
	return text
		.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
		.replace(/[`*_]/g, '')
		.replace(/\s+/g, ' ')
		.trim();
}

/** Copies every design document into the content tree, removing copies of deleted ones. */
export function syncDesign() {
	const files = designFiles();
	const stale = updateIndexes({ write: false });
	if (stale.length > 0) {
		throw new Error(`index tables out of date, run mise run doc:index: ${stale.join(', ')}`);
	}
	const broken = checkSections();
	if (broken.length > 0) {
		throw new Error(`section references that name no section:\n${broken.join('\n')}`);
	}
	const { strict } = checkStructure();
	if (strict.length > 0) {
		throw new Error(`specs over the limits of structure.mjs (mise run doc:structure):\n${strict.join('\n')}`);
	}
	const wanted = new Set();
	for (const rel of files) {
		const target = path.join(contentDir, rel.toLowerCase());
		wanted.add(target);
		const page = toPage(rel, fs.readFileSync(path.join(docsDir, rel), 'utf8'));
		fs.mkdirSync(path.dirname(target), { recursive: true });
		const current = fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : null;
		if (current !== page) {
			// Write and rename, so another Astro process that reads the
			// pages (the dev server, or a check and a build started by hand)
			// never reads a half-written one. `mise run ci` runs check and
			// build one after the other (`wait_for` in .mise.toml).
			const temp = `${target}.${process.pid}.tmp`;
			fs.writeFileSync(temp, page);
			fs.renameSync(temp, target);
		}
	}
	for (const dir of designDirs) {
		for (const rel of listFiles(path.join(contentDir, dir), /\.mdx?$/, dir)) {
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
				for (const rel of designFiles(/\.svg$/)) addWatchFile(path.join(docsDir, rel));
			},
			'astro:build:done': () => {
				if (failures.length > 0) {
					throw new Error(`references that are wrong:\n${failures.join('\n')}`);
				}
			},
		},
	};
}
