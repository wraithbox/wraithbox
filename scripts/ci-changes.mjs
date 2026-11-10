// @ts-check
// Decides which jobs of .github/workflows/ci.yml a run needs (#58), and
// checks afterwards that every job either passed or was skipped for that
// reason. `mise run ci` is the full local gate and does not use this.
//
//   bun scripts/ci-changes.mjs detect    # in the `changes` job
//   bun scripts/ci-changes.mjs verdict   # in the `CI result` job
//
// `detect` reads GITHUB_EVENT_NAME. A pull request runs a job only when a
// file it reads changed: the diff is the pull request's merge commit
// against its first parent, the base branch. Every other event (a push to
// main, workflow_dispatch) runs everything. It prints one line per file
// with the rule that matched it, and writes `go`, `swift`, `bundle` and
// `docs` (`true` or `false`) to GITHUB_OUTPUT. The Swift job runs when
// `swift` or `bundle` is true. `swift` runs all of it, and `bundle` alone
// runs only the build, sign and bundle steps (#354).
//
// It also writes `full`, which runs the fuzz smoke and the Windows leg of
// the Go job (#753). It is `true` for every event but a pull request, and
// for a train's pull request into main (head `train/**`, base `main`), so
// those legs run about once an hour during a campaign and catch a break
// before it reaches main. Every other pull request gets `false`: the
// local `mise run ci` gate runs the fuzz smoke and `go:cross` before each
// push.
//
// It fails closed. A changed file that no rule matches runs every job,
// and so does an error: the workflow skips a job only when `detect`
// succeeded and wrote `false` for it.
//
// `verdict` reads the `needs` context of the `CI result` job as JSON in
// NEEDS. docs/github-settings.md makes it the one required check,
// because a skipped matrix job never reports
// the per-OS check names. It exits 1 when a job failed or was cancelled,
// when `changes` itself failed, or when a job was skipped that `detect`
// did not skip. A job skipped by `full` counts as skipped by `detect`.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';

/**
 * The outputs of `detect`. `bundle` gates the Swift job's build, sign and
 * bundle steps, and `docs` the site check and build steps.
 */
export const OUTPUTS = /** @type {const} */ (['go', 'swift', 'bundle', 'docs']);

/** @typedef {typeof OUTPUTS[number]} Output */

const ALL = [...OUTPUTS];

/**
 * Which outputs a changed path turns on. A path matches every rule whose
 * glob fits, and gets the union of their outputs. A path no rule matches
 * turns on all of them. Match liberally: when a path might matter to a
 * job, it runs the job.
 *
 * `**` matches any number of directories, `*` anything but a `/`.
 * @type {{glob: string, jobs: readonly Output[]}[]}
 */
export const RULES = [
	// The toolchain, the CI definition and the scripts it runs.
	{ glob: '.mise.toml', jobs: ALL },
	{ glob: 'mise.lock', jobs: ALL },
	{ glob: '.github/workflows/**', jobs: ALL },
	{ glob: '.github/actions/**', jobs: ALL },
	{ glob: 'renovate.json', jobs: ALL },
	{ glob: 'scripts/**', jobs: ALL },

	// The Swift job builds the app bundle (app:bundle), which holds the Go
	// programs, from the layout and check in internal/appbundle and the
	// dev command cmd/wb-dev-app. Any file of the Go module can change a
	// program in the bundle, so every one runs the bundle steps, the only
	// steps of the Swift job that read Go (#354, #362).
	{ glob: 'packages/wraithbox-go/**', jobs: ['go', 'bundle'] },
	{ glob: '.golangci.yml', jobs: ['go'] },
	// The gRPC contracts have a Go and a Swift side.
	{ glob: '**/*.proto', jobs: ['go', 'swift', 'bundle'] },
	{ glob: '**/buf*.yaml', jobs: ['go', 'swift', 'bundle'] },

	// The release number goes into every Go and Swift build's version.
	// swift:build runs the Go stamping tool, so the tool, the package it
	// imports and the Go module and workspace files run the Swift job too.
	{ glob: 'VERSION', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'packages/wraithbox-go/internal/tools/buildversion/**', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'packages/wraithbox-go/internal/version/**', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'packages/wraithbox-go/go.mod', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'packages/wraithbox-go/go.sum', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'go.work', jobs: ['go', 'swift', 'bundle'] },
	{ glob: 'go.work.sum', jobs: ['go', 'swift', 'bundle'] },

	// Every Swift input runs the whole Swift job, bundle steps included.
	{ glob: 'packages/wraithbox-swift/**', jobs: ['swift', 'bundle'] },
	{ glob: '.swiftlint.yml', jobs: ['swift', 'bundle'] },
	{ glob: '.swift-format', jobs: ['swift', 'bundle'] },

	// The docs site renders docs/ and its own package. The prose checks
	// run on every pull request and need no rule.
	{ glob: 'docs/**', jobs: ['docs'] },
	{ glob: 'packages/wraithbox-doc/**', jobs: ['docs'] },
	{ glob: '**/*.md', jobs: ['docs'] },
	{ glob: '**/*.mdx', jobs: ['docs'] },
	{ glob: '.vale.ini', jobs: ['docs'] },
	{ glob: '.vale/**', jobs: ['docs'] },
	{ glob: 'cspell*', jobs: ['docs'] },

	// Read by no job that is ever skipped: the agent harness, and the
	// issue forms and labels that `mise run gh:labels` applies.
	{ glob: '.claude/**', jobs: [] },
	{ glob: '.github/ISSUE_TEMPLATE/**', jobs: [] },
	{ glob: '.github/labels*.yml', jobs: [] },
	{ glob: '.github/sync-labels.ts', jobs: [] },
];

