// @ts-check
// Turns references in page text into links with a hover card that shows
// what they point at, so a reader doesn't need to remember numbers:
//
//   S6-repo-writes (alias S6)        requirement in spec 003-requirements
//   X14-flow-attribution (alias X14) spike in spec 011-verification-and-spikes
//   spec 007-egress-gateway          a spec page (alias: spec 007, Spec 007,
//                                    and lists: specs 003, 007 and 009)
//   007-egress-gateway               a spec page, by its full name alone
//   issue #36, PR #12, or a bare #36 a GitHub issue or pull request
//
// An alias is shown in its full form. Requirement, spike and spec text
// comes from the specs at build time. Issue and pull request titles come
// from src/data/github-refs.json, a snapshot that `mise run doc:refs`
// refreshes, so the build stays offline.
//
// A reference written with the wrong slug fails the build. An unknown
// requirement, spike, spec or issue number is left as plain text, with a
// warning, and so is a bare spec number such as "(003, 007)".
//
// A Starlight <Badge> whose text starts with a reference is wrapped in
// the same link. The plugin also gives each requirement and spike
// definition (the list items in specs 003 and 011) an `id`, which the
// links point at.
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { plain, specDir, specUrl } from './specs.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const githubRefsFile = path.resolve(here, '../data/github-refs.json');
const githubRepo = 'https://github.com/wraithbox/wraithbox';

/** @typedef {{ url: string, title: string, description: string }} Target */
/** @typedef {Target & { slug: string, full: string }} Named a target with an ID-slug name */

/** First sentence of a plain-text passage, at most about 220 characters. */
function firstSentence(/** @type {string} */ text) {
	const sentence = text.match(/^(.+?[.?!])(\s|$)/)?.[1] ?? text;
	return sentence.length > 220 ? `${sentence.slice(0, 217).trimEnd()}…` : sentence;
}

/** The `**Purpose:**` paragraph of a spec, as plain text. */
export function purposeOf(/** @type {string} */ source) {
	const m = source.match(/^\*\*Purpose:\*\*\s*([\s\S]*?)(?:\n\n|(?![\s\S]))/m);
	return m ? plain(m[1]) : '';
}

