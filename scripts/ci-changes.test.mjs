// @ts-check
// Run with `bun test scripts/ci-changes.test.mjs`.
import { describe, expect, test } from 'bun:test';
import fs from 'node:fs';
import path from 'node:path';
import { changedFiles, classify, decide, detect, GATED, globToRegExp, verdict } from './ci-changes.mjs';

/** @param {string[]} files */
const jobsFor = (files) => [...decide(files).jobs].sort();

describe('globToRegExp', () => {
	test.each([
		['**/*.proto', 'a.proto', true],
		['**/*.proto', 'proto/wb/v1/vm.proto', true],
		['**/*.proto', 'vm.proto.txt', false],
		['docs/**', 'docs/spec/S01.md', true],
		['docs/**', 'docs-old/a', false],
		['cspell*', 'cspell.json', true],
		['cspell*', 'packages/cspell.json', false],
		['.mise.toml', '_mise.toml', false],
		['.github/labels*.yml', '.github/labels-areas.yml', true],
	])('%s on %s is %p', (glob, file, want) => {
		expect(globToRegExp(glob).test(file)).toBe(want);
	});
});

describe('decide', () => {
	test.each([
		['a docs-only change runs only the docs site', ['docs/spec/S03-requirements.md', 'README.md'], ['docs']],
		['a Go-only change does not run Swift', ['packages/wraithbox-go/internal/x/x.go'], ['go']],
		['a Swift-only change does not run Go', ['packages/wraithbox-swift/Sources/A.swift'], ['swift']],
		['a proto change runs both sides', ['proto/wb/v1/vm.proto'], ['go', 'swift']],
		['the release number runs both sides', ['VERSION'], ['go', 'swift']],
		['the stamping tool runs both sides', ['packages/wraithbox-go/internal/tools/buildversion/main.go'], ['go', 'swift']],
		['the version package runs both sides', ['packages/wraithbox-go/internal/version/version.go'], ['go', 'swift']],
		['the Go module runs both sides', ['packages/wraithbox-go/go.mod'], ['go', 'swift']],
		['the Go workspace runs both sides', ['go.work'], ['go', 'swift']],
		['a Go program runs both sides, for the bundle', ['packages/wraithbox-go/cmd/wb/main.go'], ['go', 'swift']],
		['the bundle layout and check run both sides', ['packages/wraithbox-go/internal/appbundle/check.go'], ['go', 'swift']],
		['the bundle script runs everything', ['scripts/app-bundle.sh'], ['docs', 'go', 'swift']],
		['a harness change runs nothing filtered', ['.claude/settings.json'], []],
		['an agent file is Markdown', ['.claude/agents/builder.md'], ['docs']],
		['Go and docs together', ['.golangci.yml', 'docs/a.md'], ['docs', 'go']],
	])('%s', (_name, files, want) => {
		expect(jobsFor(files)).toEqual(want);
	});

	test.each([
		'.mise.toml',
		'mise.lock',
		'.github/workflows/ci.yml',
		'.github/workflows/deploy.yml',
		'.github/actions/x/action.yml',
		'.github/dependabot.yml',
		'scripts/ci-changes.mjs',
		// No rule: fail closed.
		'LICENSE',
		'.gitattributes',
		'osv-scanner.toml',
		'packages/wraithbox-dotnet/A.cs',
		'spikes/vm.entitlements',
	])('%s runs every job', (file) => {
		expect(jobsFor([file])).toEqual(['docs', 'go', 'swift']);
	});

	test('an empty diff runs every job', () => {
		expect(jobsFor([])).toEqual(['docs', 'go', 'swift']);
	});

	test('the log names the file and the rule', () => {
		expect(decide(['.golangci.yml']).log).toEqual(['.golangci.yml: go (.golangci.yml)']);
		expect(decide(['.claude/settings.json']).log).toEqual(['.claude/settings.json: none (.claude/**)']);
		expect(classify('LICENSE').rules).toEqual(['no rule matches: every job']);
	});
});

