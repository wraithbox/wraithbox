// @ts-check
// Turns IDs in page text into links with a hover card that shows what they
// point at, so a reader doesn't need to remember numbers. The IDs are those
// of S01-spec-based-development:
//
//   S07-egress-gateway, X00-index, V1-initial  a design document's page
//   B36-flow-attribution                       a review brief (for I36)
//   SEC06-repo-writes, FR01-drop-in, NFR01-…,  a definition in an index
//   T05-…, X14-…, V1-01-…, V1-M2-…             (or the item's own page)
//   I36, PR13                                  a GitHub issue or pull request
//
// An ID without its slug (SEC06) is shown in full, except a version
// (V1), which reads as a name. Titles and descriptions
// come from the design documents at build time. Issue and pull request
// titles come from src/data/github-refs.json, a snapshot that
// `mise run doc:refs` refreshes, so the build stays offline.
//
// These fail the build: an ID with the wrong slug, an issue written as a
// pull request or the other way around, an ID without its leading zero
// (S6, X3), and an ID of the old scheme (F1-drop-in, 007-egress-gateway).
// An unknown ID, "spec 007" and "#36" give a warning and stay plain text.
//
// A Starlight <Badge> whose text starts with an ID is wrapped in the same
// link. The plugin also gives each definition (`- **SEC06-repo-writes:
// Name.** text`) an `id`, which the links point at.
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { designFiles, designUrl, docsDir, plain } from './specs.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));

/**
 * Every failure of this build, as "file: message". Starlight logs a page
 * that fails to render and carries on, so the build itself would pass.
 * `specsIntegration` fails it at the end instead (specs.mjs).
 * @type {string[]}
 */
export const failures = [];
const githubRefsFile = path.resolve(here, '../data/github-refs.json');
const githubRepo = 'https://github.com/wraithbox/wraithbox';

/** @typedef {{ url: string, title: string, description: string }} Target */
/** @typedef {Target & { slug: string, full: string }} Named a target with an ID-slug name */

/** First sentence of a plain-text passage, at most about 220 characters. */
function firstSentence(/** @type {string} */ text) {
	const sentence = text.match(/^(.+?[.?!])(\s|$)/)?.[1] ?? text;
	return sentence.length > 220 ? `${sentence.slice(0, 217).trimEnd()}…` : sentence;
}

/** The `**Purpose:**` paragraph of a document, as plain text. */
export function purposeOf(/** @type {string} */ source) {
	const m = source.match(/^\*\*Purpose:\*\*\s*([\s\S]*?)(?:\n\n|(?![\s\S]))/m);
	return m ? plain(m[1]) : '';
}

const SLUG = String.raw`[a-z0-9]+(?:-[a-z0-9]+)*`;
/** A design ID: S07, SEC06, X14, B36, V1, V1-01, V1-M2. */
const DESIGN_ID = String.raw`(?:FR|NFR|SEC|[BSTXR])\d{2,}|V\d+(?:-(?:M\d+|\d{2,}))?`;
/** A definition list item: `- **SEC06-repo-writes: Name.** text`. */
const DEFINITION = new RegExp(
	String.raw`^- \*\*(${DESIGN_ID})-(${SLUG}):\s*([^*]*?)\.?\*\*([\s\S]*?)(?=\n- \*\*|\n\n|(?![\s\S]))`,
	'gm',
);

