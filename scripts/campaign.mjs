// @ts-check
// The deterministic layer of a campaign (docs/agents/campaigns.md): keeps
// the ledger, checks the limits, starts one headless wave with its share
// of the budget, counts what the wave cost and opened, and renders the
// campaign's report page. The campaign manager (the `campaign` skill)
// runs it; it never decides what to build.
//
//   mise run campaign start <n> [--hours 12] [--usd 240] [--issues 50] [--prs 50]
//   mise run campaign status <n>
//   mise run campaign wave <n> [--size 4] [--lane any|mac] [--vm-slots 1] [--milestone <m>]
//   mise run campaign report <n> --out <checkout of the train>
//
// The ledger is `.scratch/campaign/<n>/ledger.json` in the checkout it
// runs in. Every command prints one JSON object on stdout, so the manager
// reads a result, not prose. A wave runs as
// `claude -p "/wave ... --campaign <n>" --output-format json
// --max-budget-usd <share>`, whose `total_cost_usd` counts the wave's
// subagents too, at list prices. The dispatcher hands the wave to a
// background lead and ends its turn, so the session must wait for
// background tasks without a ceiling (waveEnv, #609). The script's own
// timeout bounds the wave instead.
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { renderReport } from './campaign-report.mjs';

export const DEFAULT_LIMITS = Object.freeze({ hours: 12, usd: 240, issues: 50, prs: 50 });
/** Share of the dollar limit kept for the manager's session and finishing up. */
export const RESERVE = 0.05;
/** How long a wave may run past `--until` before the script ends it. */
export const GRACE_MS = 60 * 60 * 1000;
const HOUR_MS = 60 * 60 * 1000;

/**
 * @typedef {{ hours: number, usd: number, issues: number, prs: number }} Limits
 * @typedef {{ size: number, usd: number, until: string }} Allowance
 * @typedef {object} Wave
 * @property {number} k
 * @property {string} sessionId
 * @property {string} startedAt
 * @property {string} endedAt
 * @property {Allowance} allowance
 * @property {number} costUsd
 * @property {boolean} costEstimated true when the session gave no cost and the allowance was counted
 * @property {string} ended how the session ended: its `subtype`, `timeout` or `no output`
 * @property {number[]} issues
 * @property {number[]} prs
 * @property {string} result the session's final text, cut short
 * @typedef {{ campaign: number, startedAt: string, limits: Limits, train: string, waves: Wave[] }} Ledger
 */

/**
 * Parses `<command> <n> [--flag value]...`.
 * @param {string[]} argv
 * @returns {{ command: string, campaign: number, flags: Record<string, string> }}
 */
export function parseArgs(argv) {
	const [command, number, ...rest] = argv;
	const campaign = Number(number);
	if (!command || !Number.isInteger(campaign) || campaign <= 0) {
		throw new Error('usage: campaign <start|status|wave|report> <campaign issue> [--flag value]...');
	}
	/** @type {Record<string, string>} */
	const flags = {};
	for (let i = 0; i < rest.length; i += 2) {
		const name = rest[i];
		const value = rest[i + 1];
		if (!name.startsWith('--') || value === undefined) throw new Error(`bad flag near ${JSON.stringify(name)}`);
		flags[name.slice(2)] = value;
	}
	return { command, campaign, flags };
}

/**
 * A positive number from a flag, or the default.
 * @param {Record<string, string>} flags @param {string} name @param {number} fallback
 */
export function numberFlag(flags, name, fallback) {
	if (flags[name] === undefined) return fallback;
	const value = Number(flags[name]);
	if (!Number.isFinite(value) || value <= 0) throw new Error(`--${name} must be a positive number, got ${flags[name]}`);
	return value;
}

/**
 * A new ledger.
 * @param {number} campaign @param {Record<string, string>} flags @param {Date} now
 * @returns {Ledger}
 */
export function newLedger(campaign, flags, now) {
	/** @type {Limits} */
	const limits = {
		hours: numberFlag(flags, 'hours', DEFAULT_LIMITS.hours),
		usd: numberFlag(flags, 'usd', DEFAULT_LIMITS.usd),
		issues: numberFlag(flags, 'issues', DEFAULT_LIMITS.issues),
		prs: numberFlag(flags, 'prs', DEFAULT_LIMITS.prs),
	};
	return { campaign, startedAt: now.toISOString(), limits, train: `train/${campaign}`, waves: [] };
}

