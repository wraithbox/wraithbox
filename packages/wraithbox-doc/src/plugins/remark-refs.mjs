// @ts-check
// Turns references in page text into links with a hover card that shows
// what they point at, so a reader doesn't need to remember numbers:
//
//   S6-repo-writes, or the alias S6   requirement in spec 003
//   X14                               spike in spec 011
//   spec 007, specs 003, 007 and 009  a spec page
//   issue #36, PR #12, or a bare #36  a GitHub issue or pull request
//
// Requirement and spike text comes from the specs at build time. Issue
// and pull request titles come from src/data/github-refs.json, a snapshot
// that `mise run doc:refs` refreshes, so the build stays offline.
//
// A requirement written with the wrong slug fails the build. An unknown
// requirement, spike, spec or issue number is left as plain text, with a
// warning.
//
// The same plugin gives each requirement and spike definition (the list
// items in specs 003 and 011) an `id`, which the links point at.
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { plain, specDir, specUrl } from './specs.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const githubRefsFile = path.resolve(here, '../data/github-refs.json');
const githubRepo = 'https://github.com/wraithbox/wraithbox';

/** @typedef {{ url: string, title: string, description: string }} Target */

/** First sentence of a plain-text passage, at most about 220 characters. */
function firstSentence(/** @type {string} */ text) {
	const sentence = text.match(/^(.+?[.?!])(\s|$)/)?.[1] ?? text;
	return sentence.length > 220 ? `${sentence.slice(0, 217).trimEnd()}…` : sentence;
}