function loadSpecs() {
	/** @type {Map<string, Named & { file: string }>} spec number to page */
	const specs = new Map();
	for (const file of fs.readdirSync(specDir).filter((f) => /^\d{3}-.*\.md$/.test(f))) {
		const source = fs.readFileSync(path.join(specDir, file), 'utf8');
		const name = file.replace(/\.md$/, '');
		specs.set(file.slice(0, 3), {
			file,
			slug: name.slice(4),
			full: name,
			url: specUrl(file),
			title: source.match(/^# (.+)$/m)?.[1] ?? name,
			description: firstSentence(purposeOf(source)),
		});
	}
	return specs;
}

/**
 * Reads the numbered definitions of one spec: requirements in 003
 * (`- **S6-repo-writes: Name.** text`) or spikes in 011
 * (`14. **X14-flow-attribution: Name.** text`).
 * @param {Map<string, {file: string, url: string}>} specs
 * @param {string} number @param {RegExp} item
 */
function loadDefinitions(specs, number, item) {
	const spec = specs.get(number);
	if (!spec) throw new Error(`remark-refs: spec ${number} not found`);
	const source = fs.readFileSync(path.join(specDir, spec.file), 'utf8');
	/** @type {Map<string, Named>} ID (S6, X14) to definition */
	const found = new Map();
	for (const m of source.matchAll(item)) {
		const [, id, slug, name, rest] = m;
		const full = `${id}-${slug}`;
		found.set(id, {
			slug,
			full,
			url: `${spec.url}#${full.toLowerCase()}`,
			title: name ? `${full}: ${name}` : full,
			description: firstSentence(plain(rest)),
		});
	}
	if (found.size === 0) throw new Error(`remark-refs: no definitions found in spec ${number}`);
	return found;
}

function loadGithub() {
	/** @type {Record<string, { kind: 'issue' | 'pr', title: string, state: string, summary: string }>} */
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

const SLUG = String.raw`[a-z0-9]+(?:-[a-z0-9]+)*`;
const SPEC_ITEM = String.raw`\d{3}(?:-${SLUG})?`;
const REF = new RegExp(
	[
		// 1-2: requirement ID and optional slug
		String.raw`\b([FSNCR]\d{1,2})(?:-(${SLUG}))?\b`,
		// 3-4: spike ID and optional slug
		String.raw`\b(X\d{1,2})(?:-(${SLUG}))?\b`,
		// 5-6: "spec 007", "Specs 003, 007 and 009", across line breaks
		String.raw`\b([Ss]pecs?)\s+(${SPEC_ITEM}(?:(?:,|\s+and|\s+or|\s+to)\s+${SPEC_ITEM})*)\b`,
		// 7-8: a spec by its full name alone, "007-egress-gateway"
		String.raw`\b(\d{3})-(${SLUG})\b`,
		// 9-10: issue or pull request, with or without a word in front
		String.raw`(?:\b(issue|Issue|PR|pull request) )?(?<![\w/&])#(\d+)\b`,
		// 11: a bare spec number, only warned about
		String.raw`(?<![\w.#/:-])(0\d\d)(?![\w-])`,
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
	const specs = loadSpecs();
	const requirements = loadDefinitions(
		specs,
		'003',
		/^- \*\*([FSNCR]\d{1,2})-([a-z0-9-]+):\s*([^*]*?)\.?\*\*([\s\S]*?)(?=\n- \*\*|\n\n|(?![\s\S]))/gm,
	);
	const spikes = loadDefinitions(
		specs,
		'011',
		/^\d+\. \*\*(X\d{1,2})-([a-z0-9-]+):\s*([^*]*?)\.?\*\*([\s\S]*?)(?=\n\d+\. \*\*|\n\n|(?![\s\S]))/gm,
	);
	const github = loadGithub();

	/** Astro doesn't print vfile messages, so warnings go to the console. */
	function warn(/** @type {import('vfile').VFile} */ file, /** @type {string} */ message) {
		console.warn(`[remark-refs] ${path.relative(process.cwd(), file.path ?? '')}: ${message}`);
	}

	/**
	 * Resolves an ID with an optional slug against a table of definitions.
	 * @param {Map<string, Named>} table @param {string} id @param {string | undefined} slug
	 * @param {string} kind @param {import('vfile').VFile} file
	 */
	function resolve(table, id, slug, kind, file) {
		const target = table.get(id);
		if (!target) {
			warn(file, `unknown ${kind} ${id}`);
			return null;
		}
		if (slug && slug !== target.slug) {
			file.fail(`${kind} ${id}-${slug}: the slug of ${id} is "${target.slug}"`);
		}
		return target;
	}

	/** @param {string} number */
	function githubTarget(number) {
		const ref = github[number];
		if (!ref) return null;
		const kind = ref.kind === 'pr' ? 'PR' : 'issue';
		return {
			kind,
			target: {
				url: `${githubRepo}/${ref.kind === 'pr' ? 'pull' : 'issues'}/${number}`,
				title: `${kind === 'PR' ? 'PR' : 'Issue'} #${number} (${ref.state}): ${ref.title}`,
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
		const [whole, req, reqSlug, spike, spikeSlug, specWord, specList, nameNum, nameSlug, ghWord, ghNum, bare] =
			m;
		const text = (/** @type {string} */ value) => ({ type: 'text', value });
		if (req) {
			const t = resolve(requirements, req, reqSlug, 'requirement', file);
			return t && [refLink([text(t.full)], t)];
		}
		if (spike) {
			const t = resolve(spikes, spike, spikeSlug, 'spike', file);
			return t && [refLink([text(t.full)], t)];
		}
		if (specWord) {
			const items = [...specList.matchAll(new RegExp(String.raw`(\d{3})(?:-(${SLUG}))?`, 'g'))];
			const targets = items.map((i) => resolve(specs, i[1], i[2], 'spec', file));
			if (targets.some((t) => !t)) return null;
			if (items.length === 1) {
				const t = /** @type {Named} */ (targets[0]);
				return [refLink([text(`${specWord} ${t.full}`)], t)];
			}
			/** @type {any[]} */
			const nodes = [text(`${specWord} `)];
			let at = 0;
			items.forEach((i, n) => {
				const sep = specList.slice(at, i.index);
				if (sep) nodes.push(text(sep.replace(/\s+/g, ' ')));
				const t = /** @type {Named} */ (targets[n]);
				nodes.push(refLink([text(t.full)], t));
				at = /** @type {number} */ (i.index) + i[0].length;
			});
			return nodes;
		}
		if (nameNum) {
			// Only a real spec name: "007-egress-gateway", not "443-something".
			const t = specs.get(nameNum);
			return t && t.slug === nameSlug ? [refLink([text(whole)], t)] : null;
		}
		if (ghNum) {
			const gh = githubTarget(ghNum);
			if (!gh) {
				warn(file, `#${ghNum} is not in src/data/github-refs.json (mise run doc:refs)`);
				return null;
			}
			return [refLink([text(`${ghWord ?? gh.kind} #${ghNum}`)], gh.target)];
		}
		if (bare && specs.has(bare)) {
			warn(file, `bare spec number "${bare}": write spec ${specs.get(bare)?.full}`);
		}
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

	/** Adds an id to a requirement or spike definition list item. */
	function markDefinition(/** @type {any} */ listItem) {
		const strong = listItem.children?.[0]?.children?.[0];
		if (strong?.type !== 'strong') return false;
		const label = strong.children?.[0]?.value ?? '';
		const def = label.match(/^([FSNCRX]\d{1,2}-[a-z0-9-]+):/);
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
		if (only && /^#\d+$/.test(link.children[0].value)) {
			link.children[0].value = `${gh.kind} ${link.children[0].value}`;
		}
		link.data = { ...link.data, hProperties: { className: ['wb-ref'] } };
		link.children.push(tip(gh.target.title, gh.target.description));
	}

	/**
	 * A <Badge text="S6-repo-writes per VM"> becomes a link around the
	 * badge, with the reference in its text written in full.
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

/**
 * A hash of everything the hover cards are built from. Passed to the
 * plugin as an option, it becomes part of Astro's config, and Astro clears
 * its content cache when the config changes. Without it, a page that
 * cites S6 would keep a stale card after spec 003 changes.
 */
export function refsDigest() {
	const hash = createHash('sha256');
	for (const file of fs.readdirSync(specDir).sort()) {
		if (file.endsWith('.md')) hash.update(fs.readFileSync(path.join(specDir, file)));
	}
	if (fs.existsSync(githubRefsFile)) hash.update(fs.readFileSync(githubRefsFile));
	return hash.digest('hex');
}

/** The files `refsDigest` reads, for the dev server to watch. */
export function refsSources() {
	return [
		...fs.readdirSync(specDir).filter((f) => f.endsWith('.md')).map((f) => path.join(specDir, f)),
		githubRefsFile,
	];
}
