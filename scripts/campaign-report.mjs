// @ts-check
// Renders a campaign's report page, `docs/campaigns/W<n>-<slug>.mdx`
// (docs/agents/campaigns.md, "The report"), from the ledger, the
// campaign's pull requests as `gh pr view` gives them, and the narrative
// the campaign manager writes. Pure: campaign.mjs does the reading and
// writing, and campaign.test.mjs feeds it fixtures.
//
// The narrative is JSON:
//
//   slug, title, description   the page's file name and frontmatter
//   ended                      null while running, else what ended it
//   trainPr                    the train pull request's number
//   summary                    Markdown, a few sentences
//   choices, waiting, attended Markdown list items, one string each
//   followUps                  issues filed as follow-ups, a count
//   flaky                      flaky-test issues filed, numbers
//
// At the end of a campaign the `train-reporter` agent writes the page's
// body, `.scratch/campaign/<n>/report-body.md`: what the train changes,
// by content (docs/agents/campaigns.md, "The report"). With a body, the
// page is the status line, the body, and the figures below folded into
// "The run" at the bottom. Without one, the page is the running layout.
//
// Pull requests and issues are written `PR<n>` and `I<n>`, and specs as
// bare IDs (`S07-15`), so the docs site links them with a hover card. A
// bare ID can't fail the build on a renamed slug, and an unknown one
// only warns.

/**
 * @typedef {object} Narrative
 * @property {string} slug
 * @property {string} title
 * @property {string} description
 * @property {string | null} ended
 * @property {number} trainPr
 * @property {string} summary
 * @property {string[]} choices
 * @property {string[]} waiting
 * @property {string[]} attended
 * @property {number} followUps
 * @property {number[]} flaky
 * @typedef {object} Pr
 * @property {number} number
 * @property {string} title
 * @property {string} state MERGED, OPEN or CLOSED
 * @property {string} baseRefName
 * @property {string} body
 * @property {{ path: string }[]} files
 * @property {{ body: string }[]} reviews
 */

/** Text for MDX prose or a table cell: one line, no JSX or pipes. */
export function escape(/** @type {string} */ text) {
	return String(text)
		.replace(/\s+/g, ' ')
		.trim()
		.replace(/[{}<>|]/g, (c) => `\\${c}`);
}