function loadSpecs() {
	/** @type {Map<string, Target & { file: string }>} spec number to page */
	const specs = new Map();
	for (const file of fs.readdirSync(specDir).filter((f) => /^\d{3}-.*\.md$/.test(f))) {
		const source = fs.readFileSync(path.join(specDir, file), 'utf8');
		const title = source.match(/^# (.+)$/m)?.[1] ?? file;
		const purpose = source.match(/^\*\*Purpose:\*\*\s*([\s\S]*?)(?:\n\n|$)/m)?.[1] ?? '';
		specs.set(file.slice(0, 3), { file, url: specUrl(file), title, description: plain(purpose) });
	}
	return specs;
}

function loadRequirements(/** @type {Map<string, {file: string, url: string}>} */ specs) {
	const spec = specs.get('003');
	if (!spec) throw new Error('remark-refs: spec 003 not found');
	const source = fs.readFileSync(path.join(specDir, spec.file), 'utf8');
	/** @type {Map<string, Target & { slug: string }>} ID (S6) to requirement */
	const requirements = new Map();
	const item = /^- \*\*([FSNCR]\d{1,2})-([a-z0-9-]+):\s*([^*]*?)\.?\*\*([\s\S]*?)(?=\n- \*\*|\n\n|(?![\s\S]))/gm;
	for (const m of source.matchAll(item)) {
		const [, id, slug, name, rest] = m;
		requirements.set(id, {
			slug,
			url: `${spec.url}#${id.toLowerCase()}-${slug}`,
			title: name ? `${id}-${slug}: ${name}` : `${id}-${slug}`,
			description: firstSentence(plain(rest)),
		});
	}
	if (requirements.size === 0) throw new Error('remark-refs: no requirements found in spec 003');
	return requirements;
}

function loadSpikes(/** @type {Map<string, {file: string, url: string}>} */ specs) {
	const spec = specs.get('011');
	if (!spec) throw new Error('remark-refs: spec 011 not found');
	const source = fs.readFileSync(path.join(specDir, spec.file), 'utf8');
	/** @type {Map<string, Target>} ID (X14) to spike */
	const spikes = new Map();
	const item = /^\d+\. \*\*(X\d{1,2}) ([^*]+?)\.\*\*([\s\S]*?)(?=\n\d+\. \*\*|\n\n|(?![\s\S]))/gm;
	for (const m of source.matchAll(item)) {
		const [, id, name, rest] = m;
		spikes.set(id, {
			url: `${spec.url}#${id.toLowerCase()}`,
			title: `${id}: ${name}`,
			description: firstSentence(plain(rest)),
		});
	}
	return spikes;
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

/** @param {string} label @param {Target} target */
function refLink(label, target) {
	return {
		type: 'link',
		url: target.url,
		data: { hProperties: { className: ['wb-ref'] } },
		children: [{ type: 'text', value: label }, tip(target.title, target.description)],
	};
}

const REF = new RegExp(
	[
		// 1-3: requirement ID, number, optional slug
		String.raw`\b(([FSNCR])(\d{1,2}))(?:-([a-z0-9]+(?:-[a-z0-9]+)*))?\b`,
		// 5: spike
		String.raw`\b(X\d{1,2})\b`,
		// 6-7: "spec 007" or "specs 003, 007 and 009"
		String.raw`\b(specs?) (\d{3}(?:(?:,| and| or| to)+ \d{3})*)\b`,
		// 8-9: issue or pull request, with or without a word in front
		String.raw`(?:\b(issue|Issue|PR|pull request) )?(?<![\w/&])#(\d+)\b`,
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

/** @param {{ digest?: string }} [_options] only `digest`, see `refsDigest` */
export function remarkRefs(_options) {
	const specs = loadSpecs();
	const requirements = loadRequirements(specs);
	const spikes = loadSpikes(specs);
	const github = loadGithub();

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

	/** Astro doesn't print vfile messages, so warnings go to the console. */
	function warn(/** @type {import('vfile').VFile} */ file, /** @type {string} */ message) {
		console.warn(`[remark-refs] ${path.relative(process.cwd(), file.path ?? '')}: ${message}`);
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
		let changed = false;
		const text = (/** @type {string} */ t) => {
			if (t) out.push({ type: 'text', value: t });
		};
		for (const m of value.matchAll(REF)) {
			const start = /** @type {number} */ (m.index);
			const [whole, id, , , slug, spike, specWord, specList, ghWord, ghNumber] = m;
			/** @type {any[] | null} */
			let nodes = null;
			if (id) {
				const req = requirements.get(id);
				if (!req) {
					warn(file, `unknown requirement ${whole}`);
				} else if (slug && slug !== req.slug) {
					file.fail(`requirement ${whole}: the slug of ${id} is "${req.slug}"`);
				} else {
					nodes = [refLink(`${id}-${req.slug}`, req)];
				}
			} else if (spike) {
				const target = spikes.get(spike);
				if (target) nodes = [refLink(spike, target)];
				else warn(file, `unknown spike ${spike}`);
			} else if (specWord) {
				const numbers = [...specList.matchAll(/\d{3}/g)];
				if (numbers.every((n) => specs.has(n[0]))) {
					if (numbers.length === 1) {
						nodes = [refLink(whole, /** @type {Target} */ (specs.get(numbers[0][0])))];
					} else {
						nodes = [{ type: 'text', value: `${specWord} ` }];
						let at = 0;
						for (const n of numbers) {
							const sep = specList.slice(at, n.index);
							if (sep) nodes.push({ type: 'text', value: sep });
							nodes.push(refLink(n[0], /** @type {Target} */ (specs.get(n[0]))));
							at = /** @type {number} */ (n.index) + 3;
						}
					}
				} else {
					warn(file, `unknown spec in "${whole}"`);
				}
			} else if (ghNumber) {
				const gh = githubTarget(ghNumber);
				if (gh) nodes = [refLink(`${ghWord ?? gh.kind} #${ghNumber}`, gh.target)];
				else warn(file, `#${ghNumber} is not in src/data/github-refs.json (mise run doc:refs)`);
			}
			if (nodes) {
				text(value.slice(last, start));
				out.push(...nodes);
				last = start + whole.length;
				changed = true;
			}
		}
		if (!changed) return null;
		text(value.slice(last));
		return out;
	}

	/** Adds an id to a requirement or spike definition list item. */
	function markDefinition(/** @type {any} */ listItem) {
		const strong = listItem.children?.[0]?.children?.[0];
		if (strong?.type !== 'strong') return false;
		const label = strong.children?.[0]?.value ?? '';
		const def = label.match(/^([FSNCR]\d{1,2}-[a-z0-9-]+):/) ?? label.match(/^(X\d{1,2}) /);
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