/**
 * What the campaign has spent. Issues and pull requests are counted once
 * each, from the waves and from `extraPrs` (the ones GitHub knows).
 * @param {Ledger} ledger @param {Date} now @param {number[]} [extraPrs]
 */
export function spentOf(ledger, now, extraPrs = []) {
	const issues = new Set(ledger.waves.flatMap((w) => w.issues));
	const prs = new Set([...ledger.waves.flatMap((w) => w.prs), ...extraPrs]);
	const usd = ledger.waves.reduce((sum, w) => sum + w.costUsd, 0);
	const hours = (now.getTime() - Date.parse(ledger.startedAt)) / HOUR_MS;
	return { hours, usd, issues: issues.size, prs: prs.size };
}

/**
 * The first limit reached, or null. Dollars count as reached when what is
 * left after the reserve is under one dollar.
 * @param {Limits} limits @param {ReturnType<typeof spentOf>} spent
 */
export function limitReached(limits, spent) {
	if (spent.hours >= limits.hours) return `hours: ${spent.hours.toFixed(1)} of ${limits.hours}`;
	if (limits.usd * (1 - RESERVE) - spent.usd < 1) return `dollars: ${spent.usd.toFixed(2)} of ${limits.usd}`;
	if (spent.issues >= limits.issues) return `issues: ${spent.issues} of ${limits.issues}`;
	if (spent.prs >= limits.prs) return `pull requests: ${spent.prs} of ${limits.prs}`;
	return null;
}

/**
 * What the next wave may use.
 * @param {Ledger} ledger @param {ReturnType<typeof spentOf>} spent @param {number} size
 * @returns {Allowance}
 */
export function allowanceOf(ledger, spent, size) {
	const { limits } = ledger;
	const left = Math.min(limits.issues - spent.issues, limits.prs - spent.prs);
	return {
		size: Math.max(0, Math.min(size, left)),
		usd: Math.floor((limits.usd * (1 - RESERVE) - spent.usd) * 100) / 100,
		until: new Date(Date.parse(ledger.startedAt) + limits.hours * HOUR_MS).toISOString(),
	};
}

/**
 * The `/wave` prompt of a headless wave.
 * @param {Ledger} ledger @param {Allowance} allowance @param {Record<string, string>} flags @param {string} waveFile
 */
export function wavePrompt(ledger, allowance, flags, waveFile) {
	const lane = flags.lane ?? 'any';
	if (lane !== 'any' && lane !== 'mac') throw new Error(`--lane must be any or mac, got ${lane}`);
	const slots = numberFlag(flags, 'vm-slots', 1);
	if (slots > 2) throw new Error('--vm-slots is at most 2 (NFR05-two-macos-vms)');
	const parts = [
		`/wave ${allowance.size}`,
		`--campaign ${ledger.campaign}`,
		`--train ${ledger.train}`,
		`--lane ${lane}`,
		`--vm-slots ${slots}`,
		`--until ${allowance.until}`,
		`--wave-file ${waveFile}`,
	];
	if (flags.milestone) parts.push(`--milestone '${flags.milestone.replace(/'/g, '')}'`);
	return parts.join(' ');
}

/**
 * The `claude` arguments of a headless wave.
 * @param {string} prompt @param {Allowance} allowance @param {string} sessionId
 */
export function claudeArgs(prompt, allowance, sessionId) {
	return [
		'-p',
		prompt,
		'--output-format',
		'json',
		'--max-budget-usd',
		allowance.usd.toFixed(2),
		'--permission-mode',
		'auto',
		'--session-id',
		sessionId,
	];
}

/**
 * The environment of a headless wave. `claude -p` stops background tasks
 * 10 minutes after the last turn unless this ceiling is 0, and the
 * dispatcher's turn ends while its wave-lead runs (#609). With it, the
 * session resumes when the lead reports, and the final result's
 * `total_cost_usd` counts every turn. The wait counts only the
 * dispatcher's own tasks, not the lead's background commands, so the
 * dispatcher keeps a wait of its own running until it writes the wave
 * file (the `wave` skill, "Campaign mode", #631).
 * @param {NodeJS.ProcessEnv} base
 * @returns {NodeJS.ProcessEnv}
 */
