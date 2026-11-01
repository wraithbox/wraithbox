// @ts-check
// Run with `bun test scripts/ci-changes.test.mjs`.
import { describe, expect, test } from 'bun:test';
import fs from 'node:fs';
import path from 'node:path';
import { changedFiles, classify, decide, detect, GATED, globToRegExp, RULES, verdict } from './ci-changes.mjs';

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
		['a Go-only change runs Go and only the bundle steps of Swift', ['packages/wraithbox-go/internal/x/x.go'], ['bundle', 'go']],
		['a daemon package runs the bundle steps', ['packages/wraithbox-go/internal/hostd/datadisk/disk.go'], ['bundle', 'go']],
		['a Go test file runs the bundle steps', ['packages/wraithbox-go/internal/netd/dns_test.go'], ['bundle', 'go']],
		['the Go lint settings do not run Swift', ['.golangci.yml'], ['go']],
		['a Swift-only change does not run Go', ['packages/wraithbox-swift/Sources/A.swift'], ['bundle', 'swift']],
		['a proto change runs both sides', ['proto/wb/v1/vm.proto'], ['bundle', 'go', 'swift']],
		['the release number runs both sides', ['VERSION'], ['bundle', 'go', 'swift']],
		['the stamping tool runs both sides', ['packages/wraithbox-go/internal/tools/buildversion/main.go'], ['bundle', 'go', 'swift']],
		['the version package runs both sides', ['packages/wraithbox-go/internal/version/version.go'], ['bundle', 'go', 'swift']],
		['the Go module runs both sides', ['packages/wraithbox-go/go.mod'], ['bundle', 'go', 'swift']],
		['the Go workspace runs both sides', ['go.work'], ['bundle', 'go', 'swift']],
		['a Go program runs Go and only the bundle steps of Swift', ['packages/wraithbox-go/cmd/wb/main.go'], ['bundle', 'go']],
		['the bundle layout and check run Go and the bundle steps', ['packages/wraithbox-go/internal/appbundle/check.go'], ['bundle', 'go']],
		['a Go program and a Swift file run all of Swift', ['packages/wraithbox-go/cmd/wb/main.go', 'packages/wraithbox-swift/Package.swift'], ['bundle', 'go', 'swift']],
		['the bundle script runs everything', ['scripts/app-bundle.sh'], ['bundle', 'docs', 'go', 'swift']],
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
		expect(jobsFor([file])).toEqual(['bundle', 'docs', 'go', 'swift']);
	});

	test('an empty diff runs every job', () => {
		expect(jobsFor([])).toEqual(['bundle', 'docs', 'go', 'swift']);
	});

	test('every rule for a file of the Go module runs the bundle steps', () => {
		const missing = RULES.filter((rule) => rule.glob.startsWith('packages/wraithbox-go/') && !rule.jobs.includes('bundle')).map((rule) => rule.glob);
		expect(missing).toEqual([]);
	});

	test('every rule that runs the Swift steps runs the bundle steps too', () => {
		const missing = RULES.filter((rule) => rule.jobs.includes('swift') && !rule.jobs.includes('bundle')).map((rule) => rule.glob);
		expect(missing).toEqual([]);
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
	const passing = (outputs = { go: 'true', swift: 'true', bundle: 'true', docs: 'true' }) =>
		/** @type {Record<string, {result: string, outputs?: Record<string, string>}>} */ (
			Object.fromEntries(GATED.map((job) => [job, job === 'changes' ? { result: 'success', outputs } : { result: 'success' }]))
		);

	test('every job passed', () => {
		expect(verdict(passing())).toEqual([]);
	});

	test('a job skipped because detection said so passes', () => {
		const needs = passing({ go: 'false', swift: 'false', bundle: 'false', docs: 'true' });
		needs.go.result = 'skipped';
		needs.fuzz.result = 'skipped';
		needs.swift.result = 'skipped';
		expect(verdict(needs)).toEqual([]);
	});

	test('the fuzz shards skipped while detection said to run Go fail', () => {
		const needs = passing({ go: 'true', swift: 'false', bundle: 'true', docs: 'false' });
		needs.fuzz.result = 'skipped';
		expect(verdict(needs)).toEqual(['fuzz: skipped']);
	});

	test('a failed fuzz shard fails', () => {
		const needs = passing();
		needs.fuzz.result = 'failure';
		expect(verdict(needs)).toEqual(['fuzz: failure']);
	});

	test('the Swift job skipped while detection said to run its bundle steps fails', () => {
		const needs = passing({ go: 'true', swift: 'false', bundle: 'true', docs: 'false' });
		needs.swift.result = 'skipped';
		expect(verdict(needs)).toEqual(['swift: skipped']);
	});

	test('the Swift job skipped while its bundle output is missing fails', () => {
		const needs = passing({ go: 'true', swift: 'false', docs: 'false' });
		needs.swift.result = 'skipped';
		expect(verdict(needs)).toEqual(['swift: skipped']);
	});

	test.each(['failure', 'cancelled'])('a job that ended %s fails', (result) => {
		const needs = passing();
		needs.swift.result = result;
		expect(verdict(needs)).toEqual([`swift: ${result}`]);
	});

	test('a job skipped while detection said to run it fails', () => {
		const needs = passing({ go: 'true', swift: 'false', bundle: 'false', docs: 'true' });
		needs.go.result = 'skipped';
		expect(verdict(needs)).toEqual(['go: skipped']);
	});

	test('docs is never skipped: its prose checks run on every pull request', () => {
		const needs = passing({ go: 'true', swift: 'true', bundle: 'true', docs: 'false' });
		needs.docs.result = 'skipped';
		expect(verdict(needs)).toEqual(['docs: skipped']);
	});

	test('the actions job is never skipped', () => {
		const needs = passing();
		needs.actions.result = 'skipped';
		expect(verdict(needs)).toEqual(['actions: skipped']);
	});

	test('a failed detection fails the run, and accepts no skipped job', () => {
		const needs = passing({ go: 'false', swift: 'false', bundle: 'false', docs: 'false' });
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
		expect(outputs).toEqual(['go=true', 'swift=false', 'bundle=true', 'docs=false']);
		expect(log).toEqual(['packages/wraithbox-go/a.go: go bundle (packages/wraithbox-go/**)']);
	});

	test('a pull request that changes only a daemon package turns on the bundle steps', () => {
		const { outputs } = detect({ GITHUB_EVENT_NAME: 'pull_request' }, () => ['packages/wraithbox-go/internal/hostd/session.go']);
		expect(outputs).toEqual(['go=true', 'swift=false', 'bundle=true', 'docs=false']);
	});

	test.each(['push', 'workflow_dispatch', undefined])('event %p runs every job without listing files', (event) => {
		const { log, outputs } = detect({ GITHUB_EVENT_NAME: event }, () => {
			throw new Error('listed files for an event other than pull_request');
		});
		expect(outputs).toEqual(['go=true', 'swift=true', 'bundle=true', 'docs=true']);
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

	test('the fuzz shards run under the Go job\'s condition, on Linux', () => {
		expect(workflow.jobs.fuzz.if).toBe(workflow.jobs.go.if);
		expect(workflow.jobs.fuzz['runs-on']).toBe('ubuntu-24.04');
	});

	test('CI result needs exactly the gated jobs and always runs', () => {
		expect([...workflow.jobs.result.needs].sort()).toEqual([...GATED].sort());
		expect(workflow.jobs.result.if).toBe('${{ always() }}');
		expect(workflow.jobs.result.name).toBe('CI result');
	});

	/** @type {{name: string, uses?: string, if?: string, with?: Record<string, string>}[]} */
	const swiftSteps = workflow.jobs.swift.steps;
	const step = (/** @type {string} */ name) => swiftSteps.find((s) => s.name === name);

	test('the Swift job runs when either its swift or its bundle output is on', () => {
		expect(workflow.jobs.swift.if).toContain("needs.changes.outputs.swift == 'false' && needs.changes.outputs.bundle == 'false'");
		expect(workflow.jobs.changes.outputs.bundle).toBe('${{ steps.detect.outputs.bundle }}');
	});

	test('a bundle-only run skips the Swift lint, contracts and test steps, and runs the rest', () => {
		const swiftOnly = "${{ !(needs.changes.result == 'success' && needs.changes.outputs.swift == 'false') }}";
		const gated = swiftSteps.filter((s) => s.if === swiftOnly).map((s) => s.name);
		expect(gated).toEqual(['Lint (swift-format + SwiftLint)', 'Contracts (Swift drift)', 'Test']);
		for (const name of ['Build', 'Sign wb-vmd (ad-hoc, hardened runtime)', 'Bundle (ad-hoc Wraith Box.app + entitlement check)']) {
			expect(step(name)?.if).toBeUndefined();
		}
	});

	test('the Swift build cache is saved only by a push to main, to the key and paths it restores', () => {
		const restore = step('Restore Swift build cache');
		const save = step('Save Swift build cache');
		expect(restore?.uses).toStartWith('actions/cache/restore@');
		expect(save?.uses).toStartWith('actions/cache/save@');
		expect(save?.if).toStartWith("${{ github.event_name == 'push' && github.ref == 'refs/heads/main' && ");
		expect(save?.with?.path).toBe(restore?.with?.path);
		expect(save?.with?.key).toBe('${{ steps.swift-cache.outputs.cache-primary-key }}');
		expect(swiftSteps.indexOf(/** @type {any} */ (save))).toBe(swiftSteps.length - 1);
	});

	test('the Swift build cache is trimmed just before the save, and only when it saves', () => {
		const trim = step('Trim Swift build cache');
		const save = step('Save Swift build cache');
		expect(swiftSteps.indexOf(/** @type {any} */ (trim))).toBe(swiftSteps.indexOf(/** @type {any} */ (save)) - 1);
		expect(trim?.if).toBe(save?.if);
	});
});
