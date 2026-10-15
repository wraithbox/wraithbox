// @ts-check
// Checks the references to a section of a design document, which are
// plain text and so not checked by the reference links (remark-refs.mjs):
//
//   S06-vm-lifecycle, "Disks"           a section of another document
//   ("Packet path", DNS)                a section of the same document
//   ("The device set is fixed", below)
//
// A section is a heading, or the bold or italic lead of a list item or
// paragraph (`- **Disks.**`, `- *Transport.*`). Names compare without
// case, Markdown, and a trailing period or colon. The docs build fails on
// a reference that names no section of its document, so splitting a spec
// into pages (S01-spec-based-development, "Spec tree") can't leave a
// reference behind that points at nothing.
import fs from 'node:fs';
import path from 'node:path';
import { DESIGN_ID, SLUG } from './remark-refs.mjs';
import { designFiles, docsDir } from './specs.mjs';

/** A section name in comparable form. */
export function sectionKey(/** @type {string} */ name) {
	return name
		.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
		.replace(/[`*_]/g, '')
		.replace(/\s+/g, ' ')
		.replace(/[.:]$/, '')
		.trim()
		.toLowerCase();
}

/** The section names of one document. */
export function sectionsOf(/** @type {string} */ source) {
	/** @type {Set<string>} */
	const names = new Set();
	const text = source.replace(/^```[\s\S]*?^```/gm, '');
	const add = (/** @type {string} */ name) => {
		const key = sectionKey(name);
		names.add(key);
		// "Approvals (SEC14-no-fake-approvals)" is also "Approvals", and
		// "Layer 1: process hardening" is also "Layer 1".
		names.add(sectionKey(key.replace(/\s*\([^)]*\)$/, '')));
		names.add(sectionKey(key.replace(/:.*$/, '')));
	};
	for (const [, heading] of text.matchAll(/^#{1,6} (.+)$/gm)) add(heading);
	// The lead of a list item or paragraph, or the first cell of a table row.
	for (const [, bold, italic] of text.matchAll(/^\s*(?:- |\| )?(?:\*\*([^*\n]+?)\*\*|\*([^*\n]+?)\*)/gm)) {
		add(bold ?? italic);
	}
	return names;
}

const QUOTED = String.raw`"([^"\n]{1,80})"`;

/**
 * The patterns, built on first use: remark-refs.mjs imports specs.mjs,
 * which imports this file, so DESIGN_ID isn't set yet when it loads.
 */
function patterns() {
	return {
		/**
		 * `S06-vm-lifecycle, "Disks"`, `… "Disks" and "Devices"`, and
		 * `S07-egress-gateway ("DNS", "Connections")`.
		 */
		cross: new RegExp(
			String.raw`\b(${DESIGN_ID})(?:-${SLUG})?(?:,\s+|\s+\(\s*)${QUOTED}((?:,?\s+(?:and\s+|or\s+)?${QUOTED})*)`,
			'g',
		),
		/**
		 * `("Packet path", DNS)` and `"The device set is fixed", below`, in a
		 * spec. A quoted message or value starts in lowercase or isn't
		 * followed by `)`, a comma or "below", and is left alone.
		 */
		local: [
			new RegExp(String.raw`\(${QUOTED}(?=[,)])`, 'g'),
			new RegExp(String.raw`${QUOTED},?\s+(?:below|above)\b`, 'g'),
		],
		/** What comes before a cross reference that starts with `("`. */
		crossBefore: new RegExp(`(?:${DESIGN_ID})(?:-${SLUG})?(?:,\\s*\\(?|\\s*)$`),
	};
}

/**
 * Whether a document has a section of this name. The start of a longer
 * name also counts, up to a comma, colon or parenthesis: "Modes" names
 * "Modes, per host", and "Everything in the guest is untrusted" names
 * "Everything in the guest is untrusted, including `wb-guestd`".
 */
function has(/** @type {Set<string>} */ sections, /** @type {string} */ name) {
	const key = sectionKey(name);
	if (sections.has(key)) return true;
	return [...sections].some((s) => s.startsWith(key) && /^(?:,|:| \()/.test(s.slice(key.length)));
}

/** The line number of a character offset. */
function lineOf(/** @type {string} */ source, /** @type {number} */ offset) {
	return source.slice(0, offset).split('\n').length;
}

/**
 * Every section reference that names no section of its document, as
 * `docs/<file>:<line>: <message>`.
 */
export function checkSections() {
	const docs = designFiles().map((rel) => ({
		rel,
		source: fs.readFileSync(path.join(docsDir, rel), 'utf8'),
	}));
	/** @type {Map<string, Set<string>>} ID to its page's sections */
	const byId = new Map();
	for (const { rel, source } of docs) {
		const id = path.basename(rel).match(new RegExp(`^(${DESIGN_ID})-${SLUG}\\.mdx?$`))?.[1];
		if (id) byId.set(id, sectionsOf(source));
	}
	const { cross, local, crossBefore } = patterns();
	/** @type {string[]} */
	const problems = [];
	for (const { rel, source } of docs) {
		// Code isn't prose: an example of a reference in backticks isn't
		// one. Blanking the code keeps the offsets. A section name with
		// code in it ("When `wb-hostd` stops") keeps its backticks.
		const text = source
			.replace(/^```[\s\S]*?^```/gm, (m) => m.replace(/[^\n]/g, ' '))
			.replace(/`[^`\n]*"[^`\n]*`/g, (m) => ' '.repeat(m.length));
		const own = sectionsOf(source);
		for (const m of text.matchAll(cross)) {
			const sections = byId.get(m[1]);
			// A version without its slug is a name in prose ("in V1").
			if (!sections || /^V\d+$/.test(m[1])) continue;
			const after = text.slice((m.index ?? 0) + m[0].length);
			const names = [m[2], ...[...(m[3] ?? '').matchAll(new RegExp(QUOTED, 'g'))].map((n) => n[1])];
			names.forEach((name, i) => {
				// `(B35-openshell-artifacts, "Approvals" below)`: the last name,
				// followed by "below" or "above", is a section of this document.
				const local = i === names.length - 1 && /^\s+(?:below|above)\b/.test(after);
				if (!has(local ? own : sections, name)) {
					const where = local ? 'this document' : m[1];
					problems.push(`docs/${rel}:${lineOf(source, m.index ?? 0)}: ${where} has no section "${name}"`);
				}
			});
		}
		if (!rel.startsWith(`spec${path.sep}`)) continue;
		for (const re of local) {
			for (const m of text.matchAll(re)) {
				if (!/^[A-Z`]/.test(m[1])) continue;
				// A cross reference can also start with `("`.
				const before = text.slice(Math.max(0, (m.index ?? 0) - 40), m.index);
				if (crossBefore.test(before)) continue;
				if (!has(own, m[1])) {
					problems.push(`docs/${rel}:${lineOf(source, m.index ?? 0)}: no section "${m[1]}" in this document`);
				}
			}
		}
	}
	// Both forms of a local reference can match the same text.
	return [...new Set(problems)];
}