export function waveEnv(base) {
	return { ...base, CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS: '0' };
}

/**
 * Reads a headless session's JSON result. A session that was ended, or
 * printed no JSON, is counted at its full dollar allowance.
 * @param {string} stdout @param {boolean} timedOut @param {Allowance} allowance
 * @returns {{ costUsd: number, costEstimated: boolean, ended: string, result: string }}
 */
export function readSession(stdout, timedOut, allowance) {
	/** @type {any} */
	let out = null;
	try {
		out = JSON.parse(stdout.trim());
	} catch {
		out = null;
	}
	const cost = out && typeof out.total_cost_usd === 'number' ? out.total_cost_usd : null;
	const ended = timedOut ? 'timeout' : out ? String(out.subtype ?? 'unknown') : 'no output';
	return {
		costUsd: cost ?? allowance.usd,
		costEstimated: cost === null,
		ended,
		result: String(out?.result ?? '').slice(0, 2000),
	};
}

/**
 * The issues and pull requests a wave wrote to its wave file, or none.
 * @param {string} text
 * @returns {{ issues: number[], prs: number[], missing: boolean }}
 */
export function readWaveFile(text) {
	try {
		const data = JSON.parse(text);
		const numbers = (/** @type {unknown} */ list) =>
			Array.isArray(list) ? list.filter((n) => Number.isInteger(n) && n > 0) : [];
		return { issues: numbers(data.issues), prs: numbers(data.prs), missing: false };
	} catch {
		return { issues: [], prs: [], missing: true };
	}
}

/**
 * The pull requests of a campaign: those whose body has the line
 * `Campaign: #<n>`.
 * @param {{ number: number, body: string }[]} prs @param {number} campaign
 */
export function campaignPrs(prs, campaign) {
	const line = new RegExp(`^Campaign: #${campaign}\\s*$`, 'm');
	return prs.filter((pr) => line.test(pr.body ?? '')).map((pr) => pr.number);
}

// ---- input and output ---------------------------------------------------

/** @param {number} campaign */
function campaignDir(campaign) {
	return path.join(process.cwd(), '.scratch', 'campaign', String(campaign));
}

/** @param {number} campaign @returns {Ledger} */
function loadLedger(campaign) {
	const file = path.join(campaignDir(campaign), 'ledger.json');
	if (!fs.existsSync(file)) throw new Error(`no ledger at ${file}: run "campaign start ${campaign}" first`);
	return JSON.parse(fs.readFileSync(file, 'utf8'));
}

/** @param {Ledger} ledger */
function saveLedger(ledger) {
	const dir = campaignDir(ledger.campaign);
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, 'ledger.json'), `${JSON.stringify(ledger, null, '\t')}\n`);
}

/**
 * Runs gh and parses its JSON output.
 * @param {string[]} args
 */
