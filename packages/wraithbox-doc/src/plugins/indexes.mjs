// @ts-check
// Keeps the table in each index (S00-index, FR00-index, X00-index, …) in
// step with what it lists, so nobody maintains them by hand. The table is
// the one that starts with the line `| ID | Description | Status |`, and
// it is rewritten as a whole, one row per item, in number order:
//
//   item                          Description         Status
//   a page (S07, B36, V1, R01)    its title           its status line
//   a definition (FR01, SEC06,    its bold name       see `definitionStatus`
//   T01, X03) in the index
//
// `mise run doc:index` writes the tables (scripts/indexes.mjs). The docs
// build runs the same code without writing and fails when a table is out
// of date (specs.mjs), so a new spec, spike or brief can't be merged
// without its row.
import fs from 'node:fs';
import path from 'node:path';
import { DEFINITION, DESIGN_ID, SLUG, loadGithub } from './remark-refs.mjs';
import { designFiles, docsDir } from './specs.mjs';

const HEADER = '| ID | Description | Status |';

/** @typedef {{ id: string, full: string, description: string, status: string }} Row */

/** Text for one table cell: one line, with pipes escaped. */
function cell(/** @type {string} */ text) {
	return text.replace(/\s+/g, ' ').replace(/\|/g, '\\|').trim();
}

/** The number in an ID, for sorting: S07 is 7, V1-initial is 1. */
function numberOf(/** @type {string} */ id) {
	return Number(id.match(/\d+/)?.[0] ?? 0);
}

/** @param {string} source */
function frontmatter(source, /** @type {string} */ key) {
	return source.match(new RegExp(`^${key}: (.+)$`, 'm'))?.[1].replace(/^"(.*)"$/, '$1');
}