/**
 * A regular expression for a glob, anchored at both ends.
 * @param {string} glob
 */
export function globToRegExp(glob) {
	let source = '';
	for (let i = 0; i < glob.length; i++) {
		const char = glob[i];
		if (glob.startsWith('**/', i)) {
			source += '(?:.*/)?';
			i += 2;
		} else if (glob.startsWith('**', i)) {
			source += '.*';
			i += 1;
		} else if (char === '*') {
			source += '[^/]*';
		} else {
			source += char.replace(/[.+?^${}()|[\]\\]/g, '\\$&');
		}
	}
	return new RegExp(`^${source}$`);
}

const MATCHERS = RULES.map((rule) => ({ ...rule, re: globToRegExp(rule.glob) }));

/**
 * The outputs one path turns on, and the rules that decided it.
 * @param {string} file a path relative to the repository root
 * @returns {{jobs: Set<Output>, rules: string[]}}
 */
export function classify(file) {
	const hits = MATCHERS.filter((rule) => rule.re.test(file));
	if (hits.length === 0) return { jobs: new Set(ALL), rules: ['no rule matches: every job'] };
	return { jobs: new Set(hits.flatMap((rule) => rule.jobs)), rules: hits.map((rule) => rule.glob) };
}

/**
 * The outputs a set of changed paths turns on, with one log line per path.
 * An empty list turns on everything: there is nothing to judge by.
 * @param {string[]} files
 * @returns {{jobs: Set<Output>, log: string[]}}
 */
export function decide(files) {
	if (files.length === 0) return { jobs: new Set(ALL), log: ['no changed file listed: every job'] };
	/** @type {Set<Output>} */
	const jobs = new Set();
	const log = files.map((file) => {
		const found = classify(file);
		for (const job of found.jobs) jobs.add(job);
		const names = [...found.jobs].join(' ') || 'none';
		return `${file}: ${names} (${found.rules.join(', ')})`;
	});
	return { jobs, log };
}

/**
 * Jobs the workflow may skip, each with the test of the `detect` outputs
 * that skips it. Each test matches the job's `if:` in ci.yml. An output
 * that is missing is not `false`, so the job counts as skipped wrongly.
 * @type {Record<string, (outputs: Record<string, string>) => boolean>}
 */
const SKIPPED_BY = {
	go: (outputs) => outputs.go === 'false',
	// A pull request that is not a train's into main skips the fuzz smoke (#753).
	fuzz: (outputs) => outputs.go === 'false' || outputs.full === 'false',
	swift: (outputs) => outputs.swift === 'false' && outputs.bundle === 'false',
};

/** Every job `CI result` must list in `needs:`. */
export const GATED = ['changes', 'go', 'fuzz', 'swift', 'docs', 'actions'];

/**
 * One line per reason the run must not pass, or an empty list.
 * @param {Record<string, {result?: string, outputs?: Record<string, string>}>} needs
 *   the `needs` context of the `CI result` job
 * @returns {string[]}
 */