describe('verdict', () => {
	/**
	 * A `needs` context where detection ran and every job passed.
	 * @param {Record<string, string>} outputs
	 */
	const passing = (outputs = { go: 'true', swift: 'true', docs: 'true' }) =>
		/** @type {Record<string, {result: string, outputs?: Record<string, string>}>} */ (
			Object.fromEntries(GATED.map((job) => [job, job === 'changes' ? { result: 'success', outputs } : { result: 'success' }]))
		);

	test('every job passed', () => {
		expect(verdict(passing())).toEqual([]);
	});

	test('a job skipped because detection said so passes', () => {
		const needs = passing({ go: 'false', swift: 'false', docs: 'true' });
		needs.go.result = 'skipped';
		needs.swift.result = 'skipped';
		expect(verdict(needs)).toEqual([]);
	});

	test.each(['failure', 'cancelled'])('a job that ended %s fails', (result) => {
		const needs = passing();
		needs.swift.result = result;
		expect(verdict(needs)).toEqual([`swift: ${result}`]);
	});

	test('a job skipped while detection said to run it fails', () => {
		const needs = passing({ go: 'true', swift: 'false', docs: 'true' });
		needs.go.result = 'skipped';
		expect(verdict(needs)).toEqual(['go: skipped']);
	});

	test('docs is never skipped: its prose checks run on every pull request', () => {
		const needs = passing({ go: 'true', swift: 'true', docs: 'false' });
		needs.docs.result = 'skipped';
		expect(verdict(needs)).toEqual(['docs: skipped']);
	});

	test('the actions job is never skipped', () => {
		const needs = passing();
		needs.actions.result = 'skipped';
		expect(verdict(needs)).toEqual(['actions: skipped']);
	});

	test('a failed detection fails the run, and accepts no skipped job', () => {
		const needs = passing({ go: 'false', swift: 'false', docs: 'false' });
		needs.changes.result = 'failure';
		needs.go.result = 'skipped';
		const found = verdict(needs);
		expect(found).toHaveLength(2);
		expect(found[0]).toStartWith('changes: failure');
		expect(found[1]).toBe('go: skipped');
	});

	test('a job missing from needs fails', () => {
		const needs = passing();
		delete needs.swift;
		expect(verdict(needs)).toEqual(['swift: not in needs, so not checked']);
	});

	test('an empty needs context fails', () => {
		expect(verdict({})).toHaveLength(GATED.length);
	});
});

describe('detect', () => {
	const prFiles = () => ['packages/wraithbox-go/a.go'];

	test('a pull request turns on the jobs its files need', () => {
		const { log, outputs } = detect({ GITHUB_EVENT_NAME: 'pull_request' }, prFiles);
		expect(outputs).toEqual(['go=true', 'swift=false', 'docs=false']);
		expect(log).toEqual(['packages/wraithbox-go/a.go: go (packages/wraithbox-go/**)']);
	});

	test.each(['push', 'workflow_dispatch', undefined])('event %p runs every job without listing files', (event) => {
		const { log, outputs } = detect({ GITHUB_EVENT_NAME: event }, () => {
			throw new Error('listed files for an event other than pull_request');
		});
		expect(outputs).toEqual(['go=true', 'swift=true', 'docs=true']);
		expect(log[0]).toEndWith('is not pull_request: every job');
	});

	test('an error listing the files fails the run', () => {
		const failing = () => {
			throw new Error('no merge commit');
		};
		expect(() => detect({ GITHUB_EVENT_NAME: 'pull_request' }, failing)).toThrow('no merge commit');
	});
});

describe('changedFiles', () => {
	/**
	 * A stand-in for git that answers rev-list with the given parent line.
	 * @param {string} revList
	 * @param {string[]} calls
	 */
	const stubGit =
		(revList, calls = []) =>
		(/** @type {string[]} */ ...args) => {
			calls.push(args.join(' '));
			if (args[0] === 'rev-list') return `${revList}\n`;
			return 'docs/a.md\0packages/wraithbox-go/old.go\0';
		};

	test('a merge commit diffs against its first parent, without rename detection', () => {
		/** @type {string[]} */
		const calls = [];
		expect(changedFiles(stubGit('m p1 p2', calls))).toEqual(['docs/a.md', 'packages/wraithbox-go/old.go']);
		expect(calls[1]).toBe('diff --name-only --no-renames -z HEAD^1 HEAD');
	});

	test.each([
		['a commit with one parent', 'c p1'],
		['a root commit', 'c'],
		['an octopus merge', 'm p1 p2 p3'],
	])('%s throws', (_name, revList) => {
		expect(() => changedFiles(stubGit(revList))).toThrow('not the 2 of a pull request merge commit');
	});
});

describe('ci.yml', () => {
	const file = path.join(import.meta.dir, '../.github/workflows/ci.yml');
	const workflow = Bun.YAML.parse(fs.readFileSync(file, 'utf8'));

	test('CI result gates every job except vuln and itself', () => {
		expect(Object.keys(workflow.jobs).sort()).toEqual([...GATED, 'vuln', 'result'].sort());
	});

	test('CI result needs exactly the gated jobs and always runs', () => {
		expect([...workflow.jobs.result.needs].sort()).toEqual([...GATED].sort());
		expect(workflow.jobs.result.if).toBe('${{ always() }}');
		expect(workflow.jobs.result.name).toBe('CI result');
	});
});