/** A page's title: its heading without the ID, or a brief's frontmatter title. */
function titleOf(/** @type {string} */ rel, /** @type {string} */ source) {
	if (rel.endsWith('.mdx')) return frontmatter(source, 'title') ?? '';
	return source.match(/^# (.+)$/m)?.[1].replace(/^\S+ - /, '') ?? '';
}

/**
 * A page's status: its last `**Status:**` line, or for a brief the bold status
 * line that opens it (`**Awaiting decision.**`).
 */
function statusOf(/** @type {string} */ rel, /** @type {string} */ source) {
	if (rel.endsWith('.mdx')) {
		const body = source.replace(/^---\n[\s\S]*?\n---\n/, '');
		return body.match(/^\*\*([^*]+?)\.?\*\*/m)?.[1] ?? '';
	}
	// The last one: a spec template (S01) has one of its own further up.
	return [...source.matchAll(/^\*\*Status:\*\*\s*(.+)$/gm)].at(-1)?.[1] ?? '';
}

/**
 * The milestones that prove each requirement, from the "Covers" sentence
 * of every milestone definition (`V1-M3-network-floor`). "FR01-drop-in
 * to FR05-parallel-sessions" covers FR01 to FR05.
 * @param {{ rel: string, source: string }[]} docs
 */
function coveredBy(docs) {
	/** @type {Map<string, string[]>} */
	const covers = new Map();
	const id = String.raw`(FR|NFR|SEC)(\d{2,})-${SLUG}`;
	for (const { source } of docs) {
		for (const [, milestone, slug, , rest] of source.matchAll(DEFINITION)) {
			if (!/^V\d+-M\d+$/.test(milestone)) continue;
			const sentence = rest.match(/Covers\s+([\s\S]*?)(?:\.\s|\.$|$)/)?.[1] ?? '';
			for (const m of sentence.matchAll(new RegExp(`${id}(?:\\s+to\\s+${id})?`, 'g'))) {
				const [, kind, from, toKind, to] = m;
				const last = toKind === kind ? Number(to) : Number(from);
				for (let n = Number(from); n <= last; n++) {
					const req = `${kind}${String(n).padStart(from.length, '0')}`;
					covers.set(req, [...(covers.get(req) ?? []), `${milestone}-${slug}`]);
				}
			}
		}
	}
	return covers;
}

/**
 * The rows of one index: the pages next to it with its prefix, and the
 * definitions in it.
 * @param {string} indexRel @param {{ rel: string, source: string }[]} docs
 * @param {ReturnType<typeof loadGithub>} github @param {Map<string, string[]>} covers
 * @returns {Row[]}
 */
function rowsOf(indexRel, docs, github, covers) {
	const kind = path.basename(indexRel).match(/^([A-Z]+)00-/)?.[1] ?? '';
	const dir = path.dirname(indexRel);
	const own = new RegExp(`^${kind}\\d`);
	/** @type {Map<string, Row>} */
	const rows = new Map();
	/** @type {Map<string, { rel: string, source: string }>} */
	const pages = new Map();
	for (const doc of docs) {
		if (path.dirname(doc.rel) !== dir) continue;
		const m = path
			.basename(doc.rel)
			.replace(/\.mdx?$/, '')
			.match(new RegExp(`^(${DESIGN_ID})-(${SLUG})$`));
		if (!m || !own.test(m[1]) || numberOf(m[1]) === 0) continue;
		pages.set(m[1], doc);
		rows.set(m[1], {
			id: m[1],
			full: m[0],
			description: titleOf(doc.rel, doc.source),
			status: statusOf(doc.rel, doc.source),
		});
	}
	const index = docs.find((d) => d.rel === indexRel);
	for (const [, id, slug, name] of index?.source.matchAll(DEFINITION) ?? []) {
		if (!own.test(id)) continue;
		const full = `${id}-${slug}`;
		if (!name) throw new Error(`docs/${indexRel}: ${full} has no name (- **${full}: Name.** …)`);
		const page = pages.get(id);
		rows.set(id, {
			id,
			full,
			description: name,
			status: definitionStatus(kind, id, full, page, github, covers),
		});
	}
	return [...rows.values()].sort((a, b) => numberOf(a.id) - numberOf(b.id));
}

/**
 * The status of a definition. A spike is "Answered" once its result page
 * exists, and otherwise its GitHub issue's state. A requirement shows the
 * milestones that prove it. A threat in the index is a risk accepted
 * rather than solved.
 * @param {string} kind @param {string} id @param {string} full
 * @param {{ rel: string, source: string } | undefined} page
 * @param {ReturnType<typeof loadGithub>} github @param {Map<string, string[]>} covers
 */
function definitionStatus(kind, id, full, page, github, covers) {
	if (kind === 'X') {
		if (page) return statusOf(page.rel, page.source) || 'Answered';
		const issue = Object.values(github).find((r) => r.kind === 'issue' && r.title.startsWith(`${full}:`));
		if (!issue) return 'No issue';
		return issue.state === 'open' ? 'Open' : 'Closed';
	}
	if (kind === 'T') return 'Accepted';
	if (['FR', 'NFR', 'SEC'].includes(kind)) return covers.get(id)?.join(', ') ?? 'No milestone';
	return '';
}

/** @param {Row[]} rows */
function renderTable(rows) {
	return [
		HEADER,
		'|----|-------------|--------|',
		...rows.map((r) => `| ${r.full} | ${cell(r.description)} | ${cell(r.status)} |`),
	].join('\n');
}

/**
 * Rewrites the table of every index. Returns the indexes whose table was
 * out of date; with `write` false, it only reports them.
 * @param {{ write: boolean }} options
 */
export function updateIndexes({ write }) {
	const docs = designFiles().map((rel) => ({
		rel,
		source: fs.readFileSync(path.join(docsDir, rel), 'utf8'),
	}));
	const github = loadGithub();
	const covers = coveredBy(docs);
	/** @type {string[]} */
	const stale = [];
	for (const { rel, source } of docs) {
		if (!/^[A-Z]+00-/.test(path.basename(rel))) continue;
		const lines = source.split('\n');
		const start = lines.indexOf(HEADER);
		if (start < 0) throw new Error(`docs/${rel}: no table (a line "${HEADER}" where it goes)`);
		let end = start;
		while (end < lines.length && lines[end].startsWith('|')) end++;
		const table = renderTable(rowsOf(rel, docs, github, covers));
		const updated = [...lines.slice(0, start), table, ...lines.slice(end)].join('\n');
		if (updated === source) continue;
		stale.push(`docs/${rel}`);
		if (write) fs.writeFileSync(path.join(docsDir, rel), updated);
	}
	return stale;
}
