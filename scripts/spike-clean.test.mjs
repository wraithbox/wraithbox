// @ts-check
// Tests for spike-clean.mjs. Each test builds a throwaway cache under
// `.scratch/` at the repository root and never reads or writes the real
// spike cache. Run with `mise run scripts:test`.
import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import fs from 'node:fs';
import path from 'node:path';
import { defaultRoot, KEEP, run, selects } from './spike-clean.mjs';

const scratch = path.join(import.meta.dir, '..', '.scratch');

/** @type {string} */
let base;
/** @type {string} */
let root;
/** @type {{ log: string[], error: string[] }} */
let lines;
const out = {
	/** @param {string} line */
	log: (line) => lines.log.push(line),
	/** @param {string} line */
	error: (line) => lines.error.push(line),
};

/**
 * Writes a file with some content, making its directories.
 * @param {string} file
 */
function touch(file) {
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, 'x'.repeat(8192));
}

/** @param {string} name */
const inRoot = (name) => fs.existsSync(path.join(root, name));

beforeEach(() => {
	fs.mkdirSync(scratch, { recursive: true });
	base = fs.mkdtempSync(path.join(scratch, 'spike-clean-'));
	root = path.join(base, 'wraithbox-spikes');
	lines = { log: [], error: [] };
	touch(path.join(root, KEEP, 'Restore.ipsw'));
	touch(path.join(root, 'x25', 'disk.img'));
	touch(path.join(root, 'x25-outside', 'disk.img'));
	touch(path.join(root, 'x250', 'disk.img'));
	touch(path.join(root, 'x26', 'disk.img'));
	touch(path.join(root, 'x08', 'run1', 'disk.img'));
	touch(path.join(base, 'outside', 'precious.txt'));
});

afterEach(() => {
	fs.rmSync(base, { recursive: true, force: true });
});

describe('selects', () => {
	test.each([
		['x25', 25, true],
		['x25-outside', 25, true],
		['x08', 8, true],
		['x250', 25, false],
		['x2', 25, false],
		['x25-', 25, false],
		[KEEP, 25, false],
		['y25', 25, false],
	])('%s for spike %d is %p', (name, id, want) => {
		expect(selects(name, id)).toBe(want);
	});
});

describe('run', () => {
	test('without arguments it lists the cache and removes nothing', () => {
		expect(run(root, [], out)).toBe(0);
		const text = lines.log.join('\n');
		expect(text).toContain(`${KEEP}, kept`);
		expect(text).toContain('x25-outside');
		for (const name of [KEEP, 'x25', 'x25-outside', 'x250', 'x26', 'x08']) expect(inRoot(name)).toBe(true);
	});

	test('removes the spike and its suffixed siblings, and nothing else', () => {
		expect(run(root, ['x25'], out)).toBe(0);
		expect(inRoot('x25')).toBe(false);
		expect(inRoot('x25-outside')).toBe(false);
		for (const name of [KEEP, 'x250', 'x26', 'x08']) expect(inRoot(name)).toBe(true);
		expect(lines.log.filter((line) => line.startsWith('removed '))).toHaveLength(2);
		expect(lines.log.at(-1)).toMatch(/^freed \d/);
	});

	test('compares spike numbers, so x8 removes x08', () => {
		expect(run(root, ['X8'], out)).toBe(0);
		expect(inRoot('x08')).toBe(false);
	});

	test('a spike with nothing cached is reported, not an error', () => {
		expect(run(root, ['x99'], out)).toBe(0);
		expect(lines.log[0]).toContain('x99: nothing in');
	});

	test('a missing cache is not an error', () => {
		const missing = path.join(base, 'missing');
		expect(run(missing, [], out)).toBe(0);
		expect(run(missing, ['x25'], out)).toBe(0);
	});

	test.each([
		[KEEP],
		['..'],
		['.'],
		[''],
		['*'],
		['x25/..'],
		['x25/../restore-images'],
		['../outside'],
		['x25/run1'],
		['/'],
		['x25-outside'],
		['25'],
	])('refuses the argument %p and removes nothing', (arg) => {
		expect(run(root, ['x26', arg], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('rule: arguments are spike IDs');
		for (const name of [KEEP, 'x25', 'x25-outside', 'x26']) expect(inRoot(name)).toBe(true);
		expect(fs.existsSync(path.join(base, 'outside', 'precious.txt'))).toBe(true);
	});

	test('refuses an entry that links outside the cache, and removes nothing', () => {
		fs.symlinkSync(path.join(base, 'outside'), path.join(root, 'x30'));
		expect(run(root, ['x26', 'x30'], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('symbolic link');
		expect(inRoot('x26')).toBe(true);
		expect(fs.existsSync(path.join(base, 'outside', 'precious.txt'))).toBe(true);
	});

	test('refuses an entry that links to the image cache', () => {
		fs.symlinkSync(path.join(root, KEEP), path.join(root, 'x31'));
		expect(run(root, ['x31'], out)).toBe(1);
		expect(fs.existsSync(path.join(root, KEEP, 'Restore.ipsw'))).toBe(true);
	});

	test('refuses an entry that is a file', () => {
		touch(path.join(root, 'x40'));
		expect(run(root, ['x40'], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('not a directory');
		expect(inRoot('x40')).toBe(true);
	});

	test('refuses a cache directory that is a symbolic link', () => {
		const link = path.join(base, 'linked-cache');
		fs.symlinkSync(root, link);
		expect(run(link, ['x25'], out)).toBe(1);
		expect(run(link, [], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('the cache is a real directory');
		expect(inRoot('x25')).toBe(true);
	});

	test('a link inside a removed bundle is removed, not followed', () => {
		fs.symlinkSync(path.join(base, 'outside'), path.join(root, 'x25', 'link'));
		expect(run(root, ['x25'], out)).toBe(0);
		expect(inRoot('x25')).toBe(false);
		expect(fs.existsSync(path.join(base, 'outside', 'precious.txt'))).toBe(true);
	});
});

test('the cache path does not follow $HOME', () => {
	const saved = process.env.HOME;
	const before = defaultRoot();
	try {
		process.env.HOME = base;
		expect(defaultRoot()).toBe(before);
		expect(defaultRoot().startsWith(base)).toBe(false);
	} finally {
		process.env.HOME = saved;
	}
	expect(before.endsWith(path.join('Library', 'Caches', 'wraithbox-spikes'))).toBe(true);
});