function gh(args) {
	const run = spawnSync('gh', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
	if (run.status !== 0) throw new Error(`gh ${args.join(' ')}: ${run.stderr.trim()}`);
	return JSON.parse(run.stdout);
}

/** The campaign's pull requests on GitHub, in any state and against any base. */
function githubPrs(/** @type {number} */ campaign) {
	const found = gh([
		'pr', 'list', '--state', 'all', '--limit', '200', '--json', 'number,body',
		'--search', `"Campaign: #${campaign}" in:body`,
	]);
	return campaignPrs(found, campaign);
}

/** @param {Ledger} ledger @param {Date} now */
function status(ledger, now) {
	const spent = spentOf(ledger, now, githubPrs(ledger.campaign));
	return { campaign: ledger.campaign, limits: ledger.limits, spent, stop: limitReached(ledger.limits, spent), waves: ledger.waves.length };
}

/** @param {Ledger} ledger @param {Record<string, string>} flags */
function runWave(ledger, flags) {
	const now = new Date();
	const before = status(ledger, now);
	if (before.stop) return { started: false, stop: before.stop };
	const allowance = allowanceOf(ledger, before.spent, numberFlag(flags, 'size', 4));
	if (allowance.size === 0) return { started: false, stop: 'no issues or pull requests left' };
	const k = ledger.waves.length + 1;
	const waveFile = path.join(campaignDir(ledger.campaign), `wave-${k}.json`);
	const sessionId = randomUUID();
	const prompt = wavePrompt(ledger, allowance, flags, waveFile);
	const timeout = Math.max(60_000, Date.parse(allowance.until) + GRACE_MS - now.getTime());
	const run = spawnSync('claude', claudeArgs(prompt, allowance, sessionId), {
		encoding: 'utf8',
		maxBuffer: 64 * 1024 * 1024,
		timeout,
		env: waveEnv(process.env),
		stdio: ['ignore', 'pipe', 'inherit'],
	});
	// spawnSync ends the session with SIGTERM at the timeout and sets
	// error.code to ETIMEDOUT. Any signal counts as ended.
	const timedOut = /** @type {NodeJS.ErrnoException | undefined} */ (run.error)?.code === 'ETIMEDOUT' || run.signal !== null;
	const session = readSession(run.stdout ?? '', timedOut, allowance);
	const made = readWaveFile(fs.existsSync(waveFile) ? fs.readFileSync(waveFile, 'utf8') : '');
	/** @type {Wave} */
	const wave = {
		k, sessionId, startedAt: now.toISOString(), endedAt: new Date().toISOString(), allowance,
		costUsd: session.costUsd, costEstimated: session.costEstimated, ended: session.ended,
		issues: made.issues, prs: made.prs, result: session.result,
	};
	ledger.waves.push(wave);
	saveLedger(ledger);
	const after = status(ledger, new Date());
	return { started: true, wave: { ...wave, waveFileMissing: made.missing }, spent: after.spent, stop: after.stop };
}

/** @param {Ledger} ledger @param {Record<string, string>} flags */
function writeReport(ledger, flags) {
	if (!flags.out) throw new Error('report needs --out <checkout of the train>');
	const dir = campaignDir(ledger.campaign);
	const narrativeFile = path.join(dir, 'narrative.json');
	if (!fs.existsSync(narrativeFile)) throw new Error(`no narrative at ${narrativeFile}`);
	const narrative = JSON.parse(fs.readFileSync(narrativeFile, 'utf8'));
	const numbers = [...new Set([...ledger.waves.flatMap((w) => w.prs), ...githubPrs(ledger.campaign)])].sort((a, b) => a - b);
	const prs = numbers.map((n) =>
		gh(['pr', 'view', String(n), '--json', 'number,title,state,baseRefName,body,files,reviews']),
	);
	const now = new Date();
	// The train reporter's body, once the campaign has ended
	// (docs/agents/campaigns.md, "The report").
	const bodyFile = path.join(dir, 'report-body.md');
	const body = fs.existsSync(bodyFile) ? fs.readFileSync(bodyFile, 'utf8') : null;
	const page = renderReport({ ledger, narrative, prs, spent: spentOf(ledger, now, numbers), now, body });
	const file = path.join(flags.out, 'docs', 'campaigns', `W${ledger.campaign}-${narrative.slug}.mdx`);
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, page);
	return { written: path.relative(flags.out, file) };
}

/** @param {string[]} argv */
export function main(argv) {
	const { command, campaign, flags } = parseArgs(argv);
	if (command === 'start') {
		const file = path.join(campaignDir(campaign), 'ledger.json');
		if (fs.existsSync(file)) throw new Error(`a ledger exists at ${file}; resume instead of starting again`);
		const ledger = newLedger(campaign, flags, new Date());
		saveLedger(ledger);
		return { started: true, ledger };
	}
	const ledger = loadLedger(campaign);
	if (command === 'status') return status(ledger, new Date());
	if (command === 'wave') return runWave(ledger, flags);
	if (command === 'report') return writeReport(ledger, flags);
	throw new Error(`unknown command ${command}`);
}

if (import.meta.main) {
	try {
		console.log(JSON.stringify(main(process.argv.slice(2)), null, '\t'));
	} catch (err) {
		console.error(`campaign: ${err instanceof Error ? err.message : String(err)}`);
		process.exit(1);
	}
}