/** The issues a pull request body closes. */
export function closes(/** @type {string} */ body) {
	return [...String(body ?? '').matchAll(/\b(?:Closes|Fixes|Resolves) #(\d+)/gi)].map((m) => Number(m[1]));
}

/** Where a pull request is, in the report's words. */
export function placeOf(/** @type {Pr} */ pr) {
	const onTrain = pr.baseRefName.startsWith('train/');
	if (pr.state === 'MERGED') return onTrain ? 'on the train' : `merged into ${pr.baseRefName}`;
	if (pr.state === 'OPEN') return onTrain ? 'open' : `waiting for you (against ${pr.baseRefName})`;
	return 'closed';
}

/** The number of code review rounds, from `Code review, round <k>`. */
export function roundsOf(/** @type {Pr} */ pr) {
	const rounds = pr.reviews.map((r) => Number(r.body.match(/^Code review, round (\d+)/)?.[1] ?? 0));
	return Math.max(0, ...rounds);
}

/** Whether the first code review approved. */
export function firstApproved(/** @type {Pr} */ pr) {
	const first = pr.reviews.find((r) => r.body.startsWith('Code review'));
	if (!first) return null;
	return [...first.body.matchAll(/Verdict: ([a-z ]+)/g)].at(-1)?.[1].trim() === 'approve';
}

/**
 * The spec pages, contracts and dependency manifests a pull request
 * changes: `S07-15` for docs/spec/S07-egress-gateway/S07-15-….md, and the
 * file name for a .proto, a go.mod or a Package.swift.
 */
export function specsOf(/** @type {Pr} */ pr) {
	const found = new Set();
	for (const { path } of pr.files) {
		const spec = path.match(/^docs\/spec\/(?:[^/]+\/)?(S\d{2,}(?:-\d{2})?)-[^/]+\.mdx?$/);
		if (spec) found.add(spec[1].replace(/-00$/, ''));
		else if (/(\.proto|(^|\/)go\.mod|(^|\/)Package\.swift)$/.test(path)) found.add(`\`${path.split('/').at(-1)}\``);
	}
	return [...found].sort();
}

/** @param {number} n */
function money(n) {
	return `$${n.toFixed(2)}`;
}

/** @param {string[]} items @param {string} empty */
function list(items, empty) {
	if (!items.length) return empty;
	return items.map((item) => `- ${item.replace(/\s*\n\s*/g, ' ').trim()}`).join('\n');
}

/**
 * @param {import('./campaign.mjs').Ledger} ledger
 * @param {{ hours: number, usd: number, issues: number, prs: number }} spent
 */
function budgetTable(ledger, spent) {
	const { limits } = ledger;
	const row = (/** @type {string} */ name, /** @type {string} */ used, /** @type {string} */ limit) =>
		`| ${name} | ${used} | ${limit} |`;
	return [
		'| Limit | Spent | Of |',
		'|---|---|---|',
		row('Hours', spent.hours.toFixed(1), String(limits.hours)),
		row('Dollars', money(spent.usd), money(limits.usd)),
		row('Issues', String(spent.issues), String(limits.issues)),
		row('Pull requests', String(spent.prs), String(limits.prs)),
	].join('\n');
}

/** @param {Pr[]} prs */
function prTable(prs) {
	if (!prs.length) return 'No pull requests yet.';
	const rows = prs.map((pr) => {
		const issues = closes(pr.body).map((n) => `I${n}`).join(', ') || 'none';
		const specs = specsOf(pr).join(', ') || 'none';
		return `| PR${pr.number} | ${issues} | ${escape(pr.title)} | ${placeOf(pr)} | ${roundsOf(pr)} | ${specs} | ${pr.files.length} |`;
	});
	return ['| Pull request | Closes | Title | Where | Rounds | Specs and contracts | Files |', '|---|---|---|---|---|---|---|', ...rows].join('\n');
}

/** @param {import('./campaign.mjs').Wave[]} waves */
function waveTable(waves) {
	if (!waves.length) return 'No waves yet.';
	const rows = waves.map((w) => {
		const minutes = Math.round((Date.parse(w.endedAt) - Date.parse(w.startedAt)) / 60000);
		const cost = w.costEstimated ? `${money(w.costUsd)} (its share, no cost reported)` : money(w.costUsd);
		return `| ${w.k} | ${minutes} min | ${cost} | ${w.issues.length} | ${w.prs.length} | ${escape(w.ended)} |`;
	});
	return ['| Wave | Length | Cost | Issues | Pull requests | Session ended |', '|---|---|---|---|---|---|', ...rows].join('\n');
}

/** @param {Pr[]} prs @param {Narrative} narrative */
function healthList(prs, narrative) {
	const reviewed = prs.map(firstApproved).filter((v) => v !== null);
	const approved = reviewed.filter(Boolean).length;
	const rounds = prs.map(roundsOf).filter((r) => r > 0);
	const mean = rounds.length ? (rounds.reduce((a, b) => a + b, 0) / rounds.length).toFixed(1) : 'none';
	const flaky = narrative.flaky.length ? narrative.flaky.map((n) => `I${n}`).join(', ') : 'none';
	return [
		`- First code review approved: ${approved} of ${reviewed.length} pull requests.`,
		`- Code review rounds per reviewed pull request: ${mean}.`,
		`- Follow-ups filed: ${narrative.followUps}.`,
		`- Flaky tests filed: ${flaky}.`,
		"- The campaign manager's own session isn't metered. The 5 percent reserve of the dollar limit pays for it.",
	].join('\n');
}

/**
 * The report page.
 * @param {{ ledger: import('./campaign.mjs').Ledger, narrative: Narrative, prs: Pr[],
 *   spent: { hours: number, usd: number, issues: number, prs: number }, now: Date,
 *   body?: string | null }} data
 */
export function renderReport({ ledger, narrative, prs, spent, now, body = null }) {
	const day = now.toISOString().slice(0, 10);
	const status = narrative.ended ? `Ended ${day}: ${escape(narrative.ended)}` : 'Running';
	const sorted = [...prs].sort((a, b) => a.number - b.number);
	// The first render comes before the train pull request exists, and
	// `PR0` would fail the docs build (a one-digit ID).
	const trainPr = narrative.trainPr > 0 ? `PR${narrative.trainPr}` : 'its pull request, not opened yet';
	if (body?.trim()) return bodyReport({ ledger, narrative, sorted, spent, status, trainPr, body });
	return `---
title: ${JSON.stringify(narrative.title)}
description: ${JSON.stringify(narrative.description)}
---

**${status}.** Campaign I${ledger.campaign}, train \`${ledger.train}\` in ${trainPr}, started ${ledger.startedAt.slice(0, 16).replace('T', ' ')} UTC. Rendered by \`scripts/campaign.mjs\` (docs/agents/campaigns.md).

## Summary

${narrative.summary.trim()}

## Budget

${budgetTable(ledger, spent)}

## Pull requests

${prTable(sorted)}

## Choices to veto

To veto a pull request on the train, comment \`veto #<pr>\` on ${trainPr}.

${list(narrative.choices, 'None.')}

## Waiting for you

${list(narrative.waiting, 'Nothing.')}

## Attended queue

${list(narrative.attended, 'Nothing.')}

## Health

${healthList(sorted, narrative)}

${waveTable(ledger.waves)}
`;
}

/**
 * The page once the train reporter has written its body: the content
 * first, and the figures the script counts folded at the bottom.
 * @param {{ ledger: import('./campaign.mjs').Ledger, narrative: Narrative, sorted: Pr[],
 *   spent: { hours: number, usd: number, issues: number, prs: number }, status: string,
 *   trainPr: string, body: string }} data
 */
function bodyReport({ ledger, narrative, sorted, spent, status, trainPr, body }) {
	return `---
title: ${JSON.stringify(narrative.title)}
description: ${JSON.stringify(narrative.description)}
---

**${status}.** Train \`${ledger.train}\` in ${trainPr}. To veto a pull request on the train, comment \`veto #<pr>\` on ${trainPr}.

${body.trim()}

## The run

<details>
<summary>Budget, pull requests, and waves</summary>

Campaign I${ledger.campaign}, started ${ledger.startedAt.slice(0, 16).replace('T', ' ')} UTC. Rendered by \`scripts/campaign.mjs\` (docs/agents/campaigns.md), with the body above from the \`train-reporter\` agent.

${budgetTable(ledger, spent)}

${prTable(sorted)}

${healthList(sorted, narrative)}

${waveTable(ledger.waves)}

</details>
`;
}
