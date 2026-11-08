// @ts-check
// Tests for campaign.mjs and campaign-report.mjs. They call the pure
// functions only: nothing runs claude or gh. Run with
// `mise run scripts:test`.
import { describe, expect, test } from 'bun:test';
import { closes, escape, firstApproved, placeOf, renderReport, roundsOf, specsOf } from './campaign-report.mjs';
import {
	allowanceOf,
	campaignPrs,
	claudeArgs,
	DEFAULT_LIMITS,
	limitReached,
	newLedger,
	numberFlag,
	parseArgs,
	readSession,
	readWaveFile,
	spentOf,
	wavePrompt,
} from './campaign.mjs';

const start = new Date('2026-11-08T08:00:00Z');
const hoursLater = (/** @type {number} */ h) => new Date(start.getTime() + h * 3600_000);

/** A wave entry with defaults. @param {Partial<import('./campaign.mjs').Wave>} over */
function wave(over) {
	return {
		k: 1,
		sessionId: 's',
		startedAt: start.toISOString(),
		endedAt: hoursLater(1).toISOString(),
		allowance: { size: 4, usd: 228, until: hoursLater(12).toISOString() },
		costUsd: 0,
		costEstimated: false,
		ended: 'success',
		issues: [],
		prs: [],
		result: '',
		...over,
	};
}

describe('parseArgs', () => {
	test('reads the command, the campaign and the flags', () => {
		expect(parseArgs(['wave', '602', '--size', '5', '--lane', 'mac'])).toEqual({
			command: 'wave',
			campaign: 602,
			flags: { size: '5', lane: 'mac' },
		});
	});
	test('refuses a missing or bad campaign number', () => {
		expect(() => parseArgs(['wave'])).toThrow(/usage/);
		expect(() => parseArgs(['wave', 'x'])).toThrow(/usage/);
		expect(() => parseArgs(['wave', '-3'])).toThrow(/usage/);
	});
	test('refuses a flag without a value', () => {
		expect(() => parseArgs(['wave', '602', '--size'])).toThrow(/bad flag/);
		expect(() => parseArgs(['wave', '602', 'size', '4'])).toThrow(/bad flag/);
	});
});

describe('limits', () => {
	test('defaults are 12 hours, 240 dollars, 50 issues and 50 pull requests', () => {
		expect(newLedger(602, {}, start).limits).toEqual({ ...DEFAULT_LIMITS });
		expect(DEFAULT_LIMITS).toEqual({ hours: 12, usd: 240, issues: 50, prs: 50 });
	});
	test('flags override the defaults and must be positive', () => {
		expect(newLedger(602, { hours: '4', usd: '50' }, start).limits).toEqual({ hours: 4, usd: 50, issues: 50, prs: 50 });
		expect(() => numberFlag({ usd: '0' }, 'usd', 1)).toThrow(/positive/);
		expect(() => numberFlag({ usd: 'lots' }, 'usd', 1)).toThrow(/positive/);
	});
	test('counts issues and pull requests once each, including ones GitHub knows', () => {
		const ledger = newLedger(602, {}, start);
		ledger.waves.push(wave({ issues: [1, 2], prs: [10], costUsd: 20 }), wave({ k: 2, issues: [2, 3], prs: [10, 11], costUsd: 5.5 }));
		expect(spentOf(ledger, hoursLater(2), [11, 12])).toEqual({ hours: 2, usd: 25.5, issues: 3, prs: 3 });
	});
	test('names the first limit reached', () => {
		const limits = { ...DEFAULT_LIMITS };
		const none = { hours: 1, usd: 10, issues: 1, prs: 1 };
		expect(limitReached(limits, none)).toBeNull();
		expect(limitReached(limits, { ...none, hours: 12 })).toMatch(/^hours/);
		expect(limitReached(limits, { ...none, issues: 50 })).toMatch(/^issues/);
		expect(limitReached(limits, { ...none, prs: 50 })).toMatch(/^pull requests/);
	});
	test('dollars are reached when less than a dollar is left above the reserve', () => {
		const limits = { ...DEFAULT_LIMITS };
		const spent = { hours: 1, usd: 0, issues: 0, prs: 0 };
		expect(limitReached(limits, { ...spent, usd: 226.9 })).toBeNull();
		expect(limitReached(limits, { ...spent, usd: 227.1 })).toMatch(/^dollars/);
	});
});