export function verdict(needs) {
	const found = GATED.filter((job) => !(job in needs)).map((job) => `${job}: not in needs, so not checked`);
	const changes = needs.changes;
	const detected = changes?.result === 'success';
	if (changes && !detected) {
		found.push(`changes: ${changes.result}; the jobs ran as if every file changed, but detection must be fixed (scripts/ci-changes.mjs)`);
	}
	for (const [job, { result } = {}] of Object.entries(needs)) {
		if (job === 'changes' || result === 'success') continue;
		const skippedBy = SKIPPED_BY[job];
		const skippedByRule = result === 'skipped' && detected && skippedBy !== undefined && skippedBy(changes?.outputs ?? {});
		if (!skippedByRule) found.push(`${job}: ${result}`);
	}
	return found;
}

/** @typedef {(...args: string[]) => string} Git runs git with the arguments and returns its stdout */

/** @type {Git} */
const runGit = (...args) => execFileSync('git', args, { encoding: 'utf8' });

/**
 * The paths a pull request changes: its merge commit against the base
 * branch, both sides of a rename. Throws unless HEAD is a merge commit.
 * @param {Git} git
 * @returns {string[]}
 */
export function changedFiles(git) {
	const parents = git('rev-list', '--parents', '-n', '1', 'HEAD').trim().split(' ');
	if (parents.length !== 3) throw new Error(`HEAD has ${parents.length - 1} parents, not the 2 of a pull request merge commit`);
	return git('diff', '--name-only', '--no-renames', '-z', 'HEAD^1', 'HEAD').split('\0').filter(Boolean);
}

/**
 * Whether a run gets the fuzz smoke and the Windows Go leg (#753), and
 * why. Every event but a pull request does, and so does a train's pull
 * request into main. A builder's pull request into a train doesn't.
 * @param {Record<string, string | undefined>} env the environment, for
 *   GITHUB_EVENT_NAME, GITHUB_HEAD_REF and GITHUB_BASE_REF
 * @returns {{full: boolean, reason: string}}
 */
export function full(env) {
	const event = env.GITHUB_EVENT_NAME ?? '';
	if (event !== 'pull_request') return { full: true, reason: `event ${JSON.stringify(event)} is not pull_request` };
	const head = env.GITHUB_HEAD_REF ?? '';
	const base = env.GITHUB_BASE_REF ?? '';
	const branches = `head ${JSON.stringify(head)} into base ${JSON.stringify(base)}`;
	if (head.startsWith('train/') && base === 'main') return { full: true, reason: `${branches} is a train's pull request into main` };
	return { full: false, reason: `${branches} is not a train's pull request into main` };
}

/**
 * The log lines and the `name=value` output lines for one run. Only a
 * pull_request event lists the changed files; every other event turns on
 * every output.
 * @param {Record<string, string | undefined>} env the environment, for
 *   GITHUB_EVENT_NAME, GITHUB_HEAD_REF and GITHUB_BASE_REF
 * @param {() => string[]} listFiles the changed paths of a pull request
 * @returns {{log: string[], outputs: string[]}}
 */
export function detect(env, listFiles) {
	const event = env.GITHUB_EVENT_NAME ?? '';
	let jobs = new Set(ALL);
	/** @type {string[]} */
	let log = [`event ${JSON.stringify(event)} is not pull_request: every job`];
	if (event === 'pull_request') ({ jobs, log } = decide(listFiles()));
	const legs = full(env);
	log = [...log, `full=${legs.full}: ${legs.reason} (fuzz smoke and Windows Go leg, #753)`];
	return { log, outputs: [...OUTPUTS.map((output) => `${output}=${jobs.has(output)}`), `full=${legs.full}`] };
}

function runDetect() {
	const { log, outputs } = detect(process.env, () => changedFiles(runGit));
	for (const line of [...log, ...outputs]) console.log(line);
	if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `${outputs.join('\n')}\n`);
}

function check() {
	const found = verdict(JSON.parse(process.env.NEEDS ?? '{}'));
	for (const line of found) console.error(line);
	if (found.length === 0) console.log('every job passed, or was skipped because detection said so');
	process.exit(found.length ? 1 : 0);
}

if (import.meta.main) {
	const mode = process.argv[2];
	if (mode === 'detect') runDetect();
	else if (mode === 'verdict') check();
	else {
		console.error('usage: bun scripts/ci-changes.mjs detect|verdict');
		process.exit(2);
	}
}
