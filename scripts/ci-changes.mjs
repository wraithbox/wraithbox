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
// with the rule that matched it, and writes `go`, `swift` and `docs`
// (`true` or `false`) to GITHUB_OUTPUT.
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
// did not skip.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';

/** The outputs of `detect`. `docs` gates the site check and build steps. */
export const OUTPUTS = /** @type {const} */ (['go', 'swift', 'docs']);

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
	{ glob: '.github/dependabot.yml', jobs: ALL },
	{ glob: 'scripts/**', jobs: ALL },

	{ glob: 'packages/wraithbox-go/**', jobs: ['go'] },
	{ glob: '.golangci.yml', jobs: ['go'] },
	// The gRPC contracts have a Go and a Swift side.
	{ glob: '**/*.proto', jobs: ['go', 'swift'] },
	{ glob: '**/buf*.yaml', jobs: ['go', 'swift'] },

	// The release number goes into every Go and Swift build's version.
	// swift:build runs the Go stamping tool, so the tool, the package it
	// imports and the Go module and workspace files run the Swift job too.
	{ glob: 'VERSION', jobs: ['go', 'swift'] },
	{ glob: 'packages/wraithbox-go/internal/tools/buildversion/**', jobs: ['go', 'swift'] },
	{ glob: 'packages/wraithbox-go/internal/version/**', jobs: ['go', 'swift'] },
	{ glob: 'packages/wraithbox-go/go.mod', jobs: ['go', 'swift'] },
	{ glob: 'packages/wraithbox-go/go.sum', jobs: ['go', 'swift'] },
	{ glob: 'go.work', jobs: ['go', 'swift'] },
	{ glob: 'go.work.sum', jobs: ['go', 'swift'] },

	// The Swift job builds the app bundle (app:bundle), which holds the Go
	// programs, from the layout and check in internal/appbundle and the
	// dev command cmd/wb-dev-app.
	{ glob: 'packages/wraithbox-go/cmd/**', jobs: ['go', 'swift'] },
	{ glob: 'packages/wraithbox-go/internal/appbundle/**', jobs: ['go', 'swift'] },

	{ glob: 'packages/wraithbox-swift/**', jobs: ['swift'] },
	{ glob: '.swiftlint.yml', jobs: ['swift'] },
	{ glob: '.swift-format', jobs: ['swift'] },

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

/** Jobs the workflow may skip, with the `detect` output that skips each. */
const SKIPPABLE = /** @type {Record<string, Output>} */ ({ go: 'go', swift: 'swift' });

/** Every job `CI result` must list in `needs:`. */
export const GATED = ['changes', 'go', 'swift', 'docs', 'actions'];

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
		const output = SKIPPABLE[job];
		const skippedByRule = result === 'skipped' && output && detected && changes?.outputs?.[output] === 'false';
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
 * The log lines and the `name=value` output lines for one run. Only a
 * pull_request event lists the changed files; every other event turns on
 * every output.
 * @param {Record<string, string | undefined>} env the environment, for GITHUB_EVENT_NAME
 * @param {() => string[]} listFiles the changed paths of a pull request
 * @returns {{log: string[], outputs: string[]}}
 */
export function detect(env, listFiles) {
	const event = env.GITHUB_EVENT_NAME ?? '';
	let jobs = new Set(ALL);
	/** @type {string[]} */
	let log = [`event ${JSON.stringify(event)} is not pull_request: every job`];
	if (event === 'pull_request') ({ jobs, log } = decide(listFiles()));
	return { log, outputs: OUTPUTS.map((output) => `${output}=${jobs.has(output)}`) };
}

function runDetect() {
	const { log, outputs } = detect(process.env, () => changedFiles(runGit));
	for (const line of [...log, ...outputs]) console.log(line);
	if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `${outputs.join('\n')}\n`);
}

function check() {
	const found = verdict(JSON.parse(process.env.NEEDS ?? '{}'));
	for (const line of found) console.error(line);
	if (found.length === 0) console.log('every job passed, or was skipped because its inputs did not change');
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