describe('allowanceOf', () => {
	test('gives what is left, keeping the reserve', () => {
		const ledger = newLedger(602, {}, start);
		const a = allowanceOf(ledger, { hours: 3, usd: 100, issues: 10, prs: 12 }, 6);
		expect(a).toEqual({ size: 6, usd: 128, until: hoursLater(12).toISOString() });
	});
	test('caps the size by the issues and pull requests left', () => {
		const ledger = newLedger(602, {}, start);
		expect(allowanceOf(ledger, { hours: 0, usd: 0, issues: 48, prs: 10 }, 6).size).toBe(2);
		expect(allowanceOf(ledger, { hours: 0, usd: 0, issues: 10, prs: 49 }, 6).size).toBe(1);
		expect(allowanceOf(ledger, { hours: 0, usd: 0, issues: 50, prs: 10 }, 6).size).toBe(0);
	});
});

describe('the headless wave', () => {
	const ledger = newLedger(602, {}, start);
	const allowance = { size: 4, usd: 128.5, until: hoursLater(12).toISOString() };
	test('the prompt carries the campaign, the train and the allowance', () => {
		const prompt = wavePrompt(ledger, allowance, { lane: 'mac', 'vm-slots': '2', milestone: "V1-M4-it's" }, '/w/wave-1.json');
		expect(prompt).toBe(
			`/wave 4 --campaign 602 --train train/602 --lane mac --vm-slots 2 --until ${allowance.until} --wave-file /w/wave-1.json --milestone 'V1-M4-its'`,
		);
	});
	test('refuses an unknown lane and more than two VM slots', () => {
		expect(() => wavePrompt(ledger, allowance, { lane: 'host' }, 'f')).toThrow(/lane/);
		expect(() => wavePrompt(ledger, allowance, { 'vm-slots': '3' }, 'f')).toThrow(/at most 2/);
	});
	test('claude gets the dollar share and a session ID', () => {
		const args = claudeArgs('/wave 4', allowance, 'abc');
		expect(args.slice(args.indexOf('--max-budget-usd'), args.indexOf('--max-budget-usd') + 2)).toEqual(['--max-budget-usd', '128.50']);
		expect(args).toContain('--session-id');
		expect(args[0]).toBe('-p');
	});
});

describe('readSession', () => {
	const allowance = { size: 4, usd: 50, until: '' };
	test('reads the cost and how the session ended', () => {
		const out = JSON.stringify({ total_cost_usd: 12.25, subtype: 'success', result: 'done' });
		expect(readSession(out, false, allowance)).toEqual({ costUsd: 12.25, costEstimated: false, ended: 'success', result: 'done' });
	});
	test('a session that hit its budget still reports its cost', () => {
		const out = JSON.stringify({ total_cost_usd: 50.1, subtype: 'error_max_budget_usd' });
		expect(readSession(out, false, allowance)).toMatchObject({ costUsd: 50.1, ended: 'error_max_budget_usd' });
	});
	test('an ended or garbled session counts at its full share', () => {
		expect(readSession('', true, allowance)).toMatchObject({ costUsd: 50, costEstimated: true, ended: 'timeout' });
		expect(readSession('not json', false, allowance)).toMatchObject({ costUsd: 50, costEstimated: true, ended: 'no output' });
		expect(readSession('{"subtype":"success"}', false, allowance)).toMatchObject({ costUsd: 50, costEstimated: true });
	});
});

describe('readWaveFile and campaignPrs', () => {
	test('keeps only positive whole numbers', () => {
		expect(readWaveFile('{"issues":[1,"2",-3,4.5,5],"prs":[9]}')).toEqual({ issues: [1, 5], prs: [9], missing: false });
	});
	test('a missing or broken file counts nothing and says so', () => {
		expect(readWaveFile('')).toEqual({ issues: [], prs: [], missing: true });
	});
	test('matches the Campaign line exactly', () => {
		const prs = [
			{ number: 1, body: 'x\nCampaign: #602\n' },
			{ number: 2, body: 'Campaign: #6021' },
			{ number: 3, body: 'see Campaign: #602 elsewhere' },
			{ number: 4, body: 'Campaign: #602' },
		];
		expect(campaignPrs(prs, 602)).toEqual([1, 4]);
	});
});