/** Every design ID: documents by file name, and the definitions in them. */
function loadDesign() {
	/** @type {Map<string, Named>} */
	const table = new Map();
	/** @type {Map<string, string>} ID to the file that is its own page */
	const pages = new Map();
	const sources = designFiles().map((rel) => ({
		rel,
		source: fs.readFileSync(path.join(docsDir, rel), 'utf8'),
	}));
	for (const { rel, source } of sources) {
		const m = path.basename(rel).replace(/\.mdx?$/, '').match(new RegExp(`^(${DESIGN_ID})-(${SLUG})$`));
		if (!m) continue;
		const [full, id, slug] = m;
		if (pages.has(id)) throw new Error(`remark-refs: ${id} is both docs/${rel} and docs/${pages.get(id)}`);
		pages.set(id, rel);
		// A Markdown document's title is its heading, without the ID in
		// front. A brief (MDX) has its title in the frontmatter.
		const front = (/** @type {string} */ key) =>
			source.match(new RegExp(`^${key}: (.+)$`, 'm'))?.[1].replace(/^"(.*)"$/, '$1');
		const title = rel.endsWith('.mdx')
			? (front('title') ?? full)
			: (source.match(/^# (.+)$/m)?.[1].replace(/^\S+ - /, '') ?? full);
		table.set(id, {
			slug,
			full,
			url: designUrl(rel),
			title: `${full}: ${title}`,
			description: firstSentence(rel.endsWith('.mdx') ? (front('description') ?? '') : purposeOf(source)),
		});
	}
	/** @type {Set<string>} */
	const defined = new Set();
	for (const { rel, source } of sources) {
		for (const m of source.matchAll(DEFINITION)) {
			const [, id, slug, name, rest] = m;
			const full = `${id}-${slug}`;
			if (defined.has(id)) throw new Error(`remark-refs: ${id} is defined twice`);
			defined.add(id);
			const page = pages.has(id) ? table.get(id) : undefined;
			if (page && page.slug !== slug) {
				throw new Error(`remark-refs: ${full} in docs/${rel}, but its page is ${page.full}`);
			}
			// An item with its own page links there, with the summary from its
			// definition in the index.
			table.set(id, {
				slug,
				full,
				url: page ? page.url : `${designUrl(rel)}#${full.toLowerCase()}`,
				title: name ? `${full}: ${name}` : full,
				description: firstSentence(plain(rest)),
			});
		}
	}
	return table;
}

/**
 * A brief takes the number of its GitHub issue (B36 for I36), so each
 * issue has at most one; two briefs with one number already fail in
 * `loadDesign`. This fails a brief numbered after a pull request, and
 * warns when the issue is missing from the snapshot or doesn't link its
 * brief with a `Brief:` line (docs/agents/review-briefs.md).
 * @param {Map<string, Named>} design @param {ReturnType<typeof loadGithub>} github
 */
function checkBriefs(design, github) {
	for (const [id, brief] of design) {
		if (!/^B\d+$/.test(id) || id === 'B00') continue;
		const ref = github[String(Number(id.slice(1)))];
		if (!ref) {
			console.warn(`[remark-refs] ${brief.full}: issue #${Number(id.slice(1))} is not in src/data/github-refs.json`);
		} else if (ref.kind !== 'issue') {
			failures.push(`${brief.full}: #${Number(id.slice(1))} is a pull request, and a brief takes an issue's number`);
		} else if (ref.brief !== brief.full) {
			console.warn(`[remark-refs] ${brief.full}: issue #${Number(id.slice(1))} doesn't link it (a "Brief:" line in its body, then mise run doc:refs)`);
		}
	}
}

function loadGithub() {
	/** @type {Record<string, { kind: 'issue' | 'pr', title: string, state: string, summary: string, brief?: string }>} */
	const refs = fs.existsSync(githubRefsFile)
		? JSON.parse(fs.readFileSync(githubRefsFile, 'utf8'))
		: {};
	return refs;
}

/** @param {string} title @param {string} description */
function tip(title, description) {
	return {
		type: 'wbRefTip',
		data: { hName: 'span', hProperties: { className: ['wb-ref-tip'] } },
		children: [
			{
				type: 'wbRefTipTitle',
				data: { hName: 'span', hProperties: { className: ['wb-ref-tip-title'] } },
				children: [{ type: 'text', value: title }],
			},
			{ type: 'text', value: description },
		],
	};
}

/** @param {any[]} children @param {Target} target */
function refLink(children, target) {
	return {
		type: 'link',
		url: target.url,
		data: { hProperties: { className: ['wb-ref'] } },
		children: [...children, tip(target.title, target.description)],
	};
}

const REF = new RegExp(
	[
		// 1-2: a design ID and its optional slug
		String.raw`\b(${DESIGN_ID})(?:-(${SLUG}))?\b`,
		// 3: an issue or pull request, I36 or PR13
		String.raw`\b((?:I|PR)\d{2,})\b`,
		// 4: an ID without its leading zero, or of the old scheme: fails
		String.raw`\b((?:FR|NFR|SEC|PR|[BSTXRI])\d(?:-${SLUG})?|[FNC]\d{1,2}-${SLUG}|0\d\d-${SLUG})\b`,
		// 5: "spec 007", "issue #36", "PR #12" or a bare "#36": warns
		String.raw`(\b[Ss]pecs? \d{3}\b|(?:\b(?:issue|Issue|PR|pull request) )?(?<![\w/&])#\d+\b)`,
	].join('|'),
	'g',
);

/** Node types whose text is never scanned for references. */
const SKIP = new Set([
	'code',
	'inlineCode',
	'heading',
	'html',
	'yaml',
	'mdxjsEsm',
	'mdxFlowExpression',
	'mdxTextExpression',
	'wbRefTip',
]);

/**
 * @param {{ digest?: string }} [_options] only `digest`: a hash of the
 * sources (`refsDigest`), so Astro's cache resets when they change
 */
export function remarkRefs(_options) {
	const design = loadDesign();
	const github = loadGithub();
	checkBriefs(design, github);

	/** Records a failure for the end of the build, and stops this page. */
	function fail(/** @type {import('vfile').VFile} */ file, /** @type {string} */ message) {
		failures.push(`${path.relative(process.cwd(), file.path ?? '')}: ${message}`);
		file.fail(message);
	}

	/** Astro doesn't print vfile messages, so warnings go to the console. */
	function warn(/** @type {import('vfile').VFile} */ file, /** @type {string} */ message) {
		console.warn(`[remark-refs] ${path.relative(process.cwd(), file.path ?? '')}: ${message}`);
	}

	/** @param {string} number */
	function githubTarget(number) {
		const n = String(Number(number));
		const ref = github[n];
		if (!ref) return null;
		const id = `${ref.kind === 'pr' ? 'PR' : 'I'}${n.padStart(2, '0')}`;
		return {
			id,
			target: {
				url: `${githubRepo}/${ref.kind === 'pr' ? 'pull' : 'issues'}/${n}`,
				title: `${id} (${ref.state}): ${ref.title}`,
				description: ref.summary,
			},
		};
	}

	/**
	 * The nodes for one match, or null to leave the text as it is.
	 * @param {RegExpMatchArray} m @param {import('vfile').VFile} file
	 * @returns {any[] | null}
	 */
	function nodesFor(m, file) {
		const [, id, slug, gh, old, legacy] = m;
		const text = (/** @type {string} */ value) => ({ type: 'text', value });
		if (id) {
			const t = design.get(id);
			if (!t) {
				warn(file, `unknown ID ${id}`);
				return null;
			}
			if (slug && slug !== t.slug) fail(file, `${id}-${slug}: the slug of ${id} is "${t.slug}"`);
			// A version reads as a name in prose ("out of scope for V1"), so
			// it keeps the form it was written in.
			const shown = !slug && /^V\d+$/.test(id) ? id : t.full;
			return [refLink([text(shown)], t)];
		}
		if (gh) {
			const target = githubTarget(gh.replace(/^\D+/, ''));
			if (!target) {
				warn(file, `${gh} is not in src/data/github-refs.json (mise run doc:refs)`);
				return null;
			}
			if (target.id !== gh) fail(file, `${gh}: GitHub number ${Number(gh.replace(/^\D+/, ''))} is ${target.id}`);
			return [refLink([text(target.id)], target.target)];
		}
		if (old) fail(file, `"${old}": write the ID of S01-spec-based-development, with two digits`);
		if (legacy) warn(file, `"${legacy}": write the bare ID (S07-egress-gateway, I36)`);
		return null;
	}

	/**
	 * Splits one text node into text and reference links.
	 * @param {string} value @param {import('vfile').VFile} file
	 * @returns {any[] | null} replacement nodes, or null when nothing matched
	 */
	function split(value, file) {
		/** @type {any[]} */
		const out = [];
		let last = 0;
		for (const m of value.matchAll(REF)) {
			const nodes = nodesFor(m, file);
			if (!nodes) continue;
			const start = /** @type {number} */ (m.index);
			if (start > last) out.push({ type: 'text', value: value.slice(last, start) });
			out.push(...nodes);
			last = start + m[0].length;
		}
		if (out.length === 0) return null;
		if (last < value.length) out.push({ type: 'text', value: value.slice(last) });
		return out;
	}

	/** Adds an id to a definition list item. */
	function markDefinition(/** @type {any} */ listItem) {
		const strong = listItem.children?.[0]?.children?.[0];
		if (strong?.type !== 'strong') return false;
		const label = strong.children?.[0]?.value ?? '';
		const def = label.match(new RegExp(`^((?:${DESIGN_ID})-${SLUG}):`));
		if (!def) return false;
		listItem.data = { ...listItem.data, hProperties: { id: def[1].toLowerCase() } };
		return true;
	}

	/** Adds a hover card to a hand-written link to an issue or pull request. */
	function decorateLink(/** @type {any} */ link) {
		const m = link.url.match(/^https:\/\/github\.com\/wraithbox\/wraithbox\/(?:issues|pull)\/(\d+)$/);
		if (!m) return;
		const gh = githubTarget(m[1]);
		if (!gh) return;
		const only = link.children.length === 1 && link.children[0].type === 'text';
		if (only && /^#\d+$/.test(link.children[0].value)) link.children[0].value = gh.id;
		link.data = { ...link.data, hProperties: { className: ['wb-ref'] } };
		link.children.push(tip(gh.target.title, gh.target.description));
	}

	/**
	 * A <Badge text="SEC06-repo-writes per VM"> becomes a link around the
	 * badge, with the ID in its text written in full.
	 * @param {any} badge @param {import('vfile').VFile} file
	 */
	function linkBadge(badge, file) {
		const attr = badge.attributes?.find((/** @type {any} */ a) => a.name === 'text');
		if (typeof attr?.value !== 'string') return null;
		const m = attr.value.match(new RegExp(`^(?:${REF.source})`));
		if (!m) return null;
		const nodes = nodesFor(m, file);
		const link = nodes?.find((n) => n.type === 'link');
		if (!nodes || nodes.length !== 1 || !link) return null;
		attr.value = link.children[0].value + attr.value.slice(m[0].length);
		link.children[0] = badge;
		return link;
	}

	/** @param {any} node @param {import('vfile').VFile} file @param {number} [from] */
	function walk(node, file, from = 0) {
		if (!node.children) return;
		for (let i = from; i < node.children.length; i++) {
			const child = node.children[i];
			if (SKIP.has(child.type)) continue;
			if (child.type === 'link') {
				decorateLink(child);
				continue;
			}
			if (child.type === 'mdxJsxTextElement' && child.name === 'Badge') {
				const link = linkBadge(child, file);
				if (link) node.children[i] = link;
				continue;
			}
			if (child.type === 'listItem' && markDefinition(child)) {
				// The defining label itself stays plain. Scan the rest.
				walk(child.children[0], file, 1);
				walk(child, file, 1);
				continue;
			}
			if (child.type === 'text') {
				const replaced = split(child.value, file);
				if (replaced) {
					node.children.splice(i, 1, ...replaced);
					i += replaced.length - 1;
				}
				continue;
			}
			walk(child, file);
		}
	}

	return (/** @type {any} */ tree, /** @type {import('vfile').VFile} */ file) => walk(tree, file);
}

/** The files the hover cards are built from, for the dev server to watch. */
export function refsSources() {
	return [...designFiles().map((rel) => path.join(docsDir, rel)), githubRefsFile];
}

/**
 * A hash of `refsSources`. Passed to the plugin as an option, it becomes
 * part of Astro's config, and Astro clears its content cache when the
 * config changes. Without it, a page that cites SEC06 would keep a stale
 * card after SEC00-index changes.
 */
export function refsDigest() {
	const hash = createHash('sha256');
	for (const file of refsSources()) {
		if (fs.existsSync(file)) hash.update(fs.readFileSync(file));
	}
	return hash.digest('hex');
}
