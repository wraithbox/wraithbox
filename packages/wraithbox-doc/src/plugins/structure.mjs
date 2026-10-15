// @ts-check
// Keeps specs short enough to read and review (S01-spec-based-development,
// "Size and structure"). Each check has one limit:
//
//   page        at most 300 lines
//   section     at most 80 lines from one heading to the next
//   list        at most two levels deep
//   paragraph   at most 120 words, a list item counting as one
//   sentence    at most 40 words
//
// Code blocks and tables are left out of the word counts. The docs build
// fails when a spec in `STRICT` breaks a limit. `mise run doc:structure`
// lists what every spec breaks, so the specs not yet in `STRICT` can be
// brought under the limits one by one.
import fs from 'node:fs';
import path from 'node:path';
import { designFiles, docsDir } from './specs.mjs';

export const LIMITS = { page: 300, section: 80, depth: 2, paragraph: 120, sentence: 40 };

/**
 * The specs held to the limits by the build, by ID. A spec's pages
 * (S07-04) are held with it.
 */
export const STRICT = new Set(['S04', 'S08']);

/** Words in a passage of Markdown, without link targets and markup. */
function words(/** @type {string} */ text) {
	return text
		.replace(/\]\([^)]*\)/g, ']')
		.replace(/[`*_[\]]/g, '')
		.split(/\s+/)
		.filter((w) => /\w/.test(w));
}

/** The sentences of a paragraph: split after `.`, `?` or `!` and a space. */
function sentences(/** @type {string} */ text) {
	return text
		.replace(/\s+/g, ' ')
		.split(/(?<=[.?!])\s+(?=[A-Z`*("])/)
		.filter((s) => s.trim());
}

/**
 * The problems of one document, as `<line>: <message>`.
 * @param {string} source
 */
export function structureOf(source) {
	/** @type {string[]} */
	const problems = [];
	const lines = source.split('\n');
	if (lines.length > LIMITS.page) problems.push(`1: ${lines.length} lines, more than ${LIMITS.page}`);

	let inCode = false;
	let lastHeading = 0;
	/** @type {{ start: number, text: string[] }[]} */
	const blocks = [];
	/** @type {{ start: number, text: string[] } | null} */
	let block = null;
	const flush = () => {
		if (block) blocks.push(block);
		block = null;
	};
	lines.forEach((line, i) => {
		const n = i + 1;
		if (/^\s*```/.test(line)) {
			inCode = !inCode;
			flush();
			return;
		}
		if (inCode) return;
		if (/^#{1,6} /.test(line)) {
			if (n - lastHeading > LIMITS.section) {
				problems.push(`${lastHeading || 1}: ${n - lastHeading} lines without a heading, more than ${LIMITS.section}`);
			}
			lastHeading = n;
			flush();
			return;
		}
		const item = line.match(/^(\s*)(?:[-*+]|\d+\.) /);
		if (item && item[1].length / 2 >= LIMITS.depth) {
			problems.push(`${n}: a list ${item[1].length / 2 + 1} levels deep, more than ${LIMITS.depth}`);
		}
		if (!line.trim() || /^\s*\|/.test(line) || /^\s*!\[/.test(line) || /^\s*</.test(line)) {
			flush();
			return;
		}
		if (item) flush();
		block ??= { start: n, text: [] };
		block.text.push(line);
	});
	flush();
	if (lines.length - lastHeading > LIMITS.section) {
		problems.push(`${lastHeading || 1}: ${lines.length - lastHeading} lines without a heading, more than ${LIMITS.section}`);
	}

	for (const { start, text } of blocks) {
		const joined = text.join(' ').replace(/^\s*(?:[-*+]|\d+\.) /, '');
		const count = words(joined).length;
		if (count > LIMITS.paragraph) problems.push(`${start}: a paragraph of ${count} words, more than ${LIMITS.paragraph}`);
		for (const sentence of sentences(joined)) {
			const length = words(sentence).length;
			if (length > LIMITS.sentence) {
				problems.push(`${start}: a sentence of ${length} words, more than ${LIMITS.sentence}: "${sentence.slice(0, 60)}…"`);
			}
		}
	}
	return problems;
}

/** The spec ID a file belongs to: S07 for `spec/S07-…/S07-04-dns.md`. */
function specOf(/** @type {string} */ rel) {
	return path.basename(rel).match(/^(S\d{2,})/)?.[1];
}

/**
 * The problems of every spec, as `docs/<file>:<line>: <message>`, split
 * into those of the specs in `STRICT` and the others.
 */
export function checkStructure() {
	/** @type {string[]} */
	const strict = [];
	/** @type {string[]} */
	const other = [];
	for (const rel of designFiles(/\.md$/)) {
		if (!rel.startsWith(`spec${path.sep}`)) continue;
		const id = specOf(rel);
		if (!id || id === 'S00') continue;
		const found = structureOf(fs.readFileSync(path.join(docsDir, rel), 'utf8')).map((p) => `docs/${rel}:${p}`);
		(STRICT.has(id) ? strict : other).push(...found);
	}
	return { strict, other };
}