describe('report helpers', () => {
	/** @param {Partial<import('./campaign-report.mjs').Pr>} over */
	const pr = (over) => ({ number: 7, title: 't', state: 'MERGED', baseRefName: 'train/602', body: '', files: [], reviews: [], ...over });
	test('escape removes JSX and table breakers', () => {
		expect(escape('a <b> {c} | d\n e')).toBe('a \\<b\\> \\{c\\} \\| d e');
	});
	test('closes reads the closing keywords', () => {
		expect(closes('Closes #12\nfixes #13, Refs #14')).toEqual([12, 13]);
	});
	test('placeOf names where a pull request is', () => {
		expect(placeOf(pr({}))).toBe('on the train');
		expect(placeOf(pr({ state: 'OPEN' }))).toBe('open');
		expect(placeOf(pr({ state: 'OPEN', baseRefName: 'main' }))).toBe('waiting for you (against main)');
		expect(placeOf(pr({ state: 'CLOSED' }))).toBe('closed');
	});
	test('rounds and the first verdict come from the code reviews', () => {
		const reviews = [
			{ body: 'Code review, round 1\n...\nVerdict: needs changes' },
			{ body: 'Security review, round 1\nVerdict: approve' },
			{ body: 'Code review, round 2\nVerdict: approve' },
		];
		expect(roundsOf(pr({ reviews }))).toBe(2);
		expect(firstApproved(pr({ reviews }))).toBe(false);
		expect(firstApproved(pr({}))).toBeNull();
	});
	test('specsOf names spec pages by bare ID and contracts by file', () => {
		const files = [
			{ path: 'docs/spec/S07-egress-gateway/S07-15-allowed-streams.md' },
			{ path: 'docs/spec/S07-egress-gateway/S07-00-egress-gateway.md' },
			{ path: 'docs/spec/S03-requirements.md' },
			{ path: 'packages/wraithbox-go/proto/netd.proto' },
			{ path: 'packages/wraithbox-go/internal/x.go' },
			{ path: 'packages/wraithbox-go/go.mod' },
			{ path: 'packages/wraithbox-go/go.sum' },
		];
		expect(specsOf(pr({ files }))).toEqual(['S03', 'S07', 'S07-15', '`go.mod`', '`netd.proto`']);
	});
});

describe('renderReport', () => {
	const ledger = newLedger(602, {}, start);
	ledger.waves.push(wave({ issues: [5], prs: [7], costUsd: 30, ended: 'success' }));
	const narrative = {
		slug: 'network-floor',
		title: 'Campaign 2026-11-08: network floor',
		description: 'Seven pull requests on the train.',
		ended: null,
		trainPr: 700,
		summary: 'One wave.',
		choices: ['PR7 keeps the old timeout.'],
		waiting: [],
		attended: [],
		followUps: 2,
		flaky: [801],
	};
	const prs = [
		{ number: 7, title: 'feat(netd): a <tricky> {title}', state: 'MERGED', baseRefName: 'train/602', body: 'Closes #5', files: [{ path: 'a.go' }], reviews: [{ body: 'Code review, round 1\nVerdict: approve' }] },
	];
	const page = renderReport({ ledger, narrative, prs, spent: spentOf(ledger, hoursLater(2)), now: hoursLater(2) });
	test('has frontmatter and a bold status line first', () => {
		expect(page.startsWith('---\ntitle: "Campaign 2026-11-08: network floor"\n')).toBe(true);
		expect(page).toContain('\n**Running.** Campaign I602, train `train/602` in PR700');
	});
	test('lists the pull request with its issue, place and rounds, escaped', () => {
		expect(page).toContain('| PR7 | I5 | feat(netd): a \\<tricky\\> \\{title\\} | on the train | 1 | none | 1 |');
	});
	test('shows the budget and the health figures', () => {
		expect(page).toContain('| Dollars | $30.00 | $240.00 |');
		expect(page).toContain('First code review approved: 1 of 1 pull requests.');
		expect(page).toContain('Flaky tests filed: I801.');
		expect(page).toContain('Nothing.');
	});
	test('before the train pull request exists, names none', () => {
		const first = renderReport({ ledger, narrative: { ...narrative, trainPr: 0 }, prs: [], spent: spentOf(ledger, start), now: start });
		expect(first).not.toContain('PR0');
		expect(first).toContain('in its pull request, not opened yet');
		expect(first).toContain('No pull requests yet.');
	});
	test('an ended campaign says what ended it', () => {
		const ended = renderReport({ ledger, narrative: { ...narrative, ended: 'dollars: 229.00 of 240' }, prs, spent: spentOf(ledger, hoursLater(2)), now: hoursLater(2) });
		expect(ended).toContain('**Ended 2026-11-08: dollars: 229.00 of 240.**');
	});
});
