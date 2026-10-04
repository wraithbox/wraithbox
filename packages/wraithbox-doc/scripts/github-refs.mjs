// @ts-check
// Writes src/data/github-refs.json: the number, kind, state, title and a
// one-sentence summary of every issue and pull request, for the hover
// cards of src/plugins/remark-refs.mjs. Needs `gh` and the network, so
// it is its own task (`mise run doc:refs`), and the build reads the
// committed snapshot. Refresh it when a page starts citing a new issue.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, '../src/data/github-refs.json');
const repo = 'wraithbox/wraithbox';

/** @param {string} kind */
function list(kind) {
	const json = execFileSync(
		'gh',
		[kind, 'list', '--repo', repo, '--state', 'all', '--limit', '1000', '--json', 'number,title,state,body'],
		{ encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 },
	);
	return /** @type {{ number: number, title: string, state: string, body: string }[]} */ (JSON.parse(json));
}

/** The first prose paragraph of a body, as one plain sentence. */
function summarize(/** @type {string} */ body) {
	const paragraphs = body
		.replace(/<!--[\s\S]*?-->/g, '')
		.split(/\n\s*\n/)
		.map((p) => p.trim())
		// Issue forms put each field's value in its own paragraph: skip the
		// short ones (an ID, a yes/no) to reach the first real sentence.
		.filter((p) => p.length >= 30 && !/^(#|Blocked by|Closes|Refs|Co-Authored-By|Assisted-by|```|- \[)/i.test(p));
	const text = (paragraphs[0] ?? '')
		.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
		.replace(/[`*_>]/g, '')
		.replace(/\s+/g, ' ')
		.trim();
	const sentence = text.match(/^(.+?[.?!])(\s|$)/)?.[1] ?? text;
	return sentence.length > 220 ? `${sentence.slice(0, 217).trimEnd()}…` : sentence;
}

/** @type {Record<string, { kind: string, state: string, title: string, summary: string }>} */
const refs = {};
for (const [kind, items] of [
	['issue', list('issue')],
	['pr', list('pr')],
]) {
	for (const item of /** @type {ReturnType<typeof list>} */ (items)) {
		refs[String(item.number)] = {
			kind: /** @type {string} */ (kind),
			state: item.state.toLowerCase(),
			title: item.title,
			summary: summarize(item.body ?? ''),
		};
	}
}
const sorted = Object.fromEntries(Object.entries(refs).sort(([a], [b]) => Number(a) - Number(b)));
fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(out, `${JSON.stringify(sorted, null, '\t')}\n`);
console.log(`wrote ${Object.keys(sorted).length} issues and pull requests to ${path.relative(process.cwd(), out)}`);
