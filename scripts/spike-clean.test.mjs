// @ts-check
// Tests for spike-clean.mjs. Each test builds a throwaway cache under
// `.scratch/` at the repository root and never reads or writes the real
// spike cache. Run with `mise run scripts:test`.
import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import fs from 'node:fs';
import path from 'node:path';
import { defaultRoot, KEEP_FILE, run, SHARED, selects } from './spike-clean.mjs';

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

/**
 * File system calls with one change: entries under a directory named
 * `datavol` report another device, as a mounted volume does.
 * @type {import('./spike-clean.mjs').Fs}
 */
const mounted = {
	lstat: (target) => {
		const stat = fs.lstatSync(target);
		if (!target.split(path.sep).includes('datavol')) return stat;
		return Object.assign(Object.create(Object.getPrototypeOf(stat)), stat, { dev: stat.dev + 1 });
	},
	remove: (target) => fs.rmSync(target, { recursive: true }),
};

/** @param {string} name */
const inRoot = (name) => fs.existsSync(path.join(root, name));

beforeEach(() => {
	fs.mkdirSync(scratch, { recursive: true });
	base = fs.mkdtempSync(path.join(scratch, 'spike-clean-'));
	root = path.join(base, 'wraithbox-spikes');
	lines = { log: [], error: [] };
	touch(path.join(root, SHARED, 'Restore.ipsw'));
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
		[SHARED, 25, false],
		['y25', 25, false],
	])('%s for spike %d is %p', (name, id, want) => {
		expect(selects(name, id)).toBe(want);
	});
});

describe('run', () => {
	test('without arguments it lists the cache and removes nothing', () => {
		expect(run(root, [], out)).toBe(0);
		const text = lines.log.join('\n');
		expect(text).toContain(`${SHARED}, kept`);
		expect(text).toContain('x25-outside');
		for (const name of [SHARED, 'x25', 'x25-outside', 'x250', 'x26', 'x08']) expect(inRoot(name)).toBe(true);
	});

	test('removes the spike and its suffixed siblings, and nothing else', () => {
		expect(run(root, ['x25'], out)).toBe(0);
		expect(inRoot('x25')).toBe(false);
		expect(inRoot('x25-outside')).toBe(false);
		for (const name of [SHARED, 'x250', 'x26', 'x08']) expect(inRoot(name)).toBe(true);
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
		[SHARED],
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
		for (const name of [SHARED, 'x25', 'x25-outside', 'x26']) expect(inRoot(name)).toBe(true);
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
		fs.symlinkSync(path.join(root, SHARED), path.join(root, 'x31'));
		expect(run(root, ['x31'], out)).toBe(1);
		expect(fs.existsSync(path.join(root, SHARED, 'Restore.ipsw'))).toBe(true);
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

	test('names each spike once, so a repeated ID is not an error', () => {
		expect(run(root, ['x8', 'x08', 'x25', 'x25'], out)).toBe(0);
		expect(inRoot('x08')).toBe(false);
		expect(inRoot('x25')).toBe(false);
		expect(lines.log.filter((line) => line.startsWith('removed '))).toHaveLength(3);
	});

	test('refuses a spike with a volume mounted inside it, and removes nothing', () => {
		touch(path.join(root, 'x17', 'datavol', 'data.img'));
		expect(run(root, ['x26', 'x17'], out, mounted)).toBe(1);
		expect(lines.error.join('\n')).toContain('is on another device (rule: detach mounted volumes first');
		expect(inRoot(path.join('x17', 'datavol', 'data.img'))).toBe(true);
		expect(inRoot('x26')).toBe(true);
	});

	test('the list names a mounted volume', () => {
		touch(path.join(root, 'x17', 'datavol', 'data.img'));
		expect(run(root, [], out, mounted)).toBe(0);
		expect(lines.log.join('\n')).toContain('holds a mounted volume at');
	});

	test('refuses a spike directory with a KEEP file, and removes nothing', () => {
		fs.writeFileSync(path.join(root, 'x08', KEEP_FILE), '#228: the data disk follow-up needs run1\n');
		expect(run(root, ['x26', 'x8'], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('#228: the data disk follow-up needs run1');
		expect(inRoot('x08')).toBe(true);
		expect(inRoot('x26')).toBe(true);
		lines = { log: [], error: [] };
		expect(run(root, [], out)).toBe(0);
		expect(lines.log.join('\n')).toContain(`${KEEP_FILE}: #228`);
	});

	test('a KEEP link counts as a KEEP file and is not read', () => {
		fs.symlinkSync(path.join(base, 'outside', 'precious.txt'), path.join(root, 'x08', KEEP_FILE));
		expect(run(root, ['x8'], out)).toBe(1);
		expect(lines.error.join('\n')).toContain('is not a regular file');
		expect(inRoot('x08')).toBe(true);
	});

	test('stops at a failed removal and reports what it removed', () => {
		/** @type {import('./spike-clean.mjs').Fs} */
		const failing = {
			lstat: (target) => fs.lstatSync(target),
			remove: (target) => {
				if (target.endsWith('x25-outside')) throw new Error('operation not permitted');
				fs.rmSync(target, { recursive: true });
			},
		};
		expect(run(root, ['x25', 'x26'], out, failing)).toBe(1);
		expect(lines.log.join('\n')).toContain(`removed ${fs.realpathSync(root)}${path.sep}x25 (`);
		expect(lines.error.join('\n')).toContain('x25-outside: removal failed, part of it can be left: operation not permitted');
		expect(lines.error.at(-1)).toMatch(/^Stopped\. Freed \d/);
		expect(inRoot('x25-outside')).toBe(true);
		expect(inRoot('x26')).toBe(true);
	});
});

test('the cache path comes from the account database, not $HOME', () => {
	const script = "import os from 'node:os'; import { defaultRoot } from './spike-clean.mjs'; console.log(os.homedir()); console.log(defaultRoot());";
	const child = Bun.spawnSync([process.execPath, '-e', script], {
		cwd: import.meta.dir,
		env: { ...process.env, HOME: base },
	});
	expect(child.exitCode).toBe(0);
	const [childHome, childRoot] = child.stdout.toString().trim().split('\n');
	// The child did see the other $HOME, so the check below means something.
	expect(childHome).toBe(base);
	expect(childRoot.startsWith(base)).toBe(false);
	expect(childRoot).toBe(defaultRoot());
	expect(childRoot.endsWith(path.join('Library', 'Caches', 'wraithbox-spikes'))).toBe(true);
});
